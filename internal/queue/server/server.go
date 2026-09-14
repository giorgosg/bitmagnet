// Hybrid LISTEN/NOTIFY + polling: the listener wakes the handler immediately
// for new jobs; polling runs as a safety net at the configured CheckInterval.

package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/queue"
	"github.com/bitmagnet-io/bitmagnet/internal/queue/handler"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"golang.org/x/sync/semaphore"
	"gorm.io/gen"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type server struct {
	stopped    chan struct{}
	query      *dao.Query
	pool       *pgxpool.Pool
	handlers   []handler.Handler
	gcInterval time.Duration
	logger     *zap.SugaredLogger
}

func (s *server) Start(ctx context.Context) (err error) {
	ctx, cancel := context.WithCancel(ctx)

	defer func() {
		if err != nil {
			cancel()
		}
	}()

	handlers := make([]serverHandler, len(s.handlers))
	listenerChans := make(map[string]chan pgconn.Notification)

	for i, h := range s.handlers {
		listenerChan := make(chan pgconn.Notification, h.Concurrency)
		sh := serverHandler{
			Handler:      h,
			sem:          semaphore.NewWeighted(int64(h.Concurrency)),
			query:        s.query,
			listenerChan: listenerChan,
			logger:       s.logger.With("queue", h.Queue),
		}
		handlers[i] = sh
		listenerChans[h.Queue] = listenerChan

		go sh.start(ctx)
	}

	go func() {
		for {
			select {
			case <-s.stopped:
				cancel()
			case <-ctx.Done():
				return
			}
		}
	}()

	// Start LISTEN/NOTIFY listener for instant job wakeup.
	go s.runListener(ctx, listenerChans)

	go s.runGarbageCollection(ctx)

	return
}

func (s *server) runListener(ctx context.Context, listenerChans map[string]chan pgconn.Notification) {
	for {
		if ctx.Err() != nil {
			return
		}

		if err := s.listenLoop(ctx, listenerChans); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}

			s.logger.Warnw("listener disconnected, reconnecting in 5s", "error", err)

			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

func (s *server) listenLoop(ctx context.Context, listenerChans map[string]chan pgconn.Notification) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}

	pgConn := conn.Conn()

	// pgxpool does not reset session state on release, so a connection handed
	// back while still subscribed would deliver notifications to whatever uses
	// it next. Drop the subscriptions before returning it to the pool.
	defer func() {
		unlistenCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()

		if _, unlistenErr := pgConn.Exec(unlistenCtx, "UNLISTEN *"); unlistenErr != nil {
			// Likely already broken. Take it out of the pool and close it rather
			// than returning a possibly-still-subscribed connection.
			_ = conn.Hijack().Close(unlistenCtx)
			return
		}

		conn.Release()
	}()

	for ch := range listenerChans {
		if _, execErr := pgConn.Exec(ctx, fmt.Sprintf("LISTEN %q", ch)); execErr != nil {
			return execErr
		}
	}

	for {
		notification, waitErr := pgConn.WaitForNotification(ctx)
		if waitErr != nil {
			return waitErr
		}

		ch, ok := listenerChans[notification.Channel]
		if !ok {
			continue
		}

		// A full channel means the handler is already saturated; the polling
		// fallback will pick the job up.
		select {
		case ch <- *notification:
		default:
		}
	}
}

const gcBatchSize = 1000

// gcBatchPredicate bounds a delete to one batch of expired jobs. The LIMIT lives
// in a subquery because Postgres does not accept LIMIT directly on DELETE.
const gcBatchPredicate = "queue_jobs.id IN (" +
	"SELECT id FROM queue_jobs " +
	"WHERE status IN (?, ?) AND ran_at + archival_duration < ?::timestamptz " +
	"LIMIT ?)"

func (s *server) runGarbageCollection(ctx context.Context) {
	for {
		s.runGCBatch(ctx)

		select {
		case <-ctx.Done():
			return
		case <-time.After(s.gcInterval):
			continue
		}
	}
}

