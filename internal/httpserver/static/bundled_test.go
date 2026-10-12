//go:build webui

package static_test

import (
	"net/http"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultBuildServesBundledUI(t *testing.T) {
	t.Parallel()

	engine, err := engineWith(t, httpserver.NewDefaultConfig().Static)
	require.NoError(t, err)

	root := get(t, engine, "/")
	assert.Equal(t, http.StatusTemporaryRedirect, root.Code)
	assert.Equal(t, "/ui/", root.Header().Get("Location"))

	for _, path := range []string{"/ui/", "/ui/torrent/deadbeef", "/ui/admin/users"} {
		page := get(t, engine, path)
		assert.Equal(t, http.StatusOK, page.Code, path)
		assert.Contains(t, page.Body.String(), "Elm.Main.init", path)
		assert.Contains(t, page.Body.String(), `<base href="/ui/"`, path)
	}

	bundle := get(t, engine, "/ui/main.js")
	assert.Equal(t, http.StatusOK, bundle.Code)
	assert.Contains(t, bundle.Body.String(), "Elm")
	assert.Equal(t, "no-store", bundle.Header().Get("Cache-Control"))
	assert.Equal(t, http.StatusOK, get(t, engine, "/ui/styles.css").Code)
	assert.Equal(t, http.StatusOK, get(t, engine, "/ui/favicon.svg").Code)
	config := get(t, engine, "/ui/config.js")
	assert.Equal(t, http.StatusOK, config.Code)
	assert.Contains(t, config.Body.String(), `window.MAGNES_BASE_PATH = "/ui"`)
	assert.Contains(t, config.Body.String(), `window.MAGNES_API_URL = "/graphql"`)
	assert.Equal(t, http.StatusNotFound, get(t, engine, "/graphql").Code)
}

func TestBundledUIUsesConfiguredMount(t *testing.T) {
	t.Parallel()

	cfg := httpserver.NewDefaultConfig().Static
	cfg.Path = "/magnes/"
	engine, err := engineWith(t, cfg)
	require.NoError(t, err)

	page := get(t, engine, "/magnes/login")
	assert.Equal(t, http.StatusOK, page.Code)
	assert.Contains(t, page.Body.String(), `<base href="/magnes/"`)
	assert.Contains(t, get(t, engine, "/magnes/config.js").Body.String(),
		`window.MAGNES_BASE_PATH = "/magnes"`)
	assert.Equal(t, "/magnes/", get(t, engine, "/").Header().Get("Location"))
	assert.Equal(t, http.StatusNotFound, get(t, engine, "/ui/").Code)
}

func TestConfiguredDirectoryOverridesBundledUI(t *testing.T) {
	t.Parallel()

	cfg := httpserver.NewDefaultConfig().Static
	cfg.Dir = uiDir(t)
	engine, err := engineWith(t, cfg)
	require.NoError(t, err)

	assert.Equal(t, "<title>magnes</title>", get(t, engine, "/ui/").Body.String())
	assert.Equal(t, "console.log('hi')", get(t, engine, "/ui/main.js").Body.String())
	assert.Equal(t, http.StatusNotFound, get(t, engine, "/").Code)
}

func TestBundledUICanBeDisabled(t *testing.T) {
	t.Parallel()

	cfg := httpserver.NewDefaultConfig().Static
	cfg.Bundled = false
	engine, err := engineWith(t, cfg)
	require.NoError(t, err)

	assert.Equal(t, http.StatusNotFound, get(t, engine, "/ui/").Code)
	assert.Equal(t, http.StatusNotFound, get(t, engine, "/").Code)
}
