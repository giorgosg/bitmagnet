package fixtureserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/dev/fixtureserver"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

type daoProvider struct{ query *dao.Query }

func (p daoProvider) Dao() (*dao.Query, error) { return p.query, nil }

func (p daoProvider) DaoTransaction(fn func(tx *dao.Query) error) error {
	return p.query.Transaction(fn)
}

type gqlError struct {
	Message    string         `json:"message"`
	Extensions map[string]any `json:"extensions"`
}

type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []gqlError      `json:"errors"`
}

type healthCheckItem struct {
	Key    string  `json:"key"`
	Status string  `json:"status"`
	Error  *string `json:"error"`
}

type healthQuery struct {
	Status string            `json:"status"`
	Checks []healthCheckItem `json:"checks"`
}

type healthResponse struct {
	Health healthQuery `json:"health"`
}

type workerItem struct {
	Key     string `json:"key"`
	Started bool   `json:"started"`
}

type workersListAll struct {
	Workers []workerItem `json:"workers"`
}

type workersQuery struct {
	ListAll workersListAll `json:"listAll"`
}

type workersResponse struct {
	Workers workersQuery `json:"workers"`
}

type queueMetricsBucket struct {
	Queue  string `json:"queue"`
	Status string `json:"status"`
	Count  int    `json:"count"`
}

type queueMetricsResult struct {
	Buckets []queueMetricsBucket `json:"buckets"`
}

type queueMetricsQuery struct {
	Metrics queueMetricsResult `json:"metrics"`
}

type queueMetricsResponse struct {
	Queue queueMetricsQuery `json:"queue"`
}

type torrentSourceItem struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type listSourcesResult struct {
	Sources []torrentSourceItem `json:"sources"`
}

type torrentMetricsBucket struct {
	Source string `json:"source"`
	Count  int    `json:"count"`
}

type torrentMetricsResult struct {
	Buckets []torrentMetricsBucket `json:"buckets"`
}

type torrentQueryResponseBody struct {
	ListSources listSourcesResult    `json:"listSources"`
	Metrics     torrentMetricsResult `json:"metrics"`
}

type listSourcesResponse struct {
	Torrent torrentQueryResponseBody `json:"torrent"`
}

type torrentMetricsResponse struct {
	Torrent torrentQueryResponseBody `json:"torrent"`
}

type queueAgg struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

type queueJobsAggregations struct {
	Queue []queueAgg `json:"queue"`
}

type queueJobsResult struct {
	TotalCount   int                   `json:"totalCount"`
	Aggregations queueJobsAggregations `json:"aggregations"`
}

type queueJobsQuery struct {
	Jobs queueJobsResult `json:"jobs"`
}

type queueJobsResponse struct {
	Queue queueJobsQuery `json:"queue"`
}

type searchItem struct {
	InfoHash string `json:"infoHash"`
}

type searchResult struct {
	TotalCount int          `json:"totalCount"`
	Items      []searchItem `json:"items"`
}

type torrentContentQuery struct {
	Search searchResult `json:"search"`
}

type searchResponse struct {
	TorrentContent torrentContentQuery `json:"torrentContent"`
}

type loginResult struct {
	Token string `json:"token"`
}

type selfMutation struct {
	Login loginResult `json:"login"`
}

type loginResponse struct {
	Self selfMutation `json:"self"`
}

// query posts a GraphQL document, optionally as a bearer identity.
func query(t *testing.T, server *httptest.Server, token, document string) gqlResponse {
	t.Helper()

	body, err := json.Marshal(map[string]string{"query": document})
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		server.URL+"/graphql",
		strings.NewReader(string(body)),
	)
	require.NoError(t, err)

	req.Header.Set("Content-Type", "application/json")

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	res, err := server.Client().Do(req)
	require.NoError(t, err)

	defer func() { _ = res.Body.Close() }()

	var decoded gqlResponse
	require.NoError(t, json.NewDecoder(res.Body).Decode(&decoded))

	return decoded
}

// requireNoGqlErrors fails with the GraphQL errors rather than on a decode of an
// empty data field, which is what a nil resolver dependency produces.
func requireNoGqlErrors(t *testing.T, res gqlResponse) {
	t.Helper()

	for _, e := range res.Errors {
		t.Logf("graphql error: %s", e.Message)
	}

	require.Empty(t, res.Errors)
}

