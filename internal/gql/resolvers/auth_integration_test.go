package resolvers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

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
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/gql"
	gqlauth "github.com/bitmagnet-io/bitmagnet/internal/gql/auth"
	"github.com/bitmagnet-io/bitmagnet/internal/gql/directive"
	gqlhttpserver "github.com/bitmagnet-io/bitmagnet/internal/gql/httpserver"
	"github.com/bitmagnet-io/bitmagnet/internal/gql/resolvers"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	torznab_httpserver "github.com/bitmagnet-io/bitmagnet/internal/torznab/httpserver"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// TestMain puts gin in test mode once, before any test starts.
//
// gin.SetMode writes package-level globals that gin.New and route registration
// read back through IsDebugging and debugPrintRoute. Calling it from the test
// helper instead meant every parallel test wrote those globals while its
// siblings read them, which the race detector reports. Setting it here happens
// before the first parallel test, so nothing writes them again afterwards.
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

type daoProvider struct{ query *dao.Query }

type invitationCountResponse struct {
	Auth invitationCountAuth `json:"auth"`
}

type invitationCountAuth struct {
	ListInvitations invitationCountResult `json:"listInvitations"`
}

type invitationCountResult struct {
	TotalCount int `json:"totalCount"`
}

func (p daoProvider) Dao() (*dao.Query, error) { return p.query, nil }

func (p daoProvider) DaoTransaction(fn func(tx *dao.Query) error) error {
	return p.query.Transaction(fn)
}

// authTestServer stands up the real request path — gin, the context bridge, the
// auth middleware and the gqlgen handler over a live database. Testing the
// resolvers in isolation would miss the part most likely to break: the identity
// is attached to the gin context, while resolvers only see the request context.
func newAuthTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()

	return newAuthTestServerWithConfig(t, authconfig.NewDefaultConfig())
}

func newAuthTestServerWithConfig(
	t *testing.T,
	cfg authconfig.Config,
) (*httptest.Server, string) {
	t.Helper()

	return newAuthTestServerWithConfigAndAuthenticator(t, cfg, nil)
}

func newAuthTestServerWithConfigAndAuthenticator(
	t *testing.T,
	cfg authconfig.Config,
	authenticatorOverride identity.Authenticator,
) (*httptest.Server, string) {
	t.Helper()

	db := dbtest.New(t)

	var provider database.DaoTransactionProvider = daoProvider{query: db.Query}

	values := cfg.UserValues()
	// These tests exercise the GraphQL auth path, not bcrypt's work factor.
	values.PasswordHashingCost.Set(user.PasswordHashingCost(bcrypt.MinCost))

	jwtService := jwt.NewService(jwt.Secret("test-secret"), jwt.Duration(cfg.JWTDuration))
	userService := user.NewService(
		provider,
		jwtService,
		values.InvitationRequired,
		values.EmailRequired,
		values.EmailVerification,
		values.PasswordMinEntropy,
		values.PasswordHashingCost,
		values.LoginRequestsPerMinute,
		values.LoginRequestBurst,
	)
	// Built exactly as production does, directive and all: without it the
	// schema resolves an identity and then ignores it.
	schema := gql.NewExecutableSchema(gql.Config{
		Resolvers: &resolvers.Resolver{},
		Directives: gql.DirectiveRoot{
			Auth: gqlauth.NewDirective(),
		},
	})

	// The @auth directives in the schema are the GraphQL object action set. The
	// non-GraphQL surfaces contribute their own through the same value group in
	// authfx, and createAPIKey now checks a requested permission against the
	// whole registry - so a stack registering only half of it would refuse keys
	// production accepts.
	registeredObjectActions := append(
		gqlauth.ObjectActions(
			directive.ExtractAuthDirectives(directive.ExtractSchemaDirectives(schema.Schema())),
		),
		append(http_auth.ObjectActionProvider()(), torznab_httpserver.ObjectAction)...,
	)
	objectActions := func() []rbac.ObjectAction { return registeredObjectActions }

	apiKeyService := api_key.NewService(api_key.NewRepository(provider), objectActions)

	rbacService := rbac.NewService(
		rbac.NewRepository(provider),
		objectActions,
		rbac.PermissionProviders(
			rbac.CorePermissions,
			rbac.VerbatimPermissions(objectActions),
			gqlauth.Permissions,
			authconfig.AnonymousPermissions(cfg, objectActions),
		),
		rbac.CacheTTL(time.Minute),
	)

	authenticator := identity.NewAuthenticator(jwtService, userService, apiKeyService, rbacService)
	if authenticatorOverride != nil {
		authenticator = authenticatorOverride
	}

	schema = gql.NewExecutableSchema(gql.Config{
		Resolvers: &resolvers.Resolver{
			UserService:   userService,
			APIKeyService: apiKeyService,
			RBACService:   rbacService,
			BrowserCookie: browser_session.NewCookie(cfg),
		},
		Directives: gql.DirectiveRoot{
			Auth: gqlauth.NewDirective(),
		},
	})

	engine := gin.New()

	// Apply the production http server option rather than mounting the
	// middlewares by hand, so this also covers what that option installs.
	authOption := http_auth.New(http_auth.Params{
		Middleware: http_auth.NewMiddleware(authenticator, browser_session.NewCookie(cfg)),
	}).Option
	require.Equal(t, "auth", authOption.Key())
	require.NoError(t, authOption.Apply(engine))

	graphQLOption := gqlhttpserver.New(gqlhttpserver.Params{
		Schema: lazy.New(func() (graphql.ExecutableSchema, error) {
			return schema, nil
		}),
		Logger:        zap.NewNop().Sugar(),
		BrowserCookie: browser_session.NewCookie(cfg),
	}).Option
	require.Equal(t, "graphql", graphQLOption.Key())
	require.NoError(t, graphQLOption.Apply(engine))

	// The bootstrap invitation is what makes the first registration possible.
	invitation, err := userService.CreateInitialInvitation(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, invitation.Code)

	server := httptest.NewServer(engine)
	t.Cleanup(server.Close)

	return server, invitation.Code
}

