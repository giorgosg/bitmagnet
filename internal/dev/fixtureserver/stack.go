// Package fixtureserver assembles bitmagnet's authenticated request path over a
// database the caller supplies.
//
// It exists because two callers need the identical stack and must not drift
// apart: the GraphQL auth integration tests, and the `dev fixture serve`
// command that hands an external browser suite a real instance to drive. A
// second hand-written copy of this wiring would pass its own tests while
// diverging from the one under test, which is the failure this package is here
// to make impossible.
//
// It lives under internal/dev deliberately: nothing here belongs in the shipped
// command surface.
package fixtureserver

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/api_key"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/authfx"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/browser_session"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/http_auth"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/identity"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/jwt"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/user"
	"github.com/bitmagnet-io/bitmagnet/internal/database"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/search"
	"github.com/bitmagnet-io/bitmagnet/internal/gql"
	gqlauth "github.com/bitmagnet-io/bitmagnet/internal/gql/auth"
	"github.com/bitmagnet-io/bitmagnet/internal/gql/directive"
	gqlhttpserver "github.com/bitmagnet-io/bitmagnet/internal/gql/httpserver"
	"github.com/bitmagnet-io/bitmagnet/internal/gql/resolvers"
	"github.com/bitmagnet-io/bitmagnet/internal/health"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/metrics/queuemetrics"
	"github.com/bitmagnet-io/bitmagnet/internal/metrics/torrentmetrics"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/queue/manager"
	torznab_httpserver "github.com/bitmagnet-io/bitmagnet/internal/torznab/httpserver"
	"github.com/bitmagnet-io/bitmagnet/internal/version"
	"github.com/bitmagnet-io/bitmagnet/internal/worker"
	"github.com/gin-gonic/gin"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Options configures a Stack. Only Config, Provider and Logger are required.
type Options struct {
	// Config is the authentication configuration the stack runs under, in full,
	// so a caller varies anonymous access, invitations, the throttle and the JWT
	// lifetime by handing over a different one.
	Config authconfig.Config
	// Provider is the database the stack reads and writes.
	Provider database.DaoTransactionProvider
	// Logger receives the GraphQL server's output.
	Logger *zap.SugaredLogger
	// JWTSecret signs session tokens. A caller that wants tokens to survive its
	// own restart supplies one; otherwise anything unique to the process does.
	JWTSecret string
	// PasswordHashingCost overrides the configured bcrypt cost when non-zero.
	// The integration tests drop it to bcrypt.MinCost because they exercise the
	// authorization path, not the work factor.
	PasswordHashingCost int
	// AuthenticatorOverride replaces the real authenticator, for tests that need
	// to drive a failure the real one cannot be made to produce.
	AuthenticatorOverride identity.Authenticator
	// SeedQueueJobs inserts queue jobs covering every status and more than one
	// queue, spread across time buckets.
	//
	// Off by default, and the default is the point: the btm-testdb corpus carries
	// no queue_jobs, so a browser suite driving the queue page has nothing to
	// render without this — while the GraphQL auth integration tests build the
	// same stack and would have their counts changed by rows they never asked
	// for. The caller that needs the data says so.
	SeedQueueJobs bool
}

// Stack is the assembled server and the services behind it.
type Stack struct {
	// Engine serves the same routes production serves: the auth middleware and
	// the gqlgen handler, mounted through the production http server options
	// rather than by hand, so whatever those options install is covered too.
	Engine *gin.Engine

	UserService   user.Service
	APIKeyService api_key.Service
	RBACService   rbac.Service

	// Search backs the torrentContent and torrent queries, so a caller pointed
	// at a populated database can actually read the index. Without it those
	// resolvers panic on a nil interface, which surfaces as an opaque
	// "internal system error" rather than anything a caller could act on.
	Search search.Search

	// ObjectActions is the registered set: the schema's @auth directives plus
	// the non-GraphQL surfaces' own. createAPIKey checks a requested permission
	// against this, so a stack registering half of it would refuse keys
	// production accepts.
	ObjectActions []rbac.ObjectAction
}

