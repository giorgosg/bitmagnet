-- +goose Up
-- +goose StatementBegin

-- token_epoch is the generation of a user's sessions. Every JWT carries the
-- value current when it was minted, and authentication refuses a token whose
-- epoch is behind the row's, so bumping it revokes every outstanding token for
-- that user at once. Existing tokens carry no epoch and decode as 0, which
-- matches this default, so the migration invalidates nothing on its own.
alter table users
  add column token_epoch integer not null default 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

alter table users
  drop column token_epoch;

-- +goose StatementEnd