type gqlError struct {
	Message    string         `json:"message"`
	Path       []any          `json:"path"`
	Locations  []gqlLocation  `json:"locations"`
	Extensions map[string]any `json:"extensions"`
}

type gqlLocation struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []gqlError      `json:"errors"`
}

// Named response shapes, rather than anonymous nested structs, to decode the
// parts of the payload each test asserts on.
type objectActionBody struct {
	Namespace string `json:"namespace"`
	Object    string `json:"object"`
	Action    string `json:"action"`
}

type permissionBody struct {
	ObjectAction objectActionBody `json:"objectAction"`
}

type loginBody struct {
	Token       string           `json:"token"`
	User        userBody         `json:"user"`
	Permissions []permissionBody `json:"permissions"`
}

type selfLoginBody struct {
	Login loginBody `json:"login"`
}

type loginResponse struct {
	Self selfLoginBody `json:"self"`
}

type userBody struct {
	Username    string     `json:"username"`
	Role        string     `json:"role"`
	LastLoginAt *time.Time `json:"lastLoginAt"`
}

type identityBody struct {
	User        *userBody          `json:"user"`
	APIKey      *apiKeyBody        `json:"apiKey"`
	Permissions []objectActionBody `json:"permissions"`
}

type apiKeyBody struct {
	Name string `json:"name"`
}

type selfIdentityBody struct {
	Identity identityBody `json:"identity"`
}

type identityResponse struct {
	Self selfIdentityBody `json:"self"`
}

type createAPIKeyBody struct {
	APIKey string `json:"apiKey"`
}

type selfCreateAPIKeyBody struct {
	CreateAPIKey createAPIKeyBody `json:"createAPIKey"`
}

type createAPIKeyResponse struct {
	Self selfCreateAPIKeyBody `json:"self"`
}

type invitationBody struct {
	Code string `json:"code"`
}

type authInviteBody struct {
	Invite invitationBody `json:"invite"`
}

type inviteResponse struct {
	Auth authInviteBody `json:"auth"`
}

type authObjectActionsBody struct {
	ListObjectActions []objectActionBody `json:"listObjectActions"`
}

type objectActionsResponse struct {
	Auth authObjectActionsBody `json:"auth"`
}

func query(t *testing.T, server *httptest.Server, token, q string) gqlResponse {
	t.Helper()

	res, _ := queryWithHeaders(t, server, token, q)

	return res
}

func queryWithHeaders(
	t *testing.T,
	server *httptest.Server,
	token, q string,
) (gqlResponse, http.Header) {
	t.Helper()

	return queryWithCredentials(t, server, token, nil, q)
}

func queryWithCredentials(
	t *testing.T,
	server *httptest.Server,
	token string,
	cookie *http.Cookie,
	q string,
) (gqlResponse, http.Header) {
	t.Helper()

	response, headers, _ := queryWithOrigin(t, server, token, cookie, "", q)

	return response, headers
}

func queryWithOrigin(
	t *testing.T,
	server *httptest.Server,
	token string,
	cookie *http.Cookie,
	origin string,
	q string,
) (gqlResponse, http.Header, int) {
	t.Helper()

	body, err := json.Marshal(map[string]string{"query": q})
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		server.URL+"/graphql",
		bytes.NewReader(body),
	)
	require.NoError(t, err)

	req.Header.Set("Content-Type", "application/json")

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	if cookie != nil {
		req.AddCookie(cookie)
	}

	if origin != "" {
		req.Header.Set("Origin", origin)
	}

	res, err := server.Client().Do(req)
	require.NoError(t, err)

	defer func() { require.NoError(t, res.Body.Close()) }()

	var decoded gqlResponse

	require.NoError(t, json.NewDecoder(res.Body).Decode(&decoded))

	return decoded, res.Header, res.StatusCode
}

func sameOrigin(t *testing.T, server *httptest.Server) string {
	t.Helper()

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	return "https://" + serverURL.Host
}

func requireNoGqlErrors(t *testing.T, res gqlResponse) {
	t.Helper()

	for _, e := range res.Errors {
		t.Errorf("graphql error: %s", e.Message)
	}

	require.Empty(t, res.Errors)
}

func requireBrowserCookieAttributes(t *testing.T, cookie *http.Cookie, name string) {
	t.Helper()

	assert.Equal(t, name, cookie.Name)
	assert.Equal(t, "/graphql", cookie.Path)
	assert.Empty(t, cookie.Domain)
	assert.True(t, cookie.HttpOnly)
	assert.True(t, cookie.Secure)
	assert.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
}

func loginBrowserCookie(
	t *testing.T,
	server *httptest.Server,
	username, password string,
) *http.Cookie {
	t.Helper()

	loggedIn, headers, _ := queryWithOrigin(
		t, server, "", nil, sameOrigin(t, server),
		`mutation { self { loginBrowser(username: "`+username+`", password: "`+password+`") } }`,
	)
	requireNoGqlErrors(t, loggedIn)

	cookies := (&http.Response{Header: headers}).Cookies()
	require.Len(t, cookies, 1)

	return cookies[0]
}