// Build assembles the stack. It registers no routes on any listener and starts
// nothing; the caller decides whether that is an httptest server or a real one.
func Build(opts Options) (*Stack, error) {
	if opts.Provider == nil {
		return nil, fmt.Errorf("fixtureserver: a database provider is required")
	}

	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop().Sugar()
	}

	values := opts.Config.UserValues()
	if opts.PasswordHashingCost != 0 {
		values.PasswordHashingCost.Set(user.PasswordHashingCost(opts.PasswordHashingCost))
	}

	jwtService := jwt.NewService(
		jwt.Secret(opts.JWTSecret),
		jwt.Duration(opts.Config.JWTDuration),
	)
	userService := user.NewService(opts.Provider, jwtService, values)

	// Built exactly as production does, directive and all: without it the schema
	// resolves an identity and then ignores it.
	//
	// Twice, because the object action set is read off the first schema and the
	// services that depend on it are the second's resolvers.
	objectActions := registeredObjectActions(newSchema(nil))
	objectActionProvider := func() []rbac.ObjectAction { return objectActions }

	apiKeyService := api_key.NewService(api_key.NewRepository(opts.Provider), objectActionProvider)

	// What an anonymous caller may do now lives in role_permissions, written once
	// per installation by the translation that authfx runs as a startup worker.
	// This stack is assembled by hand, so it runs the same translation: without it
	// the anon role holds nothing and the fixture would model an instance no real
	// deployment is in.
	if err := authfx.TranslateAnonRole(
		context.Background(),
		opts.Provider,
		opts.Config,
		objectActionProvider,
		logger,
	); err != nil {
		return nil, fmt.Errorf("fixtureserver: translating the anon role: %w", err)
	}

	rbacService := rbac.NewService(
		rbac.NewRepository(opts.Provider),
		objectActionProvider,
		rbac.PermissionProviders(
			rbac.CorePermissions,
			rbac.VerbatimPermissions(objectActionProvider),
			gqlauth.Permissions,
		),
		rbac.CacheTTL(opts.Config.RBACCacheTTL),
		rbac.AnonymousAccess(opts.Config.AnonymousAccess),
	)

	authenticator := identity.NewAuthenticator(jwtService, userService, apiKeyService, rbacService)
	if opts.AuthenticatorOverride != nil {
		authenticator = opts.AuthenticatorOverride
	}

	query, err := opts.Provider.Dao()
	if err != nil {
		return nil, fmt.Errorf("fixtureserver: opening the dao: %w", err)
	}

	searchService, err := newSearch(query)
	if err != nil {
		return nil, err
	}

	cookie := browser_session.NewCookie(opts.Config)

	healthChecker, err := newHealthChecker(query)
	if err != nil {
		return nil, err
	}

	queueMetricsClient, err := newQueueMetricsClient(query)
	if err != nil {
		return nil, err
	}

	queueManager, err := newQueueManager(query)
	if err != nil {
		return nil, err
	}

	torrentMetricsClient, err := newTorrentMetricsClient(query)
	if err != nil {
		return nil, err
	}

	workerRegistry, err := newWorkerRegistry(context.Background(), logger)
	if err != nil {
		return nil, err
	}

	// The processor and the blocking manager stay nil deliberately: each drags in
	// a subsystem this stack has no business starting, and nothing it serves asks
	// for them. Workers, health, the queue surface and torrent metrics used to be
	// in that list and should not have been — they back the status, statistics
	// and queue pages an external browser suite has to drive, and a nil interface
	// answers them with an opaque "internal system error".
	//
	// `queue.jobs` and `torrent.listSources` need nothing beyond Search and the
	// dao, and answered before any of this.
	schema := newSchema(&resolvers.Resolver{
		Dao:                query,
		Search:             searchService,
		Workers:            workerRegistry,
		Checker:            healthChecker,
		QueueMetricsClient: queueMetricsClient,
		QueueManager:       queueManager,

		TorrentMetricsClient: torrentMetricsClient,
		UserService:          userService,
		APIKeyService:        apiKeyService,
		RBACService:          rbacService,
		BrowserCookie:        cookie,
	})

	if opts.SeedQueueJobs {
		if err = seedQueueJobs(context.Background(), query); err != nil {
			return nil, err
		}
	}

	engine := gin.New()

	// The production options rather than the middlewares by hand, so this covers
	// what those options install as well as what they are given.
	authOption := http_auth.New(http_auth.Params{
		Middleware: http_auth.NewMiddleware(authenticator, cookie),
	}).Option
	if err := authOption.Apply(engine); err != nil {
		return nil, fmt.Errorf("fixtureserver: applying the auth option: %w", err)
	}

	graphQLOption := gqlhttpserver.New(gqlhttpserver.Params{
		Schema: lazy.New(func() (graphql.ExecutableSchema, error) {
			return schema, nil
		}),
		Logger:        logger,
		BrowserCookie: cookie,
	}).Option
	if err := graphQLOption.Apply(engine); err != nil {
		return nil, fmt.Errorf("fixtureserver: applying the graphql option: %w", err)
	}

	return &Stack{
		Engine:        engine,
		UserService:   userService,
		APIKeyService: apiKeyService,
		RBACService:   rbacService,
		Search:        searchService,
		ObjectActions: objectActions,
	}, nil
}

