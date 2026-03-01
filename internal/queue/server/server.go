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
	defer conn.Release()

	pgConn := conn.Conn()

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

		select {
		case ch <- *notification:
		default:
			// Channel full; the polling fallback will pick up the job.
		}
	}
}

const gcBatchSize = 1000

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
		tx := s.query.QueueJob.WithContext(ctx).Where(
			s.query.QueueJob.Status.In(string(model.QueueJobStatusProcessed), string(model.QueueJobStatusFailed)),
		).
			UnderlyingDB().Where(
			"queue_jobs.ran_at + queue_jobs.archival_duration < ?::timestamptz",
			time.Now(),
		).Where(
			"queue_jobs.id IN (SELECT id FROM queue_jobs WHERE status IN (?, ?) AND ran_at + archival_duration < ?::timestamptz LIMIT ?)",
			string(model.QueueJobStatusProcessed), string(model.QueueJobStatusFailed), time.Now(), gcBatchSize,
		).Delete(&model.QueueJob{})
		if tx.Error != nil {
			s.logger.Errorw("error deleting old queue jobs", "error", tx.Error)
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

func (h *serverHandler) handleJob(
	ctx context.Context,
	conds ...gen.Condition,
) (jobID string, processed bool, err error) {
	err = h.query.Transaction(func(tx *dao.Query) error {
		job, findErr := tx.QueueJob.WithContext(ctx).Where(
			append(
				conds,
				h.query.QueueJob.Queue.Eq(h.Queue),
				h.query.QueueJob.Status.In(
					string(model.QueueJobStatusPending),
					string(model.QueueJobStatusRetry),
				),
				h.query.QueueJob.RunAfter.Lte(time.Now()),
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

		jobID = job.ID

		var jobErr error
		if job.Deadline.Valid && job.Deadline.Time.Before(time.Now()) {
			jobErr = ErrJobExceededDeadline

			h.logger.Debugw("job deadline is in the past, skipping", "job_id", job.ID)
		} else {
			// check if the job is being retried and increment retry count accordingly
			if job.Status != model.QueueJobStatusPending {
				job.Retries++
			}
			// execute the queue handler of this job
			jobErr = handler.Exec(ctx, h.Handler, *job)
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

		_, updateErr := tx.QueueJob.WithContext(ctx).Updates(job)

		return updateErr
	})
	if err != nil {
		h.logger.Error("error handling job", "error", err)
	} else if processed {
		h.logger.Debugw("job processed", "job_id", jobID)
	}

	return
}

var ErrJobExceededDeadline = errors.New("the job did not complete before its deadline")