// runGCBatch deletes expired jobs in batches to avoid long-held locks and
// excessive WAL generation that could happen with unbounded DELETEs.
func (s *server) runGCBatch(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}

		// The subquery already carries the full predicate; repeating it in the
		// outer WHERE only makes the plan harder to read.
		tx := s.query.QueueJob.WithContext(ctx).UnderlyingDB().Where(
			gcBatchPredicate,
			string(model.QueueJobStatusProcessed),
			string(model.QueueJobStatusFailed),
			time.Now(),
			gcBatchSize,
		).Delete(&model.QueueJob{})

		if tx.Error != nil {
			// A cancelled context during shutdown is expected, not an error.
			if !errors.Is(tx.Error, context.Canceled) {
				s.logger.Errorw("error deleting old queue jobs", "error", tx.Error)
			}

			return
		}

		if tx.RowsAffected > 0 {
			s.logger.Debugw("deleted old queue jobs", "count", tx.RowsAffected)
		}

		// If we deleted fewer than the batch size, we're done for this cycle.
		if tx.RowsAffected < gcBatchSize {
			return
		}
	}
}

type serverHandler struct {
	handler.Handler
	sem   *semaphore.Weighted
	query *dao.Query
	// listenerConn *pgx.Conn
	listenerChan chan pgconn.Notification
	logger       *zap.SugaredLogger
}

func (h *serverHandler) start(ctx context.Context) {
	checkTicker := time.NewTicker(1)

	for {
		select {
		case <-ctx.Done():
			return
		case notification := <-h.listenerChan:
			if semErr := h.sem.Acquire(ctx, 1); semErr != nil {
				return
			}

			go func() {
				defer h.sem.Release(1)

				_, _, _ = h.handleJob(ctx, h.query.QueueJob.ID.Eq(notification.Payload))
			}()
		case <-checkTicker.C:
			if semErr := h.sem.Acquire(ctx, 1); semErr != nil {
				return
			}

			checkTicker.Reset(h.CheckInterval)

			go func() {
				defer h.sem.Release(1)

				jobID, _, err := h.handleJob(ctx)
				// if a job was found, we should check straight away for another job,
				// otherwise we wait for the check interval
				if err == nil && jobID != "" {
					checkTicker.Reset(1)
				}
			}()
		}
	}
}

// leaseSlack is added to a handler's job timeout to get the lease a claim takes
// out. handler.Exec returns within JobTimeout whatever the job does, so the
// slack only has to cover writing the outcome afterwards.
const leaseSlack = 30 * time.Second

// handleJob claims one job, runs it, and records what happened. The three steps
// are deliberately not one transaction: the job used to run inside the
// transaction that claimed it, which pinned a database connection `idle in
// transaction` and held the row lock for the whole run -- for the classification
// queue, a batch of up to a hundred torrents, some of them making HTTP calls.
//
// What the claiming transaction's rollback used to provide is now the lease: a
// worker that dies mid-job leaves the row pending with a locked_until that
// expires, and the next fetch picks it up. Retries are still counted only when
// an outcome is written, so an abandoned job is retried without consuming one --
// as it was before.
func (h *serverHandler) handleJob(
	ctx context.Context,
	conds ...gen.Condition,
) (jobID string, processed bool, err error) {
	job, lease, claimed, err := h.claimJob(ctx, conds...)
	if err != nil {
		h.logger.Errorw("error claiming job", "error", err)

		return "", false, err
	}

	if !claimed {
		return "", false, nil
	}

	jobID = job.ID

	var jobErr error

	if job.Deadline.Valid && job.Deadline.Time.Before(time.Now()) {
		jobErr = ErrJobExceededDeadline

		h.logger.Debugw("job deadline is in the past, skipping", "job_id", job.ID)
	} else {
		jobErr = handler.Exec(ctx, h.Handler, job)
	}

	job.RanAt = sql.NullTime{Time: time.Now(), Valid: true}

	if jobErr != nil {
		h.logger.Errorw("job failed", "error", jobErr)

		if job.Retries < job.MaxRetries {
			job.Status = model.QueueJobStatusRetry
			job.RunAfter = queue.CalculateBackoff(job.Retries)
		} else {
			job.Status = model.QueueJobStatusFailed
		}

		job.Error = model.NewNullString(jobErr.Error())
	} else {
		job.Status = model.QueueJobStatusProcessed
		processed = true
	}

	if err = h.completeJob(ctx, job, lease); err != nil {
		h.logger.Errorw("error handling job", "error", err)

		return jobID, false, err
	}

	if processed {
		h.logger.Debugw("job processed", "job_id", jobID)
	}

	return jobID, processed, nil
}

