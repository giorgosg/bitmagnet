package fixtureserver

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
)

// seedDashboardData gives the status, statistics and queue pages something to
// render. Both halves are keyed to the clock, because every one of those pages
// defaults to a short recent window and the seed corpus is a fixed snapshot.
func seedDashboardData(ctx context.Context, query *dao.Query) error {
	now := time.Now().UTC()

	if err := seedQueueJobs(ctx, query, now); err != nil {
		return err
	}

	return refreshTorrentSourceTimestamps(ctx, query, now)
}

// seedQueueJobs inserts a small, deterministic set of queue jobs: every status,
// two queues, and created_at spread over several hours so a chart bucketed by
// hour has more than one column and more than one job per column.
//
// The jobs are not meant to be run. This stack leaves the queue server unwired,
// so nothing picks them up, and the pending ones stay pending for the suite to
// look at.
func seedQueueJobs(ctx context.Context, query *dao.Query, now time.Time) error {
	// bucket is how many whole hours back this job's hourly chart column sits, and
	// offset places it inside that column.
	type seedJob struct {
		queue  string
		status model.QueueJobStatus
		bucket int
		offset time.Duration
	}

	// Truncating first is what actually keeps a pair together. An offset of ten
	// minutes from an arbitrary `now` straddles the hour boundary whenever now is
	// within ten minutes of the hour, which put the pair in adjacent columns and
	// left every column at one.
	hourStart := now.Truncate(time.Hour)

	seedJobs := make([]seedJob, 0, len(seededQueueNames)*8)

	for i, queue := range seededQueueNames {
		for j, status := range []model.QueueJobStatus{
			model.QueueJobStatusPending,
			model.QueueJobStatusRetry,
			model.QueueJobStatusFailed,
			model.QueueJobStatusProcessed,
		} {
			// Two per status per queue, in the *same* hourly column, so the facet
			// counts and the chart's columns are both above one — which is what
			// makes a rendered total worth asserting on. One whole hour back at
			// minimum, so nothing is stamped in the future.
			bucket := i*4 + j + 1

			seedJobs = append(seedJobs,
				seedJob{queue: queue, status: status, bucket: bucket, offset: time.Minute * 10},
				seedJob{queue: queue, status: status, bucket: bucket, offset: time.Minute * 20},
			)
		}
	}

	// Fingerprints are sha256(queue + payload), and migration 00019 puts a
	// partial unique index on them for the pending and retry statuses. A payload
	// that depended only on the job's position would therefore collide the second
	// time this ran against the same database -- which `dev fixture serve` never
	// does, because it clones a fresh template, but a caller reusing a database
	// would have hit it.
	//
	// The wall clock rather than a uuid, so this costs no dependency: two Builds
	// would have to land in the same nanosecond to collide, and the only thing
	// that would cost is a seed conflict on a database somebody chose to reuse.
	run := now.UnixNano()

	jobs := make([]*model.QueueJob, 0, len(seedJobs))

	for i, sp := range seedJobs {
		job, err := model.NewQueueJob(sp.queue, map[string]any{"seed": i, "run": run})
		if err != nil {
			return fmt.Errorf("fixtureserver: building a seed queue job: %w", err)
		}

		createdAt := hourStart.Add(-time.Duration(sp.bucket) * time.Hour).Add(sp.offset)
		job.Status = sp.status
		job.CreatedAt = createdAt
		job.RunAfter = createdAt

		// Everything that has left pending has run, and the chart's latency comes
		// from the gap between the two.
		if sp.status != model.QueueJobStatusPending {
			job.RanAt = sql.NullTime{Time: createdAt.Add(time.Second * 30), Valid: true}
		}

		if sp.status == model.QueueJobStatusFailed || sp.status == model.QueueJobStatusRetry {
			job.Retries = 1
			job.Error = model.NewNullString("seeded failure, so the error column has something to show")
		}

		jobs = append(jobs, &job)
	}

	if err := query.QueueJob.WithContext(ctx).CreateInBatches(jobs, 50); err != nil {
		return fmt.Errorf("fixtureserver: seeding queue jobs: %w", err)
	}

	return nil
}

// seededQueueNames are real queue names, so the facet reads like production's.
var seededQueueNames = []string{"process_torrent", "process_torrent_batch"}

// refreshTorrentSourceTimestamps moves a bounded number of torrent source rows
// into the last hour, so the statistics page has data in the window it opens on.
//
// `torrent.metrics` buckets on `torrents_torrent_sources.updated_at`, and the
// seed corpus is a snapshot whose timestamps are months old — while the page
// defaults to the last hour. So the field answered without error and with zero
// buckets, which is a chart nobody can assert on.
//
// It also splits the rows across the distinction the query itself draws:
// `updated_at > created_at + interval '1 hour'` is what it counts as an update
// rather than a first sighting, so half the rows are given an old created_at and
// half a matching recent one.
//
// This half of the seed **updates rather than inserts**, deliberately: inserting
// synthetic torrents would change what the search pages are tested against. The
// consequence is that it does nothing on an empty database — there is nothing to
// move — so the statistics chart is populated only against a clone of the seed
// template, which is what the browser harness runs on.
func refreshTorrentSourceTimestamps(ctx context.Context, query *dao.Query, now time.Time) error {
	// row_number rather than random(), so two runs produce the same spread.
	const statement = `
with picked as (
  select info_hash, source, row_number() over (order by info_hash) as n
  from torrents_torrent_sources
  limit ?
)
update torrents_torrent_sources s
set updated_at = ?::timestamptz - (interval '1 minute' * (p.n % ?)),
    created_at = case
      when p.n % 2 = 0 then ?::timestamptz - interval '30 days'
      else ?::timestamptz - (interval '1 minute' * (p.n % ?))
    end
from picked p
where s.info_hash = p.info_hash and s.source = p.source`

	result := query.UnderlyingDB().WithContext(ctx).Exec(
		statement,
		refreshedSourceRows,
		now, refreshedSourceSpreadMinutes,
		now,
		now, refreshedSourceSpreadMinutes,
	)
	if result.Error != nil {
		return fmt.Errorf("fixtureserver: refreshing torrent source timestamps: %w", result.Error)
	}

	return nil
}

const (
	// Enough for a chart to have shape, few enough that the write is trivial
	// against a ~114k-row table.
	refreshedSourceRows = 240
	// Inside the hour the statistics page opens on, leaving a few minutes' head
	// room so nothing lands on the boundary.
	refreshedSourceSpreadMinutes = 50
)