// The whole point of the port: an operator can bootstrap an account and use it.
func TestAuthRegisterLoginIdentityFlow(t *testing.T) {
	t.Parallel()

	server, code := newAuthTestServer(t)

	const password = "correct-horse-battery-staple-99"

	registered := query(t, server, "", `mutation { self { register(input: {
		invitationCode: "`+code+`", username: "admin", password: "`+password+`"
	}) { user { id username role } } } }`)
	requireNoGqlErrors(t, registered)
	assert.JSONEq(t,
		`{"self":{"register":{"user":{"id":1,"username":"admin","role":"admin"}}}}`,
		string(registered.Data),
	)

	loggedIn, headers := queryWithHeaders(t, server, "", `mutation { self { login(
		username: "admin", password: "`+password+`"
	) { token user { username role } permissions { objectAction { namespace object action } } } } }`)
	requireNoGqlErrors(t, loggedIn)
	assert.Empty(t, headers.Values("Set-Cookie"), "bearer login must not change its credential contract")

	var login loginResponse

	require.NoError(t, json.Unmarshal(loggedIn.Data, &login))
	require.NotEmpty(t, login.Self.Login.Token, "login must return a token")
	require.NotEmpty(t, login.Self.Login.Permissions, "admin must inherit the core wildcard permission")

	// The identity only reaches the resolver if the gin context bridge, the
	// middleware and the JWT authenticator all work.
	identified := query(t, server, login.Self.Login.Token,
		`{ self { identity { user { username role } permissions { namespace } } } }`)
	requireNoGqlErrors(t, identified)

	var self identityResponse

	require.NoError(t, json.Unmarshal(identified.Data, &self))
	require.NotNil(t, self.Self.Identity.User, "the bearer token must resolve to a user")
	assert.Equal(t, "admin", self.Self.Identity.User.Username)
	assert.Equal(t, "admin", self.Self.Identity.User.Role)
}

func TestLoginReturnsCurrentLastLoginAt(t *testing.T) {
	t.Parallel()

	server, code := newAuthTestServer(t)

	const password = "correct-horse-battery-staple-99"

	requireNoGqlErrors(t, query(t, server, "", `mutation { self { register(input: {
		invitationCode: "`+code+`", username: "admin", password: "`+password+`"
	}) { user { id } } } }`))

	loginStartedAt := time.Now()
	loggedIn := query(t, server, "", `mutation { self { login(
		username: "admin", password: "`+password+`"
	) { token user { lastLoginAt } } } }`)
	loginFinishedAt := time.Now()

	requireNoGqlErrors(t, loggedIn)

	var login loginResponse
	require.NoError(t, json.Unmarshal(loggedIn.Data, &login))
	require.NotNil(t, login.Self.Login.User.LastLoginAt)
	assert.WithinRange(t, *login.Self.Login.User.LastLoginAt, loginStartedAt, loginFinishedAt)

	identified := query(t, server, login.Self.Login.Token,
		`{ self { identity { user { lastLoginAt } } } }`)
	requireNoGqlErrors(t, identified)

	var self identityResponse
	require.NoError(t, json.Unmarshal(identified.Data, &self))
	require.NotNil(t, self.Self.Identity.User)
	require.NotNil(t, self.Self.Identity.User.LastLoginAt)
	assert.Equal(t, login.Self.Login.User.LastLoginAt, self.Self.Identity.User.LastLoginAt)
}

func TestBrowserLoginIssuesSecureGraphQLCookieWithoutCredentialPayload(t *testing.T) {
	t.Parallel()

	cfg := authconfig.NewDefaultConfig()
	cfg.JWTDuration = time.Hour
	server, code := newAuthTestServerWithConfig(t, cfg)

	const password = "correct-horse-battery-staple-99"

	requireNoGqlErrors(t, query(t, server, "", `mutation { self { register(input: {
		invitationCode: "`+code+`", username: "admin", password: "`+password+`"
	}) { user { id } } } }`))

	loggedIn, headers, _ := queryWithOrigin(
		t, server, "", nil, sameOrigin(t, server),
		`mutation { self { loginBrowser(username: "admin", password: "`+password+`") } }`,
	)
	requireNoGqlErrors(t, loggedIn)
	assert.JSONEq(t, `{"self":{"loginBrowser":null}}`, string(loggedIn.Data))

	cookies := (&http.Response{Header: headers}).Cookies()
	require.Len(t, cookies, 1)
	requireBrowserCookieAttributes(t, cookies[0], "__Secure-bitmagnet")
	assert.NotEmpty(t, cookies[0].Value)
	assert.NotContains(t, string(loggedIn.Data), cookies[0].Value,
		"the browser credential must exist only in the HttpOnly cookie")
	assert.WithinDuration(t, time.Now().Add(time.Hour), cookies[0].Expires, 5*time.Second)
}

func TestBrowserLoginFailureDoesNotIssueCookie(t *testing.T) {
	t.Parallel()

	cfg := authconfig.NewDefaultConfig()
	cfg.BrowserCookieName = "__Secure-custom-browser"
	server, code := newAuthTestServerWithConfig(t, cfg)

	const password = "correct-horse-battery-staple-99"

	requireNoGqlErrors(t, query(t, server, "", `mutation { self { register(input: {
		invitationCode: "`+code+`", username: "admin", password: "`+password+`"
	}) { user { id } } } }`))

	failed, headers, _ := queryWithOrigin(
		t, server, "", nil, sameOrigin(t, server),
		`mutation { self { loginBrowser(username: "admin", password: "wrong-password") } }`,
	)
	requireGraphQLErrorCode(t, failed, "INVALID_CREDENTIALS")
	assert.Empty(t, headers.Values("Set-Cookie"))
}

