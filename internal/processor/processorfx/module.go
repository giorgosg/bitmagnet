package processorfx

import (
	"github.com/bitmagnet-io/bitmagnet/internal/processor"
	batchqueue "github.com/bitmagnet-io/bitmagnet/internal/processor/batch/queue"
	processorqueue "github.com/bitmagnet-io/bitmagnet/internal/processor/queue"
	"go.uber.org/fx"
)

func New() fx.Option {
	return fx.Module(
		"processor",
		fx.Provide(
			processor.New,
			processor.NewEnhancementWorker,
			processorqueue.New,
			batchqueue.New,
		),
	)
}
