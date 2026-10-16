package enhancementcmd

import (
	"os"

	"go.uber.org/fx"
	"go.uber.org/zap/zapcore"
)

type LogParams struct {
	fx.In
	Args []string `name:"cli_args"`
}
type LogResult struct {
	fx.Out
	Output zapcore.WriteSyncer `name:"log_output"`
}

// LogOutput keeps structured operator reports alone on stdout, including warnings
// emitted while lazily constructing catalogue or persistent application services.
func LogOutput(p LogParams) LogResult {
	output := os.Stdout
	if len(p.Args) > 1 && p.Args[1] == "enhancement" {
		output = os.Stderr
	}

	return LogResult{Output: zapcore.AddSync(output)}
}
