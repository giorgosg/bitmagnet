package blocking

import (
	"context"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestManager builds a manager whose refresh interval has already elapsed, so
// every call takes the refresh path.
func newTestManager(pool *pgxpool.Pool) *manager {
	return &manager{
		pool:          pool,
		buffer:        make(map[protocol.ID]struct{}),
		maxBufferSize: 1000,
		maxFlushWait:  0,
	}
}

func hashOf(t *testing.T, b byte) protocol.ID {
	t.Helper()

	var id protocol.ID
	for i := range id {
		id[i] = b
	}

	return id
}

// largeObjectVersions returns the row versions backing the stored bloom filter.
// A rewrite of the large object replaces every row, so these values change.
func largeObjectVersions(ctx context.Context, t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()

	rows, err := pool.Query(ctx, `
		SELECT l.pageno, l.xmin::text
		FROM pg_largeobject l
		JOIN bloom_filters b ON b.oid = l.loid
		WHERE b.key = $1
		ORDER BY l.pageno`, blockedTorrentsBloomFilterKey)
	require.NoError(t, err)

	defer rows.Close()

	var versions []string

	for rows.Next() {
		var (
			pageno int32
			xmin   string
		)

		require.NoError(t, rows.Scan(&pageno, &xmin))

		versions = append(versions, xmin)
	}

	require.NoError(t, rows.Err())

	return versions
}

func TestFilter_DoesNotRewriteTheLargeObjectWithAnEmptyBuffer(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	ctx := t.Context()

	m := newTestManager(db.Pool)

	// Persist one blocked hash, so there is a stored filter to refresh from.
	require.NoError(t, m.Block(ctx, []protocol.ID{hashOf(t, 0x01)}, true))

	before := largeObjectVersions(ctx, t, db.Pool)
	require.NotEmpty(t, before, "expected a stored bloom filter")

	// The buffer is empty and maxFlushWait has elapsed: this is a read.
	_, err := m.Filter(ctx, []protocol.ID{hashOf(t, 0x02)})
	require.NoError(t, err)

	assert.Equal(t, before, largeObjectVersions(ctx, t, db.Pool),
		"Filter rewrote the bloom filter large object with nothing to persist")
}

func TestFilter_StillPicksUpHashesBlockedByAnotherProcess(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	ctx := t.Context()

	local, other := newTestManager(db.Pool), newTestManager(db.Pool)
	blocked := hashOf(t, 0x03)

	// Prime the local manager's filter, so it has something to refresh.
	_, err := local.Filter(ctx, []protocol.ID{hashOf(t, 0x04)})
	require.NoError(t, err)

	// Another process blocks a hash and persists it.
	require.NoError(t, other.Block(ctx, []protocol.ID{blocked}, true))

	// The refresh is what makes that visible here; it must survive the read path.
	filtered, err := local.Filter(ctx, []protocol.ID{blocked, hashOf(t, 0x05)})
	require.NoError(t, err)

	assert.NotContains(t, filtered, blocked, "refresh did not pick up the blocked hash")
	assert.Contains(t, filtered, hashOf(t, 0x05))
}

func TestBlock_PersistsAndKeepsFiltering(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	ctx := t.Context()

	m := newTestManager(db.Pool)
	blocked := hashOf(t, 0x06)

	require.NoError(t, m.Block(ctx, []protocol.ID{blocked}, true))

	before := largeObjectVersions(ctx, t, db.Pool)

	// Blocking a second hash must still write.
	require.NoError(t, m.Block(ctx, []protocol.ID{hashOf(t, 0x07)}, true))

	assert.NotEqual(t, before, largeObjectVersions(ctx, t, db.Pool),
		"Block should have persisted the new hash")

	filtered, err := m.Filter(ctx, []protocol.ID{blocked, hashOf(t, 0x08)})
	require.NoError(t, err)
	assert.Equal(t, []protocol.ID{hashOf(t, 0x08)}, filtered)
}
