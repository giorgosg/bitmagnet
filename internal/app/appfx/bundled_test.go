//go:build webui

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

func TestDefaultAppRegistersBundledUI(t *testing.T) {
	t.Parallel()

	var params httpserver.Params

	app := fx.New(appfx.New(), fx.NopLogger, fx.Populate(&params))
	require.NoError(t, app.Err())

	engine, err := httpserver.NewEngine(params.Config)
	require.NoError(t, err)

	for _, option := range params.Options {
		if option.Key() == "static" {
			require.NoError(t, option.Apply(engine))
		}
	}

	res := httptest.NewRecorder()
	engine.ServeHTTP(res, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ui/login", nil))
	assert.Equal(t, http.StatusOK, res.Code)
	assert.Contains(t, res.Body.String(), "Elm.Main.init")
}
