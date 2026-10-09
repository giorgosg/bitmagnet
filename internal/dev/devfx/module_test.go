package devfx_test

import (
	"io"
	"os"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/dev/devfx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// `dev fixture serve` promises a harness exactly one line on stdout: the JSON
// announcement. Anything the dev binary logs has to go to stderr instead, or it
// lands in front of that line and the harness parses a log line as the
// announcement.
//
// Not parallel: it swaps the process's stdout and stderr, which the logger
// captures when it is built.
//
//nolint:paralleltest
func TestDevLoggerWritesToStderrNotStdout(t *testing.T) {
	stdout := swapFile(t, &os.Stdout)
	stderr := swapFile(t, &os.Stderr)

	var logger *zap.SugaredLogger

	app := fx.New(devfx.New(), fx.NopLogger, fx.Populate(&logger))
	require.NoError(t, app.Err())

	logger.Infow("a line that must not reach stdout")
	_ = logger.Sync()

	assert.Empty(t, stdout(), "the dev binary logged to stdout")
	assert.Contains(t, stderr(), "a line that must not reach stdout")
}

// swapFile replaces *target with a pipe for the rest of the test and returns a
// function that closes the write end and reads what was written.
func swapFile(t *testing.T, target **os.File) func() string {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err)

	original := *target
	*target = w

	t.Cleanup(func() { *target = original })

	return func() string {
		require.NoError(t, w.Close())

		read, readErr := io.ReadAll(r)
		require.NoError(t, readErr)

		return string(read)
	}
}
