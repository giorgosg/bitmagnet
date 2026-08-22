package prometheus

import (
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newTestCollector(t *testing.T, timeout time.Duration) *queueMetricsCollector {
	t.Helper()

	db := dbtest.New(t)

	return &queueMetricsCollector{
		query:   lazy.New(func() (*dao.Query, error) { return db.Query, nil }),
		logger:  zap.NewNop().Sugar(),
		timeout: timeout,
	}
}

// TestCollect_QueryIsBounded proves the aggregate runs under a deadline: with a
// timeout that has effectively no room, the query must fail rather than run to
// completion. Against an unbounded context.Background() it always succeeds.
func TestCollect_QueryIsBounded(t *testing.T) {
	t.Parallel()

	qmc := newTestCollector(t, time.Nanosecond)

	_, err := qmc.collectQueueStatusInfos()

	require.Error(t, err, "the query ran without a deadline")
	assert.Contains(t, err.Error(), "context deadline exceeded")
}

// TestCollect_SucceedsWithinTheDeadline guards the ordinary path, so the bound
// above cannot be satisfied by simply breaking collection.
func TestCollect_SucceedsWithinTheDeadline(t *testing.T) {
	t.Parallel()

	qmc := newTestCollector(t, 30*time.Second)

	infos, err := qmc.collectQueueStatusInfos()

	require.NoError(t, err)
	assert.Empty(t, infos, "a fresh database has no queue jobs")
}
