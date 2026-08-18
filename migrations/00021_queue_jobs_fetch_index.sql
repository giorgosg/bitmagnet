-- +goose Up
-- +goose StatementBegin

-- Adapted from o51r15's fix in o51r15/bitmagnet@6ee9ec54543cabf330d99a186dae6157329fe565.
-- This migration intentionally contains only the queue index work from that commit.

-- Match the queue worker's fetch order so PostgreSQL can select the next
-- runnable job without sorting every pending and retry job first.
CREATE INDEX queue_jobs_fetch_order_idx
    ON queue_jobs (queue, (status = 'retry') DESC, priority, run_after, id)
    WHERE status IN ('pending', 'retry');

-- No current query searches queue job payloads. Maintaining this GIN index on
-- every insert, retry, completion and deletion is therefore pure overhead.
DROP INDEX IF EXISTS queue_jobs_queue_payload_idx;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE INDEX queue_jobs_queue_payload_idx ON queue_jobs USING gin(queue, payload);
DROP INDEX IF EXISTS queue_jobs_fetch_order_idx;

-- +goose StatementEnd
