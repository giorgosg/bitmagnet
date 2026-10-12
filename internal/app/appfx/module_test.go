//go:build !webui

package appfx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/app/appfx"
	"github.com/bitmagnet-io/bitmagnet/internal/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
)

func TestBackendHasNoBundledUI(t *testing.T) {
	t.Parallel()

	var params httpserver.Params

	app := fx.New(appfx.New(), fx.NopLogger, fx.Populate(&params))
	require.NoError(t, app.Err())

	engine, err := httpserver.NewEngine(params.Config)
	require.NoError(t, err)

	for _, option := range params.Options {
		assert.NotEqual(t, "webui", option.Key())
		// Mount only UI options: API options can open database connections.
		if option.Key() == "webui" || option.Key() == "static" {
			require.NoError(t, option.Apply(engine))
		}
	}

	for _, path := range []string{"/", "/webui", "/webui/torrents"} {
		res := httptest.NewRecorder()
		engine.ServeHTTP(res, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		assert.Equal(t, http.StatusNotFound, res.Code, path)
		assert.Empty(t, res.Header().Get("Location"), path)
	}
}
