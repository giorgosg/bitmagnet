package enhancementfx

import (
	"github.com/bitmagnet-io/bitmagnet/internal/config/configfx"
	"github.com/bitmagnet-io/bitmagnet/internal/enhancement"
	"go.uber.org/fx"
)

func New() fx.Option {
	return fx.Module(
		"enhancement",
		configfx.NewConfigModule[enhancement.Config]("enhancement", enhancement.NewDefaultConfig()),
		fx.Provide(enhancement.New),
	)
}
