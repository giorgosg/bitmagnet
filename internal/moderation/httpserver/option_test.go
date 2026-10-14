package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation/httpserver"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestExportFailsClosedWithoutAuthenticator(t *testing.T) {
	t.Parallel()

	opened := false
	service := lazy.New(
		func() (*moderation.Service, error) { opened = true; return moderation.NewService(nil), nil },
	)
	engine := gin.New()
	require.NoError(t, httpserver.New(service, nil).Apply(engine))

	response := httptest.NewRecorder()
	engine.ServeHTTP(
		response,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/graphql/moderation/export", nil),
	)
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.False(t, opened)
	require.Empty(t, response.Body.String())
}
