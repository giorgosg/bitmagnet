-- +goose Up
-- +goose StatementBegin
CREATE TABLE moderation_decisions (
  identifier text PRIMARY KEY CHECK (identifier ~ '^[0-9a-f]{64}$'),
  status text NOT NULL CHECK (status IN ('pending', 'allowed', 'banned')),
  origin text NOT NULL CHECK (origin IN ('moderator', 'automatic', 'import')),
  imported_origin text NOT NULL DEFAULT '' CHECK (imported_origin IN ('', 'moderator', 'automatic')),
  source text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (status <> 'allowed' OR origin = 'moderator'),
  CHECK ((origin = 'import') = (imported_origin <> ''))
);
CREATE INDEX moderation_decisions_status_identifier_idx ON moderation_decisions (status, identifier);
-- Private rows belong to the ledger, never to the disposable catalogue.
CREATE TABLE moderation_evidence (
  identifier text PRIMARY KEY REFERENCES moderation_decisions(identifier) ON DELETE CASCADE,
  data jsonb NOT NULL
);
CREATE TABLE moderation_history (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  identifier text NOT NULL REFERENCES moderation_decisions(identifier) ON DELETE CASCADE,
  action text NOT NULL,
  status text NOT NULL CHECK (status IN ('pending', 'allowed', 'banned')),
  origin text NOT NULL CHECK (origin IN ('moderator', 'automatic', 'import')),
  imported_origin text NOT NULL DEFAULT '',
  source text NOT NULL DEFAULT '',
  actor text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX moderation_history_identifier_id_idx ON moderation_history (identifier, id);
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP TABLE moderation_history;
DROP TABLE moderation_evidence;
DROP TABLE moderation_decisions;
-- +goose StatementEnd
