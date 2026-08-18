package classifier

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewLocalSearchSemaphoreUsesConfiguredCapacity(t *testing.T) {
	t.Parallel()

	semaphore := newLocalSearchSemaphore(nil, 3)

	require.Equal(t, 3, cap(semaphore.semaphore))
}

func TestNewLocalSearchSemaphoreFallsBackForNonPositiveCapacity(t *testing.T) {
	t.Parallel()

	semaphore := newLocalSearchSemaphore(nil, 0)

	require.Equal(t, 1, cap(semaphore.semaphore))
}

func TestDefaultConfigSetsSearchConcurrency(t *testing.T) {
	t.Parallel()

	require.Equal(t, 5, NewDefaultConfig().SearchConcurrency)
}
