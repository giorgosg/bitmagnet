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

// build assembles a stack over the given database and serves it.
func build(t *testing.T, db *dbtest.DB, cfg authconfig.Config) (*fixtureserver.Stack, *httptest.Server) {
	t.Helper()

	stack, err := fixtureserver.Build(fixtureserver.Options{
		Config:              cfg,
		Provider:            daoProvider{query: db.Query},
		Logger:              zap.NewNop().Sugar(),
		JWTSecret:           "fixtureserver-test-secret",
		PasswordHashingCost: bcrypt.MinCost,
	})
	require.NoError(t, err)

	server := httptest.NewServer(stack.Engine)
	t.Cleanup(server.Close)

	return stack, server
}

func TestBuildRequiresAProvider(t *testing.T) {
	t.Parallel()

	_, err := fixtureserver.Build(fixtureserver.Options{Config: authconfig.NewDefaultConfig()})
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
	assert.Equal(t, "UNAUTHORIZED", refused.Errors[0].Extensions["code"])

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
