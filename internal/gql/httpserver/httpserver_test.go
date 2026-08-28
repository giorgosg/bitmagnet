package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/browser_session"
	"github.com/bitmagnet-io/bitmagnet/internal/gql"
	gqlauth "github.com/bitmagnet-io/bitmagnet/internal/gql/auth"
	gqlhttpserver "github.com/bitmagnet-io/bitmagnet/internal/gql/httpserver"
	"github.com/bitmagnet-io/bitmagnet/internal/gql/resolvers"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// newTestEngine applies the production http server option, so these tests cover
// the routes it registers rather than a hand-mounted approximation.
func newTestEngine(t *testing.T, cfg gqlhttpserver.Config) *gin.Engine {
	t.Helper()

	gin.SetMode(gin.TestMode)

	schema := gql.NewExecutableSchema(gql.Config{
		Resolvers:  &resolvers.Resolver{},
		Directives: gql.DirectiveRoot{Auth: gqlauth.NewDirective()},
	})

	engine := gin.New()
	option := gqlhttpserver.New(gqlhttpserver.Params{
		Schema: lazy.New(func() (graphql.ExecutableSchema, error) {
			return schema, nil
		}),
		Logger:        zap.NewNop().Sugar(),
		BrowserCookie: browser_session.NewCookie(authconfig.NewDefaultConfig()),
		Config:        cfg,
	}).Option

	require.NoError(t, option.Apply(engine))

	return engine
}

func introspect(t *testing.T, engine *gin.Engine) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/graphql",
		strings.NewReader(`{"query":"{ __schema { queryType { name } } }"}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	return rec
}

func TestIntrospectionIsOffByDefault(t *testing.T) {
	t.Parallel()

	body := introspect(t, newTestEngine(t, gqlhttpserver.NewDefaultConfig())).Body.String()

	assert.NotContains(t, body, `"queryType"`,
		"the full schema is readable by anyone who can reach the POST endpoint")
}

func TestIntrospectionServesWhenEnabled(t *testing.T) {
	t.Parallel()

	body := introspect(t, newTestEngine(t, gqlhttpserver.Config{Introspection: true})).Body.String()

	assert.Contains(t, body, `"queryType"`,
		"an operator who turns introspection on must get it")
}

func TestPlaygroundIsOffByDefault(t *testing.T) {
	t.Parallel()

	engine := newTestEngine(t, gqlhttpserver.NewDefaultConfig())
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/graphql", nil))

	assert.Equal(t, http.StatusNotFound, rec.Code,
		"the playground route is registered with no auth guard")
}

func TestPlaygroundServesWhenEnabled(t *testing.T) {
	t.Parallel()

	engine := newTestEngine(t, gqlhttpserver.Config{Playground: true})
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/graphql", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "GraphQL playground")
}

// The upgrade rejection is part of the playground route, so it only has meaning
// when that route exists.
func TestPlaygroundRejectsWebSocketUpgradeWhenEnabled(t *testing.T) {
	t.Parallel()

	engine := newTestEngine(t, gqlhttpserver.Config{Playground: true})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/graphql", nil)
	req.Header.Set("Upgrade", "websocket")

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