func TestBrowserLogoutIdempotentlyExpiresConfiguredCookie(t *testing.T) {
	t.Parallel()

	cfg := authconfig.NewDefaultConfig()
	cfg.BrowserCookieName = "__Secure-custom-browser"
	server, _ := newAuthTestServerWithConfig(t, cfg)

	for range 2 {
		loggedOut, headers, _ := queryWithOrigin(
			t, server, "", nil, sameOrigin(t, server), `mutation { self { logoutBrowser } }`,
		)
		requireNoGqlErrors(t, loggedOut)
		assert.JSONEq(t, `{"self":{"logoutBrowser":null}}`, string(loggedOut.Data))

		cookies := (&http.Response{Header: headers}).Cookies()
		require.Len(t, cookies, 1)
		requireBrowserCookieAttributes(t, cookies[0], cfg.BrowserCookieName)
		assert.Empty(t, cookies[0].Value)
		assert.Equal(t, -1, cookies[0].MaxAge)
		assert.True(t, cookies[0].Expires.Before(time.Now()))
	}
}

func TestBrowserCookieResolvesUserIdentity(t *testing.T) {
	t.Parallel()

	server, code := newAuthTestServer(t)

	const password = "correct-horse-battery-staple-99"

	requireNoGqlErrors(t, query(t, server, "", `mutation { self { register(input: {
		invitationCode: "`+code+`", username: "admin", password: "`+password+`"
	}) { user { id } } } }`))

	cookie := loginBrowserCookie(t, server, "admin", password)

	identified, _, _ := queryWithOrigin(t, server, "", cookie, sameOrigin(t, server),
		`{ self { identity { user { username role } } } }`)
	requireNoGqlErrors(t, identified)
	assert.JSONEq(t,
		`{"self":{"identity":{"user":{"username":"admin","role":"admin"}}}}`,
		string(identified.Data),
	)
}

func TestCookieAuthenticatedGraphQLRequiresSameOrigin(t *testing.T) {
	t.Parallel()

	server, code := newAuthTestServer(t)
	_ = loginAsAdmin(t, server, code)
	cookie := loginBrowserCookie(t, server, "admin", "correct-horse-battery-staple-99")

	read, _, status := queryWithOrigin(t, server, "", cookie, "",
		`{ self { identity { user { username } } } }`)
	assert.Equal(t, http.StatusOK, status)
	requireNoGqlErrors(t, read)
	assert.JSONEq(t, `{"self":{"identity":{"user":{"username":"admin"}}}}`, string(read.Data))

	invitationCount := func() int {
		response, _, status := queryWithOrigin(
			t, server, "", cookie, "", `{ auth { listInvitations { totalCount } } }`,
		)
		assert.Equal(t, http.StatusOK, status)
		requireNoGqlErrors(t, response)

		var result invitationCountResponse
		require.NoError(t, json.Unmarshal(response.Data, &result))

		return result.Auth.ListInvitations.TotalCount
	}
	initialInvitationCount := invitationCount()

	assertRejected := func(origin string) {
		response, headers, status := queryWithOrigin(
			t, server, "", cookie, origin,
			`mutation { auth { invite(input: {role: "user"}) { code } } }`,
		)

		assert.Equal(t, http.StatusForbidden, status)
		require.NotEmpty(t, response.Errors)
		assert.Empty(t, headers.Values("Set-Cookie"))
		assert.Equal(
			t, initialInvitationCount, invitationCount(),
			"a rejected request must not create an invitation",
		)
	}
	assertRejected("")
	assertRejected("https://attacker.example")

	invited, _, status := queryWithOrigin(
		t, server, "", cookie, sameOrigin(t, server),
		`mutation { auth { invite(input: {role: "user"}) { code } } }`,
	)
	assert.Equal(t, http.StatusOK, status)
	requireNoGqlErrors(t, invited)
	assert.Equal(t, initialInvitationCount+1, invitationCount())
}

func TestForeignOriginDoesNotRestrictExplicitBearer(t *testing.T) {
	t.Parallel()

	server, code := newAuthTestServer(t)
	adminToken := loginAsAdmin(t, server, code)
	cookie := loginBrowserCookie(t, server, "admin", "correct-horse-battery-staple-99")

	loggedOut, headers, status := queryWithOrigin(
		t, server, adminToken, cookie, "https://attacker.example",
		`mutation { self { logoutBrowser } }`,
	)

	assert.Equal(t, http.StatusOK, status)
	requireNoGqlErrors(t, loggedOut)
	require.Len(t, (&http.Response{Header: headers}).Cookies(), 1)
}

func TestForeignOriginDoesNotRestrictAnonymousGraphQL(t *testing.T) {
	t.Parallel()

	server, code := newAuthTestServer(t)
	_ = loginAsAdmin(t, server, code)

	loggedIn, headers, status := queryWithOrigin(t, server, "", nil, "https://attacker.example",
		`mutation { self { loginBrowser(
			username: "admin", password: "correct-horse-battery-staple-99"
		) } }`)

	assert.Equal(t, http.StatusOK, status)
	requireNoGqlErrors(t, loggedIn)
	require.Len(t, (&http.Response{Header: headers}).Cookies(), 1)
}

