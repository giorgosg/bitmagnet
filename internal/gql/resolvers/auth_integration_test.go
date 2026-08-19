package resolvers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/api_key"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/http_auth"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/identity"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/jwt"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/user"
	"github.com/bitmagnet-io/bitmagnet/internal/database"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/gql"
	"github.com/bitmagnet-io/bitmagnet/internal/gql/resolvers"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

type daoProvider struct{ query *dao.Query }

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

	db := dbtest.New(t)

	var provider database.DaoTransactionProvider = daoProvider{query: db.Query}

	cfg := authconfig.NewDefaultConfig()
	values := cfg.UserValues()

	jwtService := jwt.NewService(jwt.Secret("test-secret"), jwt.Duration(time.Hour))
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
	apiKeyService := api_key.NewService(api_key.NewRepository(provider))

	objectActions := func() []rbac.ObjectAction {
		return []rbac.ObjectAction{rbac.NewObjectAction("torrent", "torrent", "query")}
	}
	rbacService := rbac.NewService(
		rbac.NewRepository(provider),
		objectActions,
		rbac.PermissionProviders(
			rbac.CorePermissions,
			rbac.VerbatimPermissions(objectActions),
			authconfig.AnonymousPermissions(cfg, objectActions),
		),
		rbac.CacheTTL(time.Minute),
	)

	authenticator := identity.NewAuthenticator(jwtService, userService, apiKeyService, rbacService)

	schema := gql.NewExecutableSchema(gql.Config{Resolvers: &resolvers.Resolver{
		UserService:   userService,
		APIKeyService: apiKeyService,
		RBACService:   rbacService,
	}})

	gin.SetMode(gin.TestMode)

	engine := gin.New()

	// Apply the production http server option rather than mounting the
	// middlewares by hand, so this also covers what that option installs.
	authOption := http_auth.New(http_auth.Params{
		Middleware: http_auth.NewMiddleware(authenticator),
	}).Option
	require.Equal(t, "auth", authOption.Key())
	require.NoError(t, authOption.Apply(engine))

	srv := handler.New(schema)
	srv.AddTransport(transport.POST{})
	srv.SetQueryCache(lru.New[*ast.QueryDocument](10))
	engine.POST("/graphql", func(c *gin.Context) {
		srv.ServeHTTP(c.Writer, c.Request)
	})

	// The bootstrap invitation is what makes the first registration possible.
	invitation, err := userService.CreateInitialInvitation(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, invitation.Code)

	server := httptest.NewServer(engine)
	t.Cleanup(server.Close)

	return server, invitation.Code
}

type gqlError struct {
	Message string `json:"message"`
}

type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []gqlError      `json:"errors"`
}

// Named response shapes, rather than anonymous nested structs, to decode the
// parts of the payload each test asserts on.
type objectActionBody struct {
	Namespace string `json:"namespace"`
}

type permissionBody struct {
	ObjectAction objectActionBody `json:"objectAction"`
}

type loginBody struct {
	Token       string           `json:"token"`
	Permissions []permissionBody `json:"permissions"`
}

type selfLoginBody struct {
	Login loginBody `json:"login"`
}

type loginResponse struct {
	Self selfLoginBody `json:"self"`
}

type userBody struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

type identityBody struct {
	User *userBody `json:"user"`
}

type selfIdentityBody struct {
	Identity identityBody `json:"identity"`
}

type identityResponse struct {
	Self selfIdentityBody `json:"self"`
}

func query(t *testing.T, server *httptest.Server, token, q string) gqlResponse {
	t.Helper()

	body, err := json.Marshal(map[string]string{"query": q})
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		server.URL+"/graphql",
		bytes.NewReader(body),
	)
	require.NoError(t, err)

	req.Header.Set("Content-Type", "application/json")

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	res, err := server.Client().Do(req)
	require.NoError(t, err)

	defer func() { require.NoError(t, res.Body.Close()) }()

	var decoded gqlResponse

	require.NoError(t, json.NewDecoder(res.Body).Decode(&decoded))

	return decoded
}

func requireNoGqlErrors(t *testing.T, res gqlResponse) {
	t.Helper()

	for _, e := range res.Errors {
		t.Errorf("graphql error: %s", e.Message)
	}

	require.Empty(t, res.Errors)
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

	loggedIn := query(t, server, "", `mutation { self { login(
		username: "admin", password: "`+password+`"
	) { token user { username role } permissions { objectAction { namespace object action } } } } }`)
	requireNoGqlErrors(t, loggedIn)

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

// Anonymous access is on by default, so an unauthenticated request must resolve
// to the anonymous identity rather than failing.
func TestAuthAnonymousIdentityIsNotAnError(t *testing.T) {
	t.Parallel()

	server, _ := newAuthTestServer(t)

	res := query(t, server, "", `{ self { identity { user { username } } } }`)
	requireNoGqlErrors(t, res)
	assert.JSONEq(t, `{"self":{"identity":{"user":null}}}`, string(res.Data))

	// A token that is not a valid JWT must be ignored, not rejected.
	bogus := query(t, server, "not-a-real-token", `{ self { identity { user { username } } } }`)
	requireNoGqlErrors(t, bogus)
	assert.JSONEq(t, `{"self":{"identity":{"user":null}}}`, string(bogus.Data))
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