// seedQueueJobs inserts a small, deterministic set of queue jobs: every status,
// two queues, and created_at spread over several hours so a chart bucketed by
// hour has more than one column.
//
// The jobs are not meant to be run. This stack leaves the queue server unwired,
// so nothing picks them up, and the pending ones stay pending for the suite to
// look at.
func seedQueueJobs(ctx context.Context, query *dao.Query) error {
	now := time.Now().UTC()

	type spec struct {
		queue    string
		status   model.QueueJobStatus
		ageHours int
	}

	specs := make([]spec, 0, len(seededQueueNames)*4)

	for i, queue := range seededQueueNames {
		for j, status := range []model.QueueJobStatus{
			model.QueueJobStatusPending,
			model.QueueJobStatusRetry,
			model.QueueJobStatusFailed,
			model.QueueJobStatusProcessed,
		} {
			// Two per status per queue, an hour apart, so the chart has columns
			// and the facet counts are not all 1.
			specs = append(specs,
				spec{queue: queue, status: status, ageHours: i*4 + j},
				spec{queue: queue, status: status, ageHours: i*4 + j + 1},
			)
		}
	}

	jobs := make([]*model.QueueJob, 0, len(specs))

	for i, sp := range specs {
		// The payload differs per job because NewQueueJob fingerprints
		// queue+payload, and the fingerprint is how the queue deduplicates.
		job, err := model.NewQueueJob(sp.queue, map[string]any{"seed": i})
		if err != nil {
			return fmt.Errorf("fixtureserver: building a seed queue job: %w", err)
		}

		createdAt := now.Add(-time.Duration(sp.ageHours) * time.Hour)
		job.Status = sp.status
		job.CreatedAt = createdAt
		job.RunAfter = createdAt

		// Everything that has left pending has run, and the chart's latency comes
		// from the gap between the two.
		if sp.status != model.QueueJobStatusPending {
			job.RanAt = sql.NullTime{Time: createdAt.Add(time.Second * 30), Valid: true}
		}

		if sp.status == model.QueueJobStatusFailed || sp.status == model.QueueJobStatusRetry {
			job.Retries = 1
			job.Error = model.NewNullString("seeded failure, so the error column has something to show")
		}

		jobs = append(jobs, &job)
	}

	if err := query.QueueJob.WithContext(ctx).CreateInBatches(jobs, 50); err != nil {
		return fmt.Errorf("fixtureserver: seeding queue jobs: %w", err)
	}

	return nil
}

// seededQueueNames are real queue names, so the facet reads like production's.
var seededQueueNames = []string{"process_torrent", "process_torrent_batch"}

// newHealthChecker builds the checker the `health` query reports on.
//
// One real check, against the database this stack was handed, so the page has
// something true to render rather than an empty list — and so a browser suite
// can assert a status rather than merely that the field resolved.
//
// It differs from production in one way, deliberately. `database/healthcheck`
// registers its postgres check with WithPeriodicCheck, which runs it on a timer
// in its own goroutine for the life of the process. This stack is built per
// test, so a synchronous check is the right shape: Checker.Check runs it on
// demand, and the response is identical either way.
func newHealthChecker(query *dao.Query) (health.Checker, error) {
	sqlDB, err := query.UnderlyingDB().DB()
	if err != nil {
		return nil, fmt.Errorf("fixtureserver: opening the sql database: %w", err)
	}

	return health.NewChecker(
		health.WithCheck(health.Check{
			Name:    "postgres",
			Timeout: healthCheckTimeout,
			Check: func(ctx context.Context) error {
				if pingErr := sqlDB.PingContext(ctx); pingErr != nil {
					return fmt.Errorf("failed to ping database: %w", pingErr)
				}

				return nil
			},
		}),
		// Mirrors internal/version/healthcheck, which contributes the same info
		// to the real checker.
		health.WithInfo(map[string]any{
			"name":    "bitmagnet",
			"version": version.GitTag,
		}),
	), nil
}

const healthCheckTimeout = time.Second * 5

// newQueueMetricsClient builds the client behind `queue.metrics`, through the
// production constructor rather than by reaching into the package, so the
// fixture cannot drift from what the real graph provides.
//
// It reads `queue_jobs` directly, so a caller pointed at a populated database
// gets real buckets.
func newQueueMetricsClient(query *dao.Query) (queuemetrics.Client, error) {
	client, err := queuemetrics.New(queuemetrics.Params{
		DB: lazy.New(func() (*gorm.DB, error) { return query.UnderlyingDB(), nil }),
	}).Client.Get()
	if err != nil {
		return nil, fmt.Errorf("fixtureserver: building the queue metrics client: %w", err)
	}

	return client, nil
}