func TestGraphQLRejectsMultipartAndWebSocketTransports(t *testing.T) {
	t.Parallel()

	server, _ := newAuthTestServer(t)

	var body bytes.Buffer

	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("operations", `{"query":"{ version }"}`))
	require.NoError(t, writer.WriteField("map", `{}`))
	require.NoError(t, writer.Close())

	multipartRequest, err := http.NewRequestWithContext(
		t.Context(), http.MethodPost, server.URL+"/graphql", &body,
	)
	require.NoError(t, err)
	multipartRequest.Header.Set("Content-Type", writer.FormDataContentType())

	multipartResponse, err := server.Client().Do(multipartRequest)
	require.NoError(t, err)

	defer func() { require.NoError(t, multipartResponse.Body.Close()) }()

	assert.NotEqual(t, http.StatusOK, multipartResponse.StatusCode)

	webSocketRequest, err := http.NewRequestWithContext(
		t.Context(), http.MethodGet, server.URL+"/graphql", nil,
	)
	require.NoError(t, err)
	webSocketRequest.Header.Set("Connection", "Upgrade")
	webSocketRequest.Header.Set("Upgrade", "websocket")
	webSocketRequest.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	webSocketRequest.Header.Set("Sec-WebSocket-Version", "13")

	webSocketResponse, err := server.Client().Do(webSocketRequest)
	require.NoError(t, err)

	defer func() { require.NoError(t, webSocketResponse.Body.Close()) }()

	// The playground is the only GET /graphql handler and it is off by default,
	// so an upgrade reaches no handler at all. The explicit rejection that route
	// performs when `graphql.playground` is enabled is covered by
	// TestPlaygroundRejectsWebSocketUpgradeWhenEnabled in internal/gql/httpserver.
	assert.Equal(t, http.StatusNotFound, webSocketResponse.StatusCode)
}

func browserCookieAndSecondUserBearer(
	t *testing.T,
	server *httptest.Server,
	bootstrapCode string,
) (*http.Cookie, string) {
	t.Helper()

	const password = "correct-horse-battery-staple-99"

	adminToken := loginAsAdmin(t, server, bootstrapCode)

	cookie := loginBrowserCookie(t, server, "admin", password)

	invited := query(t, server, adminToken, `mutation { auth { invite(input: {role: "user"}) { code } } }`)
	requireNoGqlErrors(t, invited)

	var invitation inviteResponse
	require.NoError(t, json.Unmarshal(invited.Data, &invitation))
	require.NotEmpty(t, invitation.Auth.Invite.Code)

	requireNoGqlErrors(t, query(t, server, "", `mutation { self { register(input: {
		invitationCode: "`+invitation.Auth.Invite.Code+`", username: "second", password: "`+password+`"
	}) { user { id } } } }`))

	secondLogin := query(t, server, "", `mutation { self { login(
		username: "second", password: "`+password+`"
	) { token } } }`)
	requireNoGqlErrors(t, secondLogin)

	var login loginResponse
	require.NoError(t, json.Unmarshal(secondLogin.Data, &login))
	require.NotEmpty(t, login.Self.Login.Token)

	return cookie, login.Self.Login.Token
}

func TestExplicitBearerTakesPrecedenceOverBrowserCookie(t *testing.T) {
	t.Parallel()

	server, code := newAuthTestServer(t)
	cookie, secondUserToken := browserCookieAndSecondUserBearer(t, server, code)

	identified, _ := queryWithCredentials(t, server, secondUserToken, cookie,
		`{ self { identity { user { username role } } } }`)
	requireNoGqlErrors(t, identified)
	assert.JSONEq(t,
		`{"self":{"identity":{"user":{"username":"second","role":"user"}}}}`,
		string(identified.Data),
	)
}

func TestInvalidExplicitBearerDoesNotBorrowBrowserCookie(t *testing.T) {
	t.Parallel()

	server, code := newAuthTestServer(t)
	cookie, _ := browserCookieAndSecondUserBearer(t, server, code)

	identified, headers := queryWithCredentials(t, server, "not-a-token", cookie,
		`{ self { identity { user { username } } } }`)
	requireNoGqlErrors(t, identified)
	assert.JSONEq(t, `{"self":{"identity":{"user":null}}}`, string(identified.Data))
	assert.Empty(t, headers.Values("Set-Cookie"), "an ignored ambient cookie must remain untouched")
}

func TestInvalidBrowserCookieFallsBackToAnonymousAndIsExpired(t *testing.T) {
	t.Parallel()

	server, _ := newAuthTestServer(t)
	invalid := &http.Cookie{Name: "__Secure-bitmagnet", Value: "not-a-token"}

	identified, headers := queryWithCredentials(t, server, "", invalid,
		`{ self { identity { user { username } } } }`)
	requireNoGqlErrors(t, identified)
	assert.JSONEq(t, `{"self":{"identity":{"user":null}}}`, string(identified.Data))

	cookies := (&http.Response{Header: headers}).Cookies()
	require.Len(t, cookies, 1)
	requireBrowserCookieAttributes(t, cookies[0], invalid.Name)
	assert.Empty(t, cookies[0].Value)
	assert.Equal(t, -1, cookies[0].MaxAge)
	assert.True(t, cookies[0].Expires.Before(time.Now()))
}

func TestExpiredBrowserCookieFallsBackToAnonymousAndIsExpired(t *testing.T) {
	t.Parallel()

	cfg := authconfig.NewDefaultConfig()
	cfg.JWTDuration = -time.Hour
	server, code := newAuthTestServerWithConfig(t, cfg)

	const password = "correct-horse-battery-staple-99"

	requireNoGqlErrors(t, query(t, server, "", `mutation { self { register(input: {
		invitationCode: "`+code+`", username: "admin", password: "`+password+`"
	}) { user { id } } } }`))

	browserCookie := loginBrowserCookie(t, server, "admin", password)

	identified, headers := queryWithCredentials(t, server, "", browserCookie,
		`{ self { identity { user { username } } } }`)
	requireNoGqlErrors(t, identified)
	assert.JSONEq(t, `{"self":{"identity":{"user":null}}}`, string(identified.Data))

	expired := (&http.Response{Header: headers}).Cookies()
	require.Len(t, expired, 1)
	requireBrowserCookieAttributes(t, expired[0], browserCookie.Name)
	assert.Equal(t, -1, expired[0].MaxAge)
}

