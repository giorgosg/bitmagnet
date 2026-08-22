package httpserver_test

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"syscall"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/httpserver"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// The Torznab credential travels in the query string and does not expire, so
// read access to a log file that records it is equivalent to application access.
// The request logger redacts it; the panic path is a second way out of the
// process, and it has to redact it too.
//
// Both log sinks are captured, because the defect this covers was invisible to
// either one alone: gin.Recovery — what the server used to install — prints the
// request line verbatim, in release mode as well, and writes it to
// gin.DefaultErrorWriter rather than through the application's logger.
//
// Not parallel, here or in the subtests: capturing what gin.Recovery would write
// means swapping gin.DefaultErrorWriter, which is package-level state.
//
//nolint:paralleltest // swaps gin.DefaultErrorWriter; see above
func TestMiddlewareRecoveryRedactsCredentials(t *testing.T) {
	const (
		queryKey  = "query-string-torznab-key"
		headerKey = "header-torznab-key"
		bearer    = "bearer-session-token"
	)

	originalErrorWriter := gin.DefaultErrorWriter

	t.Cleanup(func() { gin.DefaultErrorWriter = originalErrorWriter })

	brokenPipe := &net.OpError{
		Op: "write",
		Err: &os.SyscallError{
			Syscall: "write",
			Err:     syscall.EPIPE,
		},
	}

	for _, tt := range []struct {
		name       string
		panicValue any
	}{
		// The two branches log the request separately, and only one of them is
		// reachable in release mode, so both are exercised.
		{"broken pipe", brokenPipe},
		{"ordinary panic", "boom"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer

			gin.DefaultErrorWriter = io.Writer(&stderr)

			core, logs := observer.New(zap.DebugLevel)

			engine, err := httpserver.NewEngine(httpserver.NewDefaultConfig())
			require.NoError(t, err)

			engine.Use(httpserver.Middleware(zap.New(core))...)
			engine.GET("/torznab/", func(*gin.Context) {
				panic(tt.panicValue)
			})

			req := httptest.NewRequestWithContext(t.Context(),
				http.MethodGet,
				"/torznab/?t=caps&apikey="+queryKey,
				nil,
			)
			req.Header.Set("X-Api-Key", headerKey)
			req.Header.Set("Authorization", "Bearer "+bearer)

			engine.ServeHTTP(httptest.NewRecorder(), req)

			require.NotEmpty(t, logs.All(), "the panic should have been logged at all")

			logged := fmt.Sprint(logs.All()) + stderr.String()
			for _, secret := range []string{queryKey, headerKey, bearer} {
				require.NotContains(t, logged, secret,
					"recovery logging must not record credentials")
			}
		})
	}
}