// newQueueManager builds the manager behind the `queue` mutations, purgeJobs and
// enqueueReprocessTorrentsBatch.
//
// It is wired for the same reason the queries are: the queue page has actions,
// and an authenticated caller clicking one reached a nil interface. The manager
// starts nothing — it takes the dao and the gorm handle this stack already has,
// and the queue *server* that would consume what it enqueues stays unwired, so
// a job this creates sits in the table for the suite to assert on rather than
// being picked up and run.
func newQueueManager(query *dao.Query) (manager.Manager, error) {
	queueManager, err := manager.New(manager.Params{
		Dao: lazy.New(func() (*dao.Query, error) { return query, nil }),
		DB:  lazy.New(func() (*gorm.DB, error) { return query.UnderlyingDB(), nil }),
	}).Manager.Get()
	if err != nil {
		return nil, fmt.Errorf("fixtureserver: building the queue manager: %w", err)
	}

	return queueManager, nil
}

// newTorrentMetricsClient builds the client behind `torrent.metrics`, which the
// statistics page reads. Like the queue client it goes through the production
// constructor and reads the torrent tables directly.
func newTorrentMetricsClient(query *dao.Query) (torrentmetrics.Client, error) {
	client, err := torrentmetrics.New(torrentmetrics.Params{
		DB: lazy.New(func() (*gorm.DB, error) { return query.UnderlyingDB(), nil }),
	}).Client.Get()
	if err != nil {
		return nil, fmt.Errorf("fixtureserver: building the torrent metrics client: %w", err)
	}

	return client, nil
}

// newWorkerRegistry builds the registry `workers` lists, holding the keys
// production registers.
//
// The started ones are the work this stack actually does: it serves HTTP, and
// Build has already run the anon role translation. The crawler, the queue
// server and the invitation worker are listed and stopped, because this stack
// genuinely does not run them — which is both honest and what a browser suite
// needs, since a page rendering worker state wants both states present.
//
// Their hooks are empty, so starting one does nothing but mark it started.
func newWorkerRegistry(ctx context.Context, logger *zap.SugaredLogger) (worker.Registry, error) {
	keys := append(append([]string{}, startedWorkerKeys...), stoppedWorkerKeys...)
	workers := make([]worker.Worker, 0, len(keys))

	for _, key := range keys {
		workers = append(workers, worker.NewWorker(key, fx.Hook{}))
	}

	result, err := worker.NewRegistry(worker.RegistryParams{
		Shutdowner: noopShutdowner{},
		Workers:    workers,
		Logger:     logger,
	})
	if err != nil {
		return nil, fmt.Errorf("fixtureserver: building the worker registry: %w", err)
	}

	registry := result.Registry

	if err = registry.Enable(startedWorkerKeys...); err != nil {
		return nil, fmt.Errorf("fixtureserver: enabling workers: %w", err)
	}

	if err = registry.Start(ctx); err != nil {
		return nil, fmt.Errorf("fixtureserver: starting workers: %w", err)
	}

	return registry, nil
}

var (
	startedWorkerKeys = []string{"http_server", "auth_anon_role_translation"}
	stoppedWorkerKeys = []string{"dht_crawler", "queue_server", "auth_initial_invitation"}
)

// noopShutdowner stands in for fx's. The registry takes one so a worker can
// bring the process down; nothing this stack starts has a hook that could.
type noopShutdowner struct{}

func (noopShutdowner) Shutdown(...fx.ShutdownOption) error { return nil }

// newSearch builds the search service the same way searchfx does, through its
// own constructor rather than by reaching into the package.
func newSearch(query *dao.Query) (search.Search, error) {
	result := search.New(search.Params{
		Query: lazy.New(func() (*dao.Query, error) { return query, nil }),
	})

	searchService, err := result.Search.Get()
	if err != nil {
		return nil, fmt.Errorf("fixtureserver: building the search service: %w", err)
	}

	return searchService, nil
}

// newSchema builds the executable schema with the @auth directive wired in. A
// nil resolver is legitimate for the first pass, which only reads the schema's
// directives.
func newSchema(root *resolvers.Resolver) graphql.ExecutableSchema {
	if root == nil {
		root = &resolvers.Resolver{}
	}

	return gql.NewExecutableSchema(gql.Config{
		Resolvers: root,
		Directives: gql.DirectiveRoot{
			Auth: gqlauth.NewDirective(),
		},
	})
}

// registeredObjectActions is the whole registry: the @auth directives in the
// schema, plus the object actions the non-GraphQL surfaces contribute through
// the same fx value group in authfx.
func registeredObjectActions(schema graphql.ExecutableSchema) []rbac.ObjectAction {
	return append(
		gqlauth.ObjectActions(
			directive.ExtractAuthDirectives(directive.ExtractSchemaDirectives(schema.Schema())),
		),
		append(http_auth.ObjectActionProvider()(), torznab_httpserver.ObjectAction)...,
	)
}