func TestRevokedBrowserCookieFallsBackToAnonymousAndIsExpired(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name     string
		revokeOp string
	}{
		{
			name:     "disabled User",
			revokeOp: `mutation { auth { setUserEnabled(userId: 1, enabled: false) { id } } }`,
		},
		{
			name:     "deleted User",
			revokeOp: `mutation { auth { deleteUser(userId: 1) } }`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			server, code := newAuthTestServer(t)
			adminToken := loginAsAdmin(t, server, code)

			const password = "correct-horse-battery-staple-99"

			browserCookie := loginBrowserCookie(t, server, "admin", password)
			requireNoGqlErrors(t, query(t, server, adminToken, testCase.revokeOp))

			identified, headers := queryWithCredentials(t, server, "", browserCookie,
				`{ self { identity { user { username } } } }`)
			requireNoGqlErrors(t, identified)
			assert.JSONEq(t, `{"self":{"identity":{"user":null}}}`, string(identified.Data))

			expired := (&http.Response{Header: headers}).Cookies()
			require.Len(t, expired, 1)
			requireBrowserCookieAttributes(t, expired[0], browserCookie.Name)
			assert.Equal(t, -1, expired[0].MaxAge)
		})
	}
}

type failingAuthenticator struct{ err error }

func (a failingAuthenticator) Authenticate(context.Context, string) (identity.Identity, bool, error) {
	return nil, true, a.err
}

func TestAuthenticationInfrastructureFailureSurvivesGraphQLBoundary(t *testing.T) {
	t.Parallel()

	backendErr := errors.New("authentication backend unavailable")
	server, _ := newAuthTestServerWithConfigAndAuthenticator(
		t,
		authconfig.NewDefaultConfig(),
		failingAuthenticator{err: backendErr},
	)
	browserCookie := &http.Cookie{Name: "__Secure-bitmagnet", Value: "otherwise-valid"}

	response, headers := queryWithCredentials(t, server, "", browserCookie,
		`{ self { identity { user { username } } } }`)
	gqlErr := requireGraphQLErrorCode(t, response, "AUTHENTICATION_INFRASTRUCTURE_FAILURE")
	assert.Equal(t, "authentication service unavailable", gqlErr.Message)
	assert.NotContains(t, gqlErr.Message, backendErr.Error())
	assert.Empty(t, headers.Values("Set-Cookie"), "infrastructure failures must not clear credentials")
}

// Regression guard for the rbac.PutRole revocation defect: an empty object
// action set must clear the role's permissions, through the whole stack.
func TestAuthPutRoleRevokesThroughGraphQL(t *testing.T) {
	t.Parallel()

	server, code := newAuthTestServer(t)

	const password = "correct-horse-battery-staple-99"

	requireNoGqlErrors(t, query(t, server, "", `mutation { self { register(input: {
		invitationCode: "`+code+`", username: "admin", password: "`+password+`"
	}) { user { id } } } }`))

	loggedIn := query(t, server, "", `mutation { self { login(
		username: "admin", password: "`+password+`"
	) { token } } }`)
	requireNoGqlErrors(t, loggedIn)

	var login loginResponse

	require.NoError(t, json.Unmarshal(loggedIn.Data, &login))

	granted := query(t, server, login.Self.Login.Token, `mutation { auth { putRole(
		role: "tester", objectActions: [{namespace: "a", object: "b", action: "c"}]
	) { name permissions { objectAction { namespace } } } } }`)
	requireNoGqlErrors(t, granted)
	assert.JSONEq(t,
		`{"auth":{"putRole":{"name":"tester","permissions":[{"objectAction":{"namespace":"a"}}]}}}`,
		string(granted.Data),
	)

	revoked := query(t, server, login.Self.Login.Token, `mutation { auth { putRole(
		role: "tester", objectActions: []
	) { name permissions { objectAction { namespace } } } } }`)
	requireNoGqlErrors(t, revoked)
	assert.JSONEq(t,
		`{"auth":{"putRole":{"name":"tester","permissions":[]}}}`,
		string(revoked.Data),
	)
}

// With anonymous access disabled, an unauthenticated caller must not reach the
// administrative surface. These are the exact requests that demonstrated the
// hole before the @auth directive was wired in: they returned the user list,
// leaked unclaimed invitation codes — which are registration credentials — and
// allowed a wildcard role to be created, all without any credential.
func TestGraphQLDeniesAnonymousWhenAnonymousAccessIsOff(t *testing.T) {
	t.Parallel()

	cfg := authconfig.NewDefaultConfig()
	cfg.AnonymousAccess = false

	server, _ := newAuthTestServerWithConfig(t, cfg)

	for _, testCase := range []struct {
		name  string
		query string
	}{
		{
			name:  "listUsers",
			query: `{ auth { listUsers { totalCount users { id username role } } } }`,
		},
		{
			name:  "listInvitations",
			query: `{ auth { listInvitations { invitations { code role } } } }`,
		},
		{
			name: "putRole",
			query: `mutation { auth { putRole(role: "attacker", objectActions: [
				{namespace: "**", object: "**", action: "**"}
			]) { name } } }`,
		},
		{
			name:  "torrentContent search",
			query: `{ torrentContent { search(input: {limit: 1}) { totalCount } } }`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			res := query(t, server, "", testCase.query)

			require.NotEmpty(t, res.Errors, "unauthenticated request must be refused")
			assert.Equal(t, "unauthorized", res.Errors[0].Message)
		})
	}
}

