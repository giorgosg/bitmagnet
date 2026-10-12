package moderationfx

import (
	"github.com/bitmagnet-io/bitmagnet/internal/config/configfx"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"go.uber.org/fx"
)

func New() fx.Option {
	return fx.Module(
		"moderation",
		configfx.NewConfigModule[moderation.Config]("moderation", moderation.NewDefaultConfig()),
		fx.Provide(moderation.New),
	)
}
