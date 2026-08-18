package migrationssql_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueueFetchOrderIndexDefinition(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	ctx := context.Background()

	var definition string

	require.NoError(t, db.Pool.QueryRow(ctx, `
		select pg_get_indexdef(to_regclass('queue_jobs_fetch_order_idx'))
	`).Scan(&definition))
	assert.Contains(t, definition,
		"USING btree (queue, ((status = 'retry'::queue_job_status)) DESC, priority, run_after, id)",
	)
	assert.Contains(t, definition,
		"WHERE (status = ANY (ARRAY['pending'::queue_job_status, 'retry'::queue_job_status]))",
	)
}

func TestQueuePayloadIndexIsAbsent(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	ctx := context.Background()

	var exists bool

	require.NoError(t, db.Pool.QueryRow(ctx, `
		select to_regclass('queue_jobs_queue_payload_idx') is not null
	`).Scan(&exists))
	assert.False(t, exists)
}

func TestQueueFetchPlanUsesOrderIndex(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	ctx := context.Background()

	_, err := db.Pool.Exec(ctx, `
			insert into queue_jobs (
				fingerprint, queue, status, payload, priority, run_after,
				archival_duration, created_at
			)
			select
				'fetch-plan-' || n,
				'process_torrent',
				case when n % 5 = 0
					then 'retry'::queue_job_status
					else 'pending'::queue_job_status
				end,
				'{}'::jsonb,
				n % 3,
				now() - (n || ' seconds')::interval,
				'7 days'::interval,
				now()
			from generate_series(1, 2000) n
		`)
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, "analyze queue_jobs")
	require.NoError(t, err)

	rows, err := db.Pool.Query(ctx, `
			explain (costs off)
			select id
			from queue_jobs
			where queue = 'process_torrent'
				and status in ('pending', 'retry')
				and run_after <= now()
			order by (status = 'retry') desc, priority, run_after, id
			for update skip locked
			limit 1
		`)
	require.NoError(t, err)
	defer rows.Close()

	var planLines []string

	for rows.Next() {
		var line string

		require.NoError(t, rows.Scan(&line))
		planLines = append(planLines, line)
	}

	require.NoError(t, rows.Err())
	assert.Contains(t, strings.Join(planLines, "\n"), "Index Scan using queue_jobs_fetch_order_idx")
}
