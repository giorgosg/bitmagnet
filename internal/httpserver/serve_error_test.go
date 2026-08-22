package httpserver

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type stubShutdowner struct {
	calls int
	err   error
}

func (s *stubShutdowner) Shutdown(...fx.ShutdownOption) error {
	s.calls++

	return s.err
}

func TestHandleServeError_AsksFxToShutDown(t *testing.T) {
	t.Parallel()

	shutdowner := &stubShutdowner{}

	require.NotPanics(t, func() {
		handleServeError(errors.New("accept: too many open files"), zap.NewNop(), shutdowner)
	})

	assert.Equal(t, 1, shutdowner.calls)
}

func TestHandleServeError_ShutdownFailureIsNotFatal(t *testing.T) {
	t.Parallel()

	shutdowner := &stubShutdowner{err: errors.New("already shutting down")}

	require.NotPanics(t, func() {
		handleServeError(errors.New("serve failed"), zap.NewNop(), shutdowner)
	})

	assert.Equal(t, 1, shutdowner.calls)
}

func TestHandleServeError_NormalShutdownIsNotAnError(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "ErrServerClosed", err: http.ErrServerClosed},
		{name: "nil", err: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			shutdowner := &stubShutdowner{}

			require.NotPanics(t, func() {
				handleServeError(tc.err, zap.NewNop(), shutdowner)
			})

			assert.Zero(t, shutdowner.calls)
		})
	}
}
