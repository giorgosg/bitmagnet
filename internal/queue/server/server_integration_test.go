package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newTestServer(t *testing.T) (*server, *dbtest.DB) {
	t.Helper()

	db := dbtest.New(t)

	return &server{
		stopped:    make(chan struct{}),
		query:      db.Query,
		pool:       db.Pool,
		gcInterval: time.Minute,
		logger:     zap.NewNop().Sugar(),
	}, db
}

// insertJob adds a job directly, so ran_at and archival_duration can be set to
// whatever the case under test needs. The DAO refuses to write ran_at.
func insertJob(
	ctx context.Context,
	t *testing.T,
	db *dbtest.DB,
	queue string,
	status model.QueueJobStatus,
	ranAt any,
	archival string,
) {
	t.Helper()

	_, err := db.Pool.Exec(ctx, `
		insert into queue_jobs (fingerprint, queue, status, payload, run_after, ran_at, archival_duration, created_at)
		values ($1, $2, $3::queue_job_status, '{}'::jsonb, now(), $4, $5::interval, now())`,
		fmt.Sprintf("fp-%d-%s", time.Now().UnixNano(), status), queue, string(status), ranAt, archival,
	)
	require.NoError(t, err)
}

func countJobs(ctx context.Context, t *testing.T, db *dbtest.DB) int {
	t.Helper()

	var n int

	require.NoError(t, db.Pool.QueryRow(ctx, "select count(*) from queue_jobs").Scan(&n))

	return n
}

func TestRunGCBatchDeletesOnlyExpiredTerminalJobs(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	ctx := t.Context()
	expired := time.Now().Add(-2 * time.Hour)

	// Should be collected: terminal status, archival window elapsed.
	insertJob(ctx, t, db, "q", model.QueueJobStatusProcessed, expired, "1 hour")
	insertJob(ctx, t, db, "q", model.QueueJobStatusFailed, expired, "1 hour")
	// Should survive: still within its archival window.
	insertJob(ctx, t, db, "q", model.QueueJobStatusProcessed, time.Now(), "24 hours")
	// Should survive: not terminal, and never ran.
	insertJob(ctx, t, db, "q", model.QueueJobStatusPending, nil, "1 hour")
	insertJob(ctx, t, db, "q", model.QueueJobStatusRetry, nil, "1 hour")

	require.Equal(t, 5, countJobs(ctx, t, db))

	srv.runGCBatch(ctx)

	assert.Equal(t, 3, countJobs(ctx, t, db), "only expired processed/failed jobs should be collected")

	var remaining []string

	rows, err := db.Pool.Query(ctx, "select status::text from queue_jobs order by status")
	require.NoError(t, err)

	for rows.Next() {
		var s string

		require.NoError(t, rows.Scan(&s))

		remaining = append(remaining, s)
	}

	require.NoError(t, rows.Err())
	assert.ElementsMatch(t, []string{"pending", "processed", "retry"}, remaining)
}

// The batching loop is the point of the change: a single DELETE is capped at
// gcBatchSize, so more than that must take several iterations within one call.
func TestRunGCBatchCollectsMoreThanOneBatch(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	ctx := t.Context()
	total := gcBatchSize + 250

	_, err := db.Pool.Exec(ctx, `
		insert into queue_jobs (fingerprint, queue, status, payload, run_after, ran_at, archival_duration, created_at)
		select 'fp-' || g, 'q', 'processed'::queue_job_status, '{}'::jsonb,
		       now(), now() - interval '2 hours', interval '1 hour', now()
		from generate_series(1, $1) g`, total)
	require.NoError(t, err)
	require.Equal(t, total, countJobs(ctx, t, db))

	srv.runGCBatch(ctx)

	assert.Zero(t, countJobs(ctx, t, db), "the loop should keep going until fewer than a full batch remain")
}

func TestRunGCBatchStopsOnCancelledContext(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	ctx := t.Context()

	insertJob(ctx, t, db, "q", model.QueueJobStatusProcessed, time.Now().Add(-2*time.Hour), "1 hour")

	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.runGCBatch(cancelled)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runGCBatch did not return promptly on a cancelled context")
	}

	assert.Equal(t, 1, countJobs(ctx, t, db), "nothing should be deleted under a cancelled context")
}

// End-to-end proof of the LISTEN/NOTIFY path: migration 00012 installs a trigger
// that calls pg_notify on insert, and the listener must route it to the channel
// belonging to that queue.
func TestListenLoopRoutesNotificationsToTheRightQueue(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	chans := map[string]chan pgconn.Notification{
		"alpha": make(chan pgconn.Notification, 4),
		"beta":  make(chan pgconn.Notification, 4),
	}

	go func() { _ = srv.listenLoop(ctx, chans) }()

	// Give the listener time to issue its LISTEN statements before notifying.
	require.Eventually(t, func() bool {
		var n int
		err := db.Pool.QueryRow(ctx,
			"select count(*) from pg_stat_activity where query like 'LISTEN%' and datname = $1", db.Name,
		).Scan(&n)

		return err == nil && n > 0
	}, 10*time.Second, 50*time.Millisecond, "listener never issued LISTEN")

	insertJob(ctx, t, db, "alpha", model.QueueJobStatusPending, nil, "1 hour")

	select {
	case n := <-chans["alpha"]:
		assert.Equal(t, "alpha", n.Channel)
	case <-time.After(15 * time.Second):
		t.Fatal("no notification arrived on the alpha channel")
	}

	assert.Empty(t, chans["beta"], "beta should not receive alpha's notifications")
}

// Regression test for the listener returning a still-subscribed connection to
// the pool. With MaxConns=1 the pool must hand back the very same connection,
// so a leaked subscription is directly observable.
func TestListenLoopUnsubscribesBeforeReleasingTheConnection(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	ctx := t.Context()

	cfg, err := pgxpool.ParseConfig(db.DSN)
	require.NoError(t, err)

	cfg.MaxConns = 1

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)

	defer pool.Close()

	srv := &server{
		stopped: make(chan struct{}),
		query:   db.Query,
		pool:    pool,
		logger:  zap.NewNop().Sugar(),
	}

	listenCtx, cancelListen := context.WithCancel(ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)

		_ = srv.listenLoop(listenCtx, map[string]chan pgconn.Notification{
			"alpha": make(chan pgconn.Notification, 1),
		})
	}()

	// Probe through the harness pool, not `pool` — the listener is holding the
	// only connection `pool` has, so querying it here would deadlock.
	require.Eventually(t, func() bool {
		var n int
		err := db.Pool.QueryRow(ctx,
			"select count(*) from pg_stat_activity where query like 'LISTEN%' and datname = $1", db.Name,
		).Scan(&n)

		return err == nil && n > 0
	}, 10*time.Second, 50*time.Millisecond, "listener never issued LISTEN")

	cancelListen()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("listenLoop did not return after cancellation")
	}

	// The only connection in the pool is the one the listener just gave back.
	var listening []string

	require.NoError(t, pool.QueryRow(ctx,
		"select coalesce(array_agg(c), '{}') from pg_listening_channels() c",
	).Scan(&listening))
	assert.Empty(t, listening, "the connection was returned to the pool still subscribed")
}