// build assembles a stack over the given database and serves it.
func build(t *testing.T, db *dbtest.DB, cfg authconfig.Config) (*fixtureserver.Stack, *httptest.Server) {
	t.Helper()

	stack, err := fixtureserver.Build(t.Context(), fixtureserver.Options{
		Config:                    cfg,
		GrantAnonymousReadSurface: cfg.AnonymousAccess,
		Provider:                  daoProvider{query: db.Query},
		Logger:                    zap.NewNop().Sugar(),
		JWTSecret:                 "fixtureserver-test-secret",
		PasswordHashingCost:       bcrypt.MinCost,
	})
	require.NoError(t, err)

	server := httptest.NewServer(stack.Engine)
	t.Cleanup(server.Close)

	return stack, server
}

// buildWithOptions serves a stack whose Options the caller adjusts, for the
// settings that are not part of authconfig.
func buildWithOptions(
	t *testing.T,
	db *dbtest.DB,
	adjust func(*fixtureserver.Options),
) (*fixtureserver.Stack, *httptest.Server) {
	t.Helper()

	opts := fixtureserver.Options{
		Config:                    authconfig.NewDefaultConfig(),
		GrantAnonymousReadSurface: true,
		Provider:                  daoProvider{query: db.Query},
		Logger:                    zap.NewNop().Sugar(),
		JWTSecret:                 "fixtureserver-test-secret",
		PasswordHashingCost:       bcrypt.MinCost,
	}
	adjust(&opts)

	stack, err := fixtureserver.Build(t.Context(), opts)
	require.NoError(t, err)

	server := httptest.NewServer(stack.Engine)
	t.Cleanup(server.Close)

	return stack, server
}

