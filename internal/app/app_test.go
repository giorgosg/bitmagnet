package app_test

import (
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/app/appfx"
	"github.com/bitmagnet-io/bitmagnet/internal/app/cli/hooks"
	"github.com/bitmagnet-io/bitmagnet/internal/logging/loggingfx"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// The application graph must resolve. This mirrors app.New exactly, minus
// running it — a missing or ambiguous provider anywhere in appfx fails the whole
// binary at startup, and nothing else in the suite would catch it.
//
// The invoke is load-bearing: fx.ValidateApp only checks dependencies reachable
// from an invoke, so validating a module with none passes unconditionally.
func TestAppGraphResolves(t *testing.T) {
	t.Parallel()

	require.NoError(t, fx.ValidateApp(
		appfx.New(),
		loggingfx.WithLogger(),
		fx.Invoke(func(
			*zap.SugaredLogger,
			*cli.App,
			hooks.AttachedHooks,
		) {
		}),
	))
}
