package devfx

import (
	"os"

	"github.com/bitmagnet-io/bitmagnet/internal/app/cli"
	"github.com/bitmagnet-io/bitmagnet/internal/app/cli/args"
	"github.com/bitmagnet-io/bitmagnet/internal/config/configfx"
	"github.com/bitmagnet-io/bitmagnet/internal/database"
	"github.com/bitmagnet-io/bitmagnet/internal/database/migrations"
	"github.com/bitmagnet-io/bitmagnet/internal/database/postgres"
	"github.com/bitmagnet-io/bitmagnet/internal/dev/app/cmd/fixturecmd"
	"github.com/bitmagnet-io/bitmagnet/internal/dev/app/cmd/gormcmd"
	"github.com/bitmagnet-io/bitmagnet/internal/dev/app/cmd/migratecmd"
	"github.com/bitmagnet-io/bitmagnet/internal/logging/loggingfx"
	"github.com/bitmagnet-io/bitmagnet/internal/validation/validationfx"
	"go.uber.org/fx"
	"go.uber.org/zap/zapcore"
)

func New() fx.Option {
	return fx.Module(
		"dev",
		configfx.NewConfigModule[postgres.Config]("postgres", postgres.NewDefaultConfig()),
		configfx.New(),
		loggingfx.New(),
		// Logs go to stderr. `dev fixture serve` prints one line of JSON on
		// stdout for a harness to parse, and a log line in front of it is read
		// as the announcement.
		fx.Provide(fx.Annotate(
			func() zapcore.WriteSyncer { return zapcore.AddSync(os.Stderr) },
			fx.ResultTags(`name:"log_output"`),
		)),
		validationfx.New(),
		fx.Provide(args.New),
		fx.Provide(cli.New),
		fx.Provide(database.New),
		fx.Provide(migrations.New),
		fx.Provide(postgres.New),
		fx.Provide(fixturecmd.New),
		fx.Provide(gormcmd.New),
		fx.Provide(migratecmd.New),
	)
}