func TestBuildRequiresAProvider(t *testing.T) {
	t.Parallel()

	_, err := fixtureserver.Build(t.Context(), fixtureserver.Options{Config: authconfig.NewDefaultConfig()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database provider is required")
}

// The stack has to serve the index, not just the auth mutations. Search was
// initially left unwired, which answered every torrentContent query with an
// opaque "internal system error" — a fixture server whose whole point is
// content, serving none.
//
// This is the only test here that takes a seeded clone, and deliberately so. A
// clone is a file copy of the whole ~1GB template; three parallel ones, across
// packages that `go test ./...` also runs in parallel, is enough concurrent I/O
// to bring a developer machine down. Ask for content only when the assertion is
// about content.
func TestSeededStackServesTheCorpus(t *testing.T) {
	t.Parallel()

	db := dbtest.NewSeeded(t)

	cfg := authconfig.NewDefaultConfig()
	_, server := build(t, db, cfg)

	res := query(t, server, "",
		`{ torrentContent { search(input:{limit:2, totalCount:true}) { totalCount items { infoHash } } } }`)
	require.Empty(t, res.Errors, "search must succeed against a seeded database")

	var decoded searchResponse
	require.NoError(t, json.Unmarshal(res.Data, &decoded))

	assert.Greater(t, decoded.TorrentContent.Search.TotalCount, 1_000,
		"the seed template carries ~100k contents; a near-empty index means the clone is not the fixture")
	assert.Len(t, decoded.TorrentContent.Search.Items, 2)
}

// The workflow a harness runs first: read the invitation, register through it,
// and get an administrator.
func TestBootstrapInvitationRegistersAnAdministrator(t *testing.T) {
	t.Parallel()

	// Empty, not seeded: this is the auth workflow, and content would only make
	// it cost a clone. See the note on TestSeededStackServesTheCorpus.
	db := dbtest.New(t)

	cfg := authconfig.NewDefaultConfig()
	cfg.AnonymousAccess = false

	stack, server := build(t, db, cfg)

	invitation, err := stack.UserService.CreateInitialInvitation(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, invitation.Code)

	// Anonymous access is off, so the index is closed until the harness has an
	// identity. That is the state the credentialed suite needs to test against.
	refused := query(t, server, "", `{ torrentContent { search(input:{limit:1}) { totalCount } } }`)
	require.NotEmpty(t, refused.Errors)
	assert.Equal(t, "AUTHENTICATION_REQUIRED", refused.Errors[0].Extensions["code"])

	registered := query(t, server, "", `mutation { self { register(input:{
		username:"harness",
		password:"correct-horse-battery-staple-9271",
		invitationCode:"`+invitation.Code+`"
	}) { user { username role } } } }`)
	require.Empty(t, registered.Errors)
	assert.Contains(t, string(registered.Data), `"role":"admin"`)

	loggedIn := query(t, server, "", `mutation { self { login(
		username:"harness",
		password:"correct-horse-battery-staple-9271"
	) { token } } }`)
	require.Empty(t, loggedIn.Errors)

	var login loginResponse
	require.NoError(t, json.Unmarshal(loggedIn.Data, &login))
	require.NotEmpty(t, login.Self.Login.Token)

	// The same query the anonymous caller was refused now succeeds.
	allowed := query(t, server, login.Self.Login.Token,
		`{ torrentContent { search(input:{limit:1, totalCount:true}) { totalCount } } }`)
	assert.Empty(t, allowed.Errors)
}

// Magnes renders throttling as its own wait state and cannot currently test it.
// The throttle has to be provokable inside a test's patience, which means the
// rate and the burst both have to be settable.
func TestLoginThrottleIsProvokable(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)

	cfg := authconfig.NewDefaultConfig()
	cfg.LoginRequestsPerMinute = 1
	cfg.LoginRequestBurst = 1

	_, server := build(t, db, cfg)

	const attempts = 3

	codes := make([]string, 0, attempts)

	for range attempts {
		res := query(t, server, "", `mutation { self { login(
			username:"nobody",
			password:"whatever-it-does-not-matter"
		) { token } } }`)
		require.NotEmpty(t, res.Errors)

		code, _ := res.Errors[0].Extensions["code"].(string)
		codes = append(codes, code)
	}

	assert.Contains(t, codes, "LOGIN_THROTTLED",
		"a burst of 1 at 1/minute must throttle within three attempts")
}

// The operational pages are the other half of what an external browser suite
// needs to drive, and the stack used to leave all three unwired: Workers,
// Checker and QueueMetricsClient stayed nil, so `workers`, `health` and
// `queue.metrics` each panicked on a nil interface and surfaced as an opaque
// "internal system error". A browser suite cannot cover a page the fixture
// cannot answer.
//
// `queue.jobs` is the exception that already worked — it resolves through
// Search, not the metrics client — and is asserted below so the distinction
// stays visible.

func TestStackAnswersHealth(t *testing.T) {
	t.Parallel()

	_, server := build(t, dbtest.New(t), authconfig.NewDefaultConfig())

	res := query(t, server, "", `{ health { status checks { key status error } } }`)
	requireNoGqlErrors(t, res)

	var body healthResponse
	require.NoError(t, json.Unmarshal(res.Data, &body))

	// A real check against the database behind this stack, so the page has
	// something true to render rather than an empty list.
	assert.Equal(t, "up", body.Health.Status)
	require.NotEmpty(t, body.Health.Checks)

	keys := make(map[string]string, len(body.Health.Checks))
	for _, check := range body.Health.Checks {
		keys[check.Key] = check.Status
	}

	assert.Equal(t, "up", keys["postgres"], "the database check must report on the real connection")
}

func TestStackAnswersWorkers(t *testing.T) {
	t.Parallel()

	_, server := build(t, dbtest.New(t), authconfig.NewDefaultConfig())

	res := query(t, server, "", `{ workers { listAll { workers { key started } } } }`)
	requireNoGqlErrors(t, res)

	var body workersResponse
	require.NoError(t, json.Unmarshal(res.Data, &body))

	workers := body.Workers.ListAll.Workers
	require.NotEmpty(t, workers, "the page needs workers to list")

	started := 0
	stopped := 0

	for _, w := range workers {
		assert.NotEmpty(t, w.Key)

		if w.Started {
			started++
		} else {
			stopped++
		}
	}

	// Both states present, so a browser test can cover how each renders.
	assert.Positive(t, started, "at least one worker must report started")
	assert.Positive(t, stopped, "at least one worker must report stopped")
}

func TestStackAnswersQueueMetrics(t *testing.T) {
	t.Parallel()

	// Seeded, because the criterion is "answers with data": an unseeded clone has
	// no queue jobs at all, so an empty bucket list would pass an
	// absence-of-errors assertion while telling a chart nothing.
	_, server := buildWithOptions(t, dbtest.New(t), func(o *fixtureserver.Options) {
		o.SeedDashboardData = true
	})

	res := query(t, server, "", `{ queue { metrics(input: {bucketDuration: hour}) {
		buckets { queue status count }
	} } }`)
	requireNoGqlErrors(t, res)

	var body queueMetricsResponse
	require.NoError(t, json.Unmarshal(res.Data, &body))

	buckets := body.Queue.Metrics.Buckets
	require.NotEmpty(t, buckets, "the chart needs buckets")

	statuses := map[string]struct{}{}
	queues := map[string]struct{}{}
	maxCount := 0

	for _, b := range buckets {
		statuses[b.Status] = struct{}{}
		queues[b.Queue] = struct{}{}

		if b.Count > maxCount {
			maxCount = b.Count
		}
	}

	assert.Len(t, statuses, 4, "every status should reach the chart")
	assert.Len(t, queues, 2, "both queues should reach the chart")
	assert.Greater(t, maxCount, 1,
		"the pair seeded ten minutes apart must share an hourly bucket, or every column is 1")
}

// jobs resolves through Search rather than the metrics client, so it worked
// before the other three were wired. Asserted so a future change that routes it
// through the client does not break it silently.
func TestStackAnswersQueueJobs(t *testing.T) {
	t.Parallel()

	_, server := build(t, dbtest.New(t), authconfig.NewDefaultConfig())

	res := query(t, server, "", `{ queue { jobs(input: {}) { totalCount } } }`)
	requireNoGqlErrors(t, res)
}

// adminToken registers the first administrator through the bootstrap invitation
// and returns a bearer token for it. The authenticated surfaces need one, and
// doing it by hand in each test buries what the test is actually about.
func adminToken(t *testing.T, stack *fixtureserver.Stack, server *httptest.Server) string {
	t.Helper()

	invitation, err := stack.UserService.CreateInitialInvitation(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, invitation.Code)

	const password = "correct-horse-battery-staple-9271"

	registered := query(t, server, "", `mutation { self { register(input:{
		username:"queue-admin",
		password:"`+password+`",
		invitationCode:"`+invitation.Code+`"
	}) { user { role } } } }`)
	requireNoGqlErrors(t, registered)

	loggedIn := query(t, server, "", `mutation { self { login(
		username:"queue-admin",
		password:"`+password+`"
	) { token } } }`)
	requireNoGqlErrors(t, loggedIn)

	var login loginResponse
	require.NoError(t, json.Unmarshal(loggedIn.Data, &login))
	require.NotEmpty(t, login.Self.Login.Token)

	return login.Self.Login.Token
}

// The queue page has actions, not only readings, and both mutations resolve
// through the queue manager. A nil manager answered an authenticated click with
// an opaque "internal system error" — the same failure as the three queries,
// reached by a browser suite that has logged in rather than one that has not.
func TestStackAnswersQueueMutations(t *testing.T) {
	t.Parallel()

	stack, server := build(t, dbtest.New(t), authconfig.NewDefaultConfig())
	token := adminToken(t, stack, server)

	purged := query(t, server, token,
		`mutation { queue { purgeJobs(input: {queues: ["test_queue"]}) } }`)
	requireNoGqlErrors(t, purged)

	reprocessed := query(t, server, token,
		`mutation { queue { enqueueReprocessTorrentsBatch(input: {}) } }`)
	requireNoGqlErrors(t, reprocessed)
}

// The dashboard ticket names six fields the status, statistics and queue pages
// read. These are the two on `torrent`: metrics resolves through the torrent
// metrics client, and listSources reads the sources table through Search. The
// ticket asked what each one does today rather than assuming, so both are
// asserted here.
func TestStackAnswersTorrentMetrics(t *testing.T) {
	t.Parallel()

	_, server := build(t, dbtest.New(t), authconfig.NewDefaultConfig())

	res := query(t, server, "", `{ torrent { metrics(input: {bucketDuration: hour}) {
		buckets { source bucket count }
	} } }`)

	// Answers, and on a bare database answers empty: the seed's torrent half
	// moves existing source rows rather than inserting synthetic torrents, so
	// there is nothing to move here. The populated case is asserted in
	// TestSeededStackPopulatesTheStatisticsChart.
	requireNoGqlErrors(t, res)
}

func TestStackAnswersTorrentListSources(t *testing.T) {
	t.Parallel()

	_, server := build(t, dbtest.New(t), authconfig.NewDefaultConfig())

	res := query(t, server, "", `{ torrent { listSources { sources { key name } } } }`)
	requireNoGqlErrors(t, res)

	var body listSourcesResponse
	require.NoError(t, json.Unmarshal(res.Data, &body))

	// Migration 00001 seeds these two, so they are present on a bare database.
	keys := make([]string, 0, len(body.Torrent.ListSources.Sources))
	for _, src := range body.Torrent.ListSources.Sources {
		keys = append(keys, src.Key)
	}

	assert.Contains(t, keys, "dht")
}

// The btm-testdb corpus carries no queue_jobs at all — its manifest lists none
// and there is no queue_jobs.tsv — so the jobs table, its status facet and the
// totals chart have nothing to render against a clone. The ticket asked for
// jobs in every QueueJobStatus, behind a flag so the stack's other caller (the
// auth integration tests) is not handed rows it never asked for.
func TestSeedQueueJobsCoversEveryStatus(t *testing.T) {
	t.Parallel()

	_, server := buildWithOptions(t, dbtest.New(t), func(o *fixtureserver.Options) {
		o.SeedDashboardData = true
	})

	for _, status := range []string{"pending", "retry", "failed", "processed"} {
		res := query(t, server, "", `{ queue { jobs(input: {
			statuses: [`+status+`], totalCount: true
		}) { totalCount } } }`)
		requireNoGqlErrors(t, res)

		var body queueJobsResponse
		require.NoError(t, json.Unmarshal(res.Data, &body))

		assert.Positive(t, body.Queue.Jobs.TotalCount,
			"the %s facet needs at least one job to show", status)
	}
}

// More than one queue, so the queue facet has something to discriminate.
func TestSeedQueueJobsCoversMoreThanOneQueue(t *testing.T) {
	t.Parallel()

	_, server := buildWithOptions(t, dbtest.New(t), func(o *fixtureserver.Options) {
		o.SeedDashboardData = true
	})

	res := query(t, server, "", `{ queue { jobs(input: {
		facets: {queue: {aggregate: true}}
	}) { aggregations { queue { value count } } } } }`)
	requireNoGqlErrors(t, res)

	var body queueJobsResponse
	require.NoError(t, json.Unmarshal(res.Data, &body))

	assert.Greater(t, len(body.Queue.Jobs.Aggregations.Queue), 1,
		"the queue facet needs more than one queue to be worth rendering")
}

// Off by default: the auth integration tests build this stack too, and a table
// that silently grows rows would change what their assertions count.
func TestQueueJobsAreNotSeededByDefault(t *testing.T) {
	t.Parallel()

	_, server := build(t, dbtest.New(t), authconfig.NewDefaultConfig())

	res := query(t, server, "", `{ queue { jobs(input: {totalCount: true}) { totalCount } } }`)
	requireNoGqlErrors(t, res)

	var body queueJobsResponse
	require.NoError(t, json.Unmarshal(res.Data, &body))

	assert.Zero(t, body.Queue.Jobs.TotalCount)
}

// The statistics page opens on the last hour, and the seed corpus is a snapshot
// whose torrent source timestamps are months old — so `torrent.metrics` answered
// with zero buckets on the view the page actually shows. The seed's torrent half
// moves a bounded number of those rows into that window.
//
// This takes a seeded clone because that half updates existing rows rather than
// inserting synthetic torrents, which would change what the search pages are
// tested against. It shares the clone cost with nothing else, so it asserts the
// queue half here too rather than paying for a second one.
func TestSeededStackPopulatesTheStatisticsChart(t *testing.T) {
	t.Parallel()

	_, server := buildWithOptions(t, dbtest.NewSeeded(t), func(o *fixtureserver.Options) {
		o.SeedDashboardData = true
	})

	res := query(t, server, "", `{ torrent { metrics(input: {bucketDuration: minute}) {
		buckets { source count }
	} } }`)
	requireNoGqlErrors(t, res)

	var body torrentMetricsResponse
	require.NoError(t, json.Unmarshal(res.Data, &body))

	require.NotEmpty(t, body.Torrent.Metrics.Buckets,
		"the chart's default window must have buckets, or the page shows nothing")

	total := 0
	for _, b := range body.Torrent.Metrics.Buckets {
		total += b.Count
		assert.NotEmpty(t, b.Source)
	}

	assert.Positive(t, total)
}
