package processor

import (
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/enhancement"
	"github.com/bitmagnet-io/bitmagnet/internal/worker"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestAutomaticWorkerRequiresExplicitEnablementAndBudget(t *testing.T) {
	t.Parallel()

	for _, automatic := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "missing_budget"}[automatic], func(t *testing.T) {
			t.Parallel()

			config := enhancement.NewDefaultConfig()
			config.Automatic = automatic
			// Nil lazy dependencies would panic if startup touched the database, model or
			// classifier before checking the automatic-processing switch and budget.
			result := NewEnhancementWorker(EnhancementWorkerParams{Config: config})
			registry, err := worker.NewRegistry(
				worker.RegistryParams{
					Workers: []worker.Worker{result.Worker},
					Logger:  zap.NewNop().Sugar(),
				},
			)
			require.NoError(t, err)
			require.NoError(t, registry.Registry.Enable("enhancement"))

			err = registry.Registry.Start(t.Context())
			if automatic {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.NoError(t, registry.Registry.Stop(t.Context()))
			}
		})
	}
}
