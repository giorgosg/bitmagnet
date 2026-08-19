package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/httpserver"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clientIPFor reports what ClientIP() resolves to for a request arriving from
// peer with the given X-Forwarded-For, on an engine built from cfg.
func clientIPFor(t *testing.T, cfg httpserver.Config, peer, forwardedFor string) string {
	t.Helper()

	engine, err := httpserver.NewEngine(cfg)
	require.NoError(t, err)

	var got string

	engine.GET("/", func(c *gin.Context) { got = c.ClientIP() })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = peer + ":44321"
	req.Header.Set("X-Forwarded-For", forwardedFor)

	engine.ServeHTTP(httptest.NewRecorder(), req)

	return got
}

// Anything keyed by the client address — the login throttle above all — is only
// a control if the caller cannot choose the key. Gin's own default is to trust
// every proxy, so the shipped default has to override it.
func TestDefaultConfigIgnoresForwardedForHeader(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "203.0.113.9",
		clientIPFor(t, httpserver.NewDefaultConfig(), "203.0.113.9", "198.51.100.7"),
		"the default must attribute a request to the peer that opened it")
}

// The other direction: an operator behind a reverse proxy names it, and the real
// client address survives the hop. Without this the fix above would just be
// breaking deployments behind a proxy.
func TestConfiguredTrustedProxyIsBelieved(t *testing.T) {
	t.Parallel()

	cfg := httpserver.NewDefaultConfig()
	cfg.TrustedProxies = []string{"203.0.113.0/24"}

	assert.Equal(t, "198.51.100.7",
		clientIPFor(t, cfg, "203.0.113.9", "198.51.100.7"),
		"a named proxy's forwarded address must be believed")
}

// A peer outside the trusted range is not a proxy, whatever it claims.
func TestUntrustedPeerForwardedForIsIgnored(t *testing.T) {
	t.Parallel()

	cfg := httpserver.NewDefaultConfig()
	cfg.TrustedProxies = []string{"203.0.113.0/24"}

	assert.Equal(t, "192.0.2.44",
		clientIPFor(t, cfg, "192.0.2.44", "198.51.100.7"))
}