// claimJob takes the next runnable job and leases it, in a transaction short
// enough to be measured in milliseconds. FOR UPDATE SKIP LOCKED still separates
// workers racing for the same row inside this transaction; the lease is what
// separates them for the duration of the run, once it has committed.
func (h *serverHandler) claimJob(
	ctx context.Context,
	conds ...gen.Condition,
) (job model.QueueJob, lease time.Time, claimed bool, err error) {
	lease = time.Now().Add(h.JobTimeout + leaseSlack)

	err = h.query.Transaction(func(tx *dao.Query) error {
		found, findErr := tx.QueueJob.WithContext(ctx).Where(
			append(
				conds,
				h.query.QueueJob.Queue.Eq(h.Queue),
				h.query.QueueJob.Status.In(
					string(model.QueueJobStatusPending),
					string(model.QueueJobStatusRetry),
				),
				h.query.QueueJob.RunAfter.Lte(time.Now()),
				// Not claimed, or claimed by a worker that is no longer coming
				// back. Nothing else reaps those: the expiry is the reaping.
				tx.QueueJob.Where(
					tx.QueueJob.Where(h.query.QueueJob.LockedUntil.IsNull()),
				).Or(
					h.query.QueueJob.LockedUntil.Lte(sql.NullTime{Time: time.Now(), Valid: true}),
				),
			)...,
		).Order(
			h.query.QueueJob.Status.Eq(string(model.QueueJobStatusRetry)),
			h.query.QueueJob.Priority,
			h.query.QueueJob.RunAfter,
		).Clauses(clause.Locking{
			Strength: "UPDATE",
			Options:  "SKIP LOCKED",
		}).First()
		if findErr != nil {
			if errors.Is(findErr, gorm.ErrRecordNotFound) {
				return nil
			}

			return findErr
		}

		// A job being retried consumes one of its retries. Counted here and
		// written with the outcome, exactly as before.
		if found.Status != model.QueueJobStatusPending {
			found.Retries++
		}

		if _, updateErr := tx.QueueJob.WithContext(ctx).
			Where(tx.QueueJob.ID.Eq(found.ID)).
			UpdateSimple(tx.QueueJob.LockedUntil.Value(sql.NullTime{Time: lease, Valid: true})); updateErr != nil {
			return updateErr
		}

		job, claimed = *found, true

		return nil
	})

	return job, lease, claimed, err
}

// completeJob writes the outcome, but only while this worker still holds the
// lease it claimed under. A job that overran its lease has already been handed
// to somebody else, and the late outcome would otherwise overwrite theirs -- and
// mark as processed a job whose second run has not finished.
func (h *serverHandler) completeJob(ctx context.Context, job model.QueueJob, lease time.Time) error {
	info, err := h.query.QueueJob.WithContext(ctx).Where(
		h.query.QueueJob.ID.Eq(job.ID),
		h.query.QueueJob.LockedUntil.Eq(sql.NullTime{Time: lease, Valid: true}),
	).UpdateSimple(
		h.query.QueueJob.Status.Value(string(job.Status)),
		h.query.QueueJob.Retries.Value(job.Retries),
		h.query.QueueJob.RunAfter.Value(job.RunAfter),
		h.query.QueueJob.RanAt.Value(job.RanAt),
		h.query.QueueJob.Error.Value(job.Error),
		// Released either way: a job sent back for a retry has to be claimable at
		// its backoff time, not held until the lease would have expired.
		h.query.QueueJob.LockedUntil.Null(),
	)
	if err != nil {
		return err
	}

	if info.RowsAffected == 0 {
		h.logger.Warnw("job outcome discarded: the lease had already expired",
			"job_id", job.ID, "status", job.Status)
	}

	return nil
}

var ErrJobExceededDeadline = errors.New("the job did not complete before its deadline")