// Refusing everything would be a lockout, since logging in is itself a mutation.
// The recovery boundary must survive anonymous access being disabled.
func TestGraphQLAllowsLoginWhenAnonymousAccessIsOff(t *testing.T) {
	t.Parallel()

	cfg := authconfig.NewDefaultConfig()
	cfg.AnonymousAccess = false

	server, code := newAuthTestServerWithConfig(t, cfg)

	const password = "correct-horse-battery-staple-99"

	registered := query(t, server, "", `mutation { self { register(input: {
		invitationCode: "`+code+`", username: "admin", password: "`+password+`"
	}) { user { username } } } }`)
	requireNoGqlErrors(t, registered)

	loggedIn := query(t, server, "", `mutation { self { login(
		username: "admin", password: "`+password+`"
	) { token } } }`)
	requireNoGqlErrors(t, loggedIn)

	var login loginResponse

	require.NoError(t, json.Unmarshal(loggedIn.Data, &login))
	require.NotEmpty(t, login.Self.Login.Token)

	// And the administrator, once authenticated, reaches what anonymous cannot.
	admin := query(t, server, login.Self.Login.Token, `{ auth { listUsers { totalCount } } }`)
	requireNoGqlErrors(t, admin)
}

func TestSelfRecoveryBoundaryIsReachableForEveryIdentity(t *testing.T) {
	t.Parallel()

	cfg := authconfig.NewDefaultConfig()
	cfg.AnonymousAccess = false

	server, code := newAuthTestServerWithConfig(t, cfg)
	adminToken := loginAsAdmin(t, server, code)

	requireNoGqlErrors(t, query(t, server, adminToken, `mutation { auth { putRole(
		role: "custom", objectActions: [{namespace: "graphql", object: "version", action: "query"}]
	) { name } } }`))

	userToken := registerAndLoginAsRole(t, server, adminToken, "user", "ordinary")
	editorToken := registerAndLoginAsRole(t, server, adminToken, "editor", "editor")
	customToken := registerAndLoginAsRole(t, server, adminToken, "custom", "custom")
	customCookie := loginBrowserCookie(t, server, "custom", "correct-horse-battery-staple-99")
	apiKey := apiKeyFrom(t, createAPIKeyAs(
		t, server, adminToken, "recovery", "graphql", "version", "query",
	))

	for _, testCase := range []struct {
		name           string
		credential     string
		expectedUser   string
		expectedRole   string
		expectedAPIKey string
	}{
		{name: "Anonymous"},
		{name: "user", credential: userToken, expectedUser: "ordinary", expectedRole: "user"},
		{name: "editor", credential: editorToken, expectedUser: "editor", expectedRole: "editor"},
		{name: "custom Role", credential: customToken, expectedUser: "custom", expectedRole: "custom"},
		{name: "admin", credential: adminToken, expectedUser: "admin", expectedRole: "admin"},
		{name: "API key", credential: apiKey, expectedUser: "admin", expectedRole: "admin", expectedAPIKey: "recovery"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			identified := query(t, server, testCase.credential, `{ self { identity {
				user { username role }
				apiKey { name }
				permissions { namespace object action }
			} } }`)
			requireNoGqlErrors(t, identified)

			var body identityResponse
			require.NoError(t, json.Unmarshal(identified.Data, &body))

			if testCase.expectedUser == "" {
				assert.Nil(t, body.Self.Identity.User)
			} else {
				require.NotNil(t, body.Self.Identity.User)
				assert.Equal(t, testCase.expectedUser, body.Self.Identity.User.Username)
				assert.Equal(t, testCase.expectedRole, body.Self.Identity.User.Role)
			}

			if testCase.expectedAPIKey == "" {
				assert.Nil(t, body.Self.Identity.APIKey)
			} else {
				require.NotNil(t, body.Self.Identity.APIKey)
				assert.Equal(t, testCase.expectedAPIKey, body.Self.Identity.APIKey.Name)
			}

			for _, permission := range body.Self.Identity.Permissions {
				assert.NotEqual(
					t, "self", permission.Object,
					"self is a recovery boundary, not an object action",
				)
			}
		})
	}

	registered := query(t, server, adminToken,
		`{ auth { listObjectActions { namespace object action } } }`)
	requireNoGqlErrors(t, registered)

	var objectActions objectActionsResponse
	require.NoError(t, json.Unmarshal(registered.Data, &objectActions))
	assert.NotContains(t, objectActions.Auth.ListObjectActions, objectActionBody{
		Namespace: "graphql",
		Object:    "self",
		Action:    "query",
	})
	assert.NotContains(t, objectActions.Auth.ListObjectActions, objectActionBody{
		Namespace: "graphql",
		Object:    "self",
		Action:    "mutate",
	})

	requireNoGqlErrors(t, query(t, server, adminToken,
		`mutation { auth { putRole(role: "custom", objectActions: []) { name } } }`))

	loggedOut, headers, status := queryWithOrigin(
		t, server, "", customCookie, sameOrigin(t, server),
		`mutation { self { logoutBrowser } }`,
	)
	assert.Equal(t, http.StatusOK, status)
	requireNoGqlErrors(t, loggedOut)

	expired := (&http.Response{Header: headers}).Cookies()
	require.Len(t, expired, 1)
	assert.Equal(t, -1, expired[0].MaxAge)
}

// createAPIKeyAs mints a key with the given permissions using the supplied
// credential, and returns the raw key plus any GraphQL errors.
func createAPIKeyAs(
	t *testing.T,
	server *httptest.Server,
	credential, name, namespace, object, action string,
) gqlResponse {
	t.Helper()

	return query(t, server, credential, `mutation { self { createAPIKey(input: {
		name: "`+name+`", permissions: [{namespace: "`+namespace+`", object: "`+object+`", action: "`+action+`"}]
	}) { apiKey } } }`)
}

func apiKeyFrom(t *testing.T, res gqlResponse) string {
	t.Helper()
	requireNoGqlErrors(t, res)

	var body createAPIKeyResponse

	require.NoError(t, json.Unmarshal(res.Data, &body))
	require.NotEmpty(t, body.Self.CreateAPIKey.APIKey)

	return body.Self.CreateAPIKey.APIKey
}

