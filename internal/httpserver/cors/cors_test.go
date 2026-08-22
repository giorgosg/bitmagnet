package cors_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/httpserver"
	"github.com/bitmagnet-io/bitmagnet/internal/httpserver/cors"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// preflightAllows reports the Access-Control-Allow-Headers value returned for a
// preflight asking for the given header, on an engine built from the default config.
//
// The requested name is lowercased, because that is what a browser sends: the
// Fetch standard guarantees CORS-unsafe request-header names in
// Access-Control-Request-Headers are lowercase, and rs/cors matches accordingly.
func preflightAllows(t *testing.T, requested string) string {
	t.Helper()

	gin.SetMode(gin.TestMode)

	engine, err := httpserver.NewEngine(httpserver.NewDefaultConfig())
	require.NoError(t, err)

	result := cors.New(cors.Params{
		Config: httpserver.NewDefaultConfig(),
		Logger: zap.NewNop().Sugar(),
	})
	require.NoError(t, result.Option.Apply(engine))

	engine.POST("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://attacker.example")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", strings.ToLower(requested))

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	return rec.Header().Get("Access-Control-Allow-Headers")
}

func TestDefaultConfig_DoesNotAllowArbitraryHeaders(t *testing.T) {
	t.Parallel()

	assert.Empty(t, preflightAllows(t, "X-Something-Invented"),
		"the default CORS config reflects any requested header back")
}

func TestDefaultConfig_AllowsTheHeadersTheServerReads(t *testing.T) {
	t.Parallel()

	for _, header := range []string{"Content-Type", "Authorization", "X-Api-Key", "X-Import-Id"} {
		t.Run(header, func(t *testing.T) {
			t.Parallel()

			assert.NotEmpty(t, preflightAllows(t, header),
				"%s is read by the server and must survive a preflight", header)
		})
	}
}

func TestDefaultConfig_CorsDebugIsOff(t *testing.T) {
	t.Parallel()

	assert.False(t, httpserver.NewDefaultConfig().Cors.Debug,
		"CORS debug logging should not be on by default")
}
