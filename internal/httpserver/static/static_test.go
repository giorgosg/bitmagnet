package static_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/httpserver"
	"github.com/bitmagnet-io/bitmagnet/internal/httpserver/static"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// uiDir writes a minimal single-page app to a temporary directory.
func uiDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"),
		[]byte("<title>magnes</title>"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.js"),
		[]byte("console.log('hi')"), 0o600))

	return dir
}

func engineWith(t *testing.T, cfg httpserver.StaticConfig) (*gin.Engine, error) {
	t.Helper()

	gin.SetMode(gin.TestMode)

	serverCfg := httpserver.NewDefaultConfig()
	serverCfg.Static = cfg

	engine, err := httpserver.NewEngine(serverCfg)
	require.NoError(t, err)

	return engine, static.New(static.Params{
		Config: serverCfg,
		Logger: zap.NewNop().Sugar(),
	}).Option.Apply(engine)
}

func get(t *testing.T, engine *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))

	return rec
}

func TestServesFilesFromTheConfiguredDirectory(t *testing.T) {
	t.Parallel()

	engine, err := engineWith(t, httpserver.StaticConfig{Dir: uiDir(t), Path: "/ui"})
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, get(t, engine, "/ui/main.js").Code)
	assert.Contains(t, get(t, engine, "/ui/main.js").Body.String(), "console.log")

	// net/http's file server redirects an explicit index.html to the directory,
	// so the index is fetched through the directory path.
	index := get(t, engine, "/ui/")
	assert.Equal(t, http.StatusOK, index.Code)
	assert.Contains(t, index.Body.String(), "magnes")
}

// A single-page app owns its own routing: a deep link must return index.html
// rather than 404, the same way the bundled web UI's mount does.
func TestUnknownPathFallsBackToIndex(t *testing.T) {
	t.Parallel()

	engine, err := engineWith(t, httpserver.StaticConfig{Dir: uiDir(t), Path: "/ui"})
	require.NoError(t, err)

	res := get(t, engine, "/ui/torrent/deadbeef")

	assert.Equal(t, http.StatusOK, res.Code)
	assert.Contains(t, res.Body.String(), "magnes")
}

func TestStaticMountDisablesBrowserCaching(t *testing.T) {
	t.Parallel()

	engine, err := engineWith(t, httpserver.StaticConfig{Dir: uiDir(t), Path: "/ui"})
	require.NoError(t, err)

	for name, path := range map[string]string{
		"asset":     "/ui/main.js",
		"spa route": "/ui/torrent/deadbeef",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, "no-store", get(t, engine, path).Header().Get("Cache-Control"))
		})
	}
}

func TestDisabledWhenNoDirectoryIsConfigured(t *testing.T) {
	t.Parallel()

	engine, err := engineWith(t, httpserver.StaticConfig{})
	require.NoError(t, err, "an unset dir is the default and must not be an error")

	assert.Equal(t, http.StatusNotFound, get(t, engine, "/ui/main.js").Code)
}

func TestRejectsADirectoryThatIsNotThere(t *testing.T) {
	t.Parallel()

	_, err := engineWith(t, httpserver.StaticConfig{
		Dir:  filepath.Join(t.TempDir(), "nope"),
		Path: "/ui",
	})

	require.Error(t, err, "a configured directory that does not exist is a startup error")
	assert.Contains(t, err.Error(), "static")
}

func TestRejectsAPathThatWouldCollideOrTakeOverTheRoot(t *testing.T) {
	t.Parallel()

	for name, path := range map[string]string{
		"root":           "/",
		"bundled web ui": "/webui",
		"missing slash":  "ui",
		"empty with dir": "",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := engineWith(t, httpserver.StaticConfig{Dir: uiDir(t), Path: path})

			require.Error(t, err)
		})
	}
}