func loginAsAdmin(t *testing.T, server *httptest.Server, code string) string {
	t.Helper()

	const password = "correct-horse-battery-staple-99"

	requireNoGqlErrors(t, query(t, server, "", `mutation { self { register(input: {
		invitationCode: "`+code+`", username: "admin", password: "`+password+`"
	}) { user { id } } } }`))

	loggedIn := query(t, server, "", `mutation { self { login(
		username: "admin", password: "`+password+`"
	) { token } } }`)
	requireNoGqlErrors(t, loggedIn)

	var login loginResponse

	require.NoError(t, json.Unmarshal(loggedIn.Data, &login))
	require.NotEmpty(t, login.Self.Login.Token)

	return login.Self.Login.Token
}

func registerAndLoginAsRole(
	t *testing.T,
	server *httptest.Server,
	adminToken, role, username string,
) string {
	t.Helper()

	invited := query(t, server, adminToken,
		`mutation { auth { invite(input: {role: "`+role+`"}) { code } } }`)
	requireNoGqlErrors(t, invited)

	var invitation inviteResponse
	require.NoError(t, json.Unmarshal(invited.Data, &invitation))
	require.NotEmpty(t, invitation.Auth.Invite.Code)

	const password = "correct-horse-battery-staple-99"
	requireNoGqlErrors(t, query(t, server, "", `mutation { self { register(input: {
		invitationCode: "`+invitation.Auth.Invite.Code+`",
		username: "`+username+`", password: "`+password+`"
	}) { user { id } } } }`))

	loggedIn := query(t, server, "", `mutation { self { login(
		username: "`+username+`", password: "`+password+`"
	) { token } } }`)
	requireNoGqlErrors(t, loggedIn)

	var login loginResponse
	require.NoError(t, json.Unmarshal(loggedIn.Data, &login))
	require.NotEmpty(t, login.Self.Login.Token)

	return login.Self.Login.Token
}

// A narrowly scoped API key must not be able to widen itself.
//
// The chain this guards against: an API-key identity reports its owning user,
// and the self recovery boundary is deliberately outside Role-Permission
// gating. So without a field-level check, a key scoped to one object action
// could call createAPIKey and mint a second key naming any registered object
// action — bounded only by the owner's role, which for an administrator is
// everything.
func TestAPIKeyCannotManageAPIKeys(t *testing.T) {
	t.Parallel()

	cfg := authconfig.NewDefaultConfig()
	cfg.AnonymousAccess = false

	server, code := newAuthTestServerWithConfig(t, cfg)
	token := loginAsAdmin(t, server, code)

	narrow := apiKeyFrom(t, createAPIKeyAs(t, server, token, "narrow", "torznab", "torznab", "query"))

	// The key cannot reach the administrative surface it was not granted.
	denied := query(t, server, narrow, `{ auth { listUsers { totalCount } } }`)
	require.NotEmpty(t, denied.Errors)
	assert.Equal(t, "unauthorized", denied.Errors[0].Message)

	for _, testCase := range []struct {
		name  string
		query string
	}{
		{
			name: "create",
			query: `mutation { self { createAPIKey(input: {
				name: "escalated", permissions: [{namespace: "graphql", object: "auth", action: "query"}]
			}) { apiKey } } }`,
		},
		{name: "delete", query: `mutation { self { deleteAPIKey(id: 1) } }`},
		{name: "list", query: `{ self { apiKeys { id name createdAt expiresAt } } }`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			res := query(t, server, narrow, testCase.query)
			gqlErr := requireGraphQLErrorCode(t, res, "API_KEY_MANAGEMENT_FORBIDDEN")
			assert.Equal(t, "api keys may not manage api keys", gqlErr.Message)
		})
	}

	for _, q := range []string{
		`mutation { self { createAPIKey(input: {
			name: "anonymous", permissions: [{namespace: "graphql", object: "auth", action: "query"}]
		}) { apiKey } } }`,
		`mutation { self { deleteAPIKey(id: 1) } }`,
		`{ self { apiKeys { id name } } }`,
	} {
		res := query(t, server, "", q)
		gqlErr := requireGraphQLErrorCode(t, res, "USER_SESSION_REQUIRED")
		assert.Equal(t, "not authenticated", gqlErr.Message)
	}

	// The legitimate path must keep working, or the guard above is just a denial.
	key := apiKeyFrom(t, createAPIKeyAs(t, server, token, "legit", "graphql", "auth", "query"))

	granted := query(t, server, key, `{ auth { listUsers { totalCount } } }`)
	requireNoGqlErrors(t, granted)
}

// The open default must remain usable without becoming a trapdoor. Anonymous
// callers keep the baseline but never auth administration: role grants persist
// in the database, so letting anon write a wildcard would survive switching
// anonymous access off.
func TestOpenAnonymousBaselineExcludesAuthAdministration(t *testing.T) {
	t.Parallel()

	server, _ := newAuthTestServer(t)
	requireNoGqlErrors(t, query(t, server, "", `{ version }`))
	requireNoGqlErrors(t, query(t, server, "", `{ self { identity { user { username } } } }`))

	for _, testCase := range []struct {
		name  string
		query string
	}{
		{
			name: "putRole on anon",
			query: `mutation { auth { putRole(role: "anon", objectActions: [
				{namespace: "**", object: "**", action: "**"}
			]) { name } } }`,
		},
		{name: "listUsers", query: `{ auth { listUsers { totalCount } } }`},
		{name: "listInvitations", query: `{ auth { listInvitations { invitations { code } } } }`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			res := query(t, server, "", testCase.query)
			require.NotEmpty(t, res.Errors, "anonymous callers must not administer auth")
			assert.Equal(t, "unauthorized", res.Errors[0].Message)
		})
	}
}
