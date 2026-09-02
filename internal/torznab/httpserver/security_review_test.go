package httpserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/browser_session"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/http_auth"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/identity"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/torznab"
	"github.com/bitmagnet-io/bitmagnet/internal/torznab/httpserver"
	torznab_mocks "github.com/bitmagnet-io/bitmagnet/internal/torznab/mocks"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const browserJWT = "browser-jwt"

type browserSessionIdentity struct{}

func (browserSessionIdentity) Self() identity.Self {
	return identity.Self{User: &model.User{ID: 1, Username: "admin", RoleName: "admin"}}
}

func (browserSessionIdentity) EffectivePermissions(context.Context) ([]rbac.ObjectAction, error) {
	return []rbac.ObjectAction{httpserver.ObjectAction}, nil
}

func (browserSessionIdentity) Enforce(_ context.Context, objectAction rbac.ObjectAction) (bool, error) {
	return objectAction == httpserver.ObjectAction, nil
}

type browserSessionAuthenticator struct{}

func (browserSessionAuthenticator) Authenticate(
	_ context.Context,
	token string,
) (identity.Identity, bool, error) {
	if token == browserJWT {
		return browserSessionIdentity{}, true, nil
	}

	return stubIdentity{}, true, nil
}

// Torznab's machine credential contract is query-string apikey or X-Api-Key.
// A browser session resolved by the global middleware must not silently become
// a third credential type for the endpoint.
func TestTorznabRejectsBrowserBearerTokenWithoutAPIKey(t *testing.T) {
	t.Parallel()

	authenticator := browserSessionAuthenticator{}
	clientMock := torznab_mocks.NewClient(t)
	lazyClient := lazy.New[torznab.Client](func() (torznab.Client, error) {
		return clientMock, nil
	})

	engine := gin.New()
	authOption := http_auth.New(http_auth.Params{Middleware: http_auth.NewMiddleware(
		authenticator,
		browser_session.NewCookie(authconfig.NewDefaultConfig()),
	)}).Option
	require.NoError(t, authOption.Apply(engine))
	require.NoError(t, httpserver.New(lazyClient, testCfg, authenticator).Apply(engine))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/torznab/?t=caps", nil)
	req.Header.Set(http_auth.AuthorizationHeader, http_auth.BearerPrefix+browserJWT)

	response := httptest.NewRecorder()
	engine.ServeHTTP(response, req)

	assert.Equal(t, http.StatusUnauthorized, response.Code)
	assert.Equal(t, unauthorizedXML(t), response.Body.String())
}

func TestTorznabIgnoresBrowserCookie(t *testing.T) {
	t.Parallel()

	authenticator := browserSessionAuthenticator{}
	clientMock := torznab_mocks.NewClient(t)
	lazyClient := lazy.New[torznab.Client](func() (torznab.Client, error) {
		return clientMock, nil
	})

	engine := gin.New()
	authOption := http_auth.New(http_auth.Params{Middleware: http_auth.NewMiddleware(
		authenticator,
		browser_session.NewCookie(authconfig.NewDefaultConfig()),
	)}).Option
	require.NoError(t, authOption.Apply(engine))
	require.NoError(t, httpserver.New(lazyClient, testCfg, authenticator).Apply(engine))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/torznab/?t=caps", nil)
	req.AddCookie(&http.Cookie{Name: "__Secure-bitmagnet", Value: "rejected-browser-cookie"})

	response := httptest.NewRecorder()
	engine.ServeHTTP(response, req)

	assert.Equal(t, http.StatusUnauthorized, response.Code)
	assert.Equal(t, unauthorizedXML(t), response.Body.String())
	assert.Empty(t, response.Header().Values("Set-Cookie"), "Torznab must not process browser cookies")
}
