-- +goose Up
-- +goose StatementBegin

-- locked_until is the claim's lease. A worker takes a job by setting it and
-- committing, runs the job with no transaction open, then writes the outcome —
-- instead of holding the claiming transaction, and a database connection, for
-- the whole of the job's run.
--
-- It is also the recovery path: a worker that dies mid-job leaves the row
-- pending with a lease that expires, and the fetch below picks it up again. That
-- is what the claiming transaction's rollback used to do.
alter table queue_jobs
  add column locked_until timestamp with time zone;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

alter table queue_jobs
  drop column locked_until;

-- +goose StatementEnd
