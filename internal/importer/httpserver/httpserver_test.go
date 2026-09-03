package httpserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/importer"
	"github.com/bitmagnet-io/bitmagnet/internal/importer/httpserver"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// allowGuard authorizes everything, so these tests exercise the handler rather
// than the permission model. Issue 01 is what makes that the realistic case:
// anonymous callers hold ObjectActionImport by default.
type allowGuard struct{}

func (allowGuard) Allow(*gin.Context, rbac.ObjectAction) bool { return true }

// recordingImporter records whether the handler started an import at all. The
// handler opens one before reading a byte of the body, so "no import started" is
// the observable form of "the request was rejected before its body was read".
type recordingImporter struct {
	started bool
	items   []importer.Item
}

func (i *recordingImporter) New(context.Context, importer.Info) importer.ActiveImport {
	i.started = true

	return i
}

func (i *recordingImporter) Import(items ...importer.Item) error {
	i.items = append(i.items, items...)

	return nil
}

func (*recordingImporter) Drain() {}

func (*recordingImporter) Closed() bool { return false }

func (*recordingImporter) Close() error { return nil }

func (*recordingImporter) Err() error { return nil }

func newTestEngine(t *testing.T) (*gin.Engine, *recordingImporter) {
	t.Helper()

	imp := &recordingImporter{}
	result := httpserver.New(httpserver.Params{
		Importer: lazy.New[importer.Importer](func() (importer.Importer, error) {
			return imp, nil
		}),
		Guard:  allowGuard{},
		Logger: zap.NewNop().Sugar(),
	})

	engine := gin.New()
	require.NoError(t, result.Option.Apply(engine))

	return engine, imp
}

const testImportBody = `{"source":"test","infoHash":"0000000000000000000000000000000000000000","name":"x","size":1}`

// The import endpoint writes the database. Without a Content-Type check a
// cross-origin POST of text/plain is a CORS *simple request*: the browser sends
// it with no preflight, so no CORS response header is needed for the write to
// land. Rejecting anything but JSON is what makes the request non-simple, and so
// preflighted, regardless of what the anonymous baseline grants.
func TestImportRejectsNonJSONContentType(t *testing.T) {
	t.Parallel()

	for _, contentType := range []string{
		"text/plain",
		"text/plain;charset=UTF-8",
		"application/x-www-form-urlencoded",
		"multipart/form-data; boundary=x",
		"",
	} {
		t.Run("content-type "+contentType, func(t *testing.T) {
			t.Parallel()

			engine, imp := newTestEngine(t)

			req := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				"/import",
				strings.NewReader(testImportBody),
			)
			if contentType != "" {
				req.Header.Set("Content-Type", contentType)
			}

			rec := httptest.NewRecorder()
			engine.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusUnsupportedMediaType, rec.Code)
			assert.False(t, imp.started, "the body must not be read before the media type is checked")
			assert.Empty(t, imp.items)
		})
	}
}

func TestImportAcceptsJSONContentType(t *testing.T) {
	t.Parallel()

	for _, contentType := range []string{
		"application/json",
		"application/json; charset=utf-8",
		"application/JSON",
	} {
		t.Run("content-type "+contentType, func(t *testing.T) {
			t.Parallel()

			engine, imp := newTestEngine(t)

			req := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				"/import",
				strings.NewReader(testImportBody),
			)
			req.Header.Set("Content-Type", contentType)

			rec := httptest.NewRecorder()
			engine.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Len(t, imp.items, 1)
		})
	}
}
