package server

import (
	"context"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/queue/handler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/sync/semaphore"
)

// newLeaseTestHandler builds a serverHandler over a real database, running the
// given function as the job.
func newLeaseTestHandler(
	t *testing.T,
	db *dbtest.DB,
	queue string,
	handle handler.Func,
) *serverHandler {
	t.Helper()

	return &serverHandler{
		Handler: handler.New(queue, handle,
			handler.Concurrency(1),
			handler.JobTimeout(10*time.Second),
		),
		sem:    semaphore.NewWeighted(1),
		query:  db.Query,
		logger: zap.NewNop().Sugar(),
	}
}

// enqueue writes a runnable job, optionally already leased until the given time.
func enqueue(ctx context.Context, t *testing.T, db *dbtest.DB, queue string, lockedUntil any) string {
	t.Helper()

	job, err := model.NewQueueJob(queue, map[string]string{"test": queue})
	require.NoError(t, err)
	require.NoError(t, db.Query.QueueJob.WithContext(ctx).Create(&job))

	if lockedUntil != nil {
		_, execErr := db.Pool.Exec(ctx,
			"update queue_jobs set locked_until = $1 where id = $2", lockedUntil, job.ID)
		require.NoError(t, execErr)
	}

	return job.ID
}

// The defect: the job ran inside the transaction that claimed it, so a database
// connection stayed pinned and the row stayed locked for the whole run. On the
// classification queue that is a batch of up to a hundred torrents, some of them
// making HTTP calls to TMDB.
func TestJobDoesNotRunInsideItsClaimingTransaction(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	ctx := t.Context()
	jobID := enqueue(ctx, t, db, "lease_test_uncommitted", nil)

	var (
		lockedUntilSeen *time.Time
		rowLockError    error
	)

	h := newLeaseTestHandler(t, db, "lease_test_uncommitted", func(context.Context, model.QueueJob) error {
		// A second connection, while the job is running. Both of these answer
		// "is the claim committed, and is the row still held?".
		conn, err := db.Pool.Acquire(ctx)
		require.NoError(t, err)

		defer conn.Release()

		require.NoError(t, conn.QueryRow(ctx,
			"select locked_until from queue_jobs where id = $1", jobID,
		).Scan(&lockedUntilSeen))

		_, rowLockError = conn.Exec(ctx,
			"select id from queue_jobs where id = $1 for update nowait", jobID)

		return nil
	})

	_, processed, err := h.handleJob(ctx)
	require.NoError(t, err)
	require.True(t, processed, "the job should have been claimed and run")

	require.NotNil(t, lockedUntilSeen,
		"the claim must be committed before the job runs, so another connection can see the lease")
	assert.NoError(t, rowLockError,
		"the job must not hold a row lock on its own queue_jobs row while it runs")
}

// The lease is what replaces the claiming transaction's rollback: a worker that
// dies mid-job leaves the row pending, and it has to become claimable again.
func TestAnExpiredLeaseIsClaimedAgain(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	ctx := t.Context()
	jobID := enqueue(ctx, t, db, "lease_test_expired", time.Now().Add(-time.Minute))

	ran := false
	h := newLeaseTestHandler(t, db, "lease_test_expired", func(context.Context, model.QueueJob) error {
		ran = true

		return nil
	})

	claimed, processed, err := h.handleJob(ctx)
	require.NoError(t, err)
	assert.Equal(t, jobID, claimed)
	assert.True(t, processed)
	assert.True(t, ran)
}

// And a lease that has not expired must be left alone, or two workers run the
// same job at once.
func TestALiveLeaseIsNotClaimed(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	ctx := t.Context()
	enqueue(ctx, t, db, "lease_test_live", time.Now().Add(time.Hour))

	ran := false
	h := newLeaseTestHandler(t, db, "lease_test_live", func(context.Context, model.QueueJob) error {
		ran = true

		return nil
	})

	claimed, processed, err := h.handleJob(ctx)
	require.NoError(t, err)
	assert.Empty(t, claimed, "a job leased by another worker must not be claimed")
	assert.False(t, processed)
	assert.False(t, ran)
}

// A job sent back for a retry has to be claimable at its backoff time, not held
// until the lease it was claimed under would have expired.
func TestARetriedJobIsUnleased(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	ctx := t.Context()
	jobID := enqueue(ctx, t, db, "lease_test_retry", nil)

	_, err := db.Pool.Exec(ctx, "update queue_jobs set max_retries = 3 where id = $1", jobID)
	require.NoError(t, err)

	h := newLeaseTestHandler(t, db, "lease_test_retry", func(context.Context, model.QueueJob) error {
		return assert.AnError
	})

	_, processed, err := h.handleJob(ctx)
	require.NoError(t, err)
	require.False(t, processed)

	var (
		status      string
		lockedUntil *time.Time
	)

	require.NoError(t, db.Pool.QueryRow(ctx,
		"select status::text, locked_until from queue_jobs where id = $1", jobID,
	).Scan(&status, &lockedUntil))
	assert.Equal(t, string(model.QueueJobStatusRetry), status)
	assert.Nil(t, lockedUntil, "a job released for retry must not stay leased")
}

// A worker that overran its lease has already been replaced: the job may have
// been claimed and run again, and the late outcome must not overwrite the newer
// one.
func TestAnOverrunWorkerDoesNotOverwriteANewerClaim(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	ctx := t.Context()
	jobID := enqueue(ctx, t, db, "lease_test_overrun", nil)

	h := newLeaseTestHandler(t, db, "lease_test_overrun", func(context.Context, model.QueueJob) error {
		// Somebody else's claim, taken while this job was running long.
		_, err := db.Pool.Exec(ctx,
			"update queue_jobs set locked_until = now() + interval '1 hour' where id = $1", jobID)
		require.NoError(t, err)

		return nil
	})

	_, _, err := h.handleJob(ctx)
	require.NoError(t, err)

	var status string

	require.NoError(t, db.Pool.QueryRow(ctx,
		"select status::text from queue_jobs where id = $1", jobID,
	).Scan(&status))
	assert.Equal(t, string(model.QueueJobStatusPending), status,
		"the outcome belongs to whoever holds the lease now")
}
