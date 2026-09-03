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
	"fmt"

	"github.com/99designs/gqlgen/graphql"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/api_key"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
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
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	torznab_httpserver "github.com/bitmagnet-io/bitmagnet/internal/torznab/httpserver"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
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

	rbacService := rbac.NewService(
		rbac.NewRepository(opts.Provider),
		objectActionProvider,
		rbac.PermissionProviders(
			rbac.CorePermissions,
			rbac.VerbatimPermissions(objectActionProvider),
			gqlauth.Permissions,
			authconfig.AnonymousPermissions(opts.Config, objectActionProvider),
		),
		rbac.CacheTTL(opts.Config.RBACCacheTTL),
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

	// Only the resolver dependencies the index and the auth workflows need are
	// wired. Workers, health, queue and processor metrics, and the blocking
	// manager stay nil deliberately: each drags in a subsystem this stack has no
	// business starting, and nothing it serves is asked for them.
	schema := newSchema(&resolvers.Resolver{
		Dao:           query,
		Search:        searchService,
		UserService:   userService,
		APIKeyService: apiKeyService,
		RBACService:   rbacService,
		BrowserCookie: cookie,
	})

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
