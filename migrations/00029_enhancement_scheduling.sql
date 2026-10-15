-- +goose Up
-- Readiness is a bounded selection hint, not evaluation memory. Legacy rows start
-- eligible; only selected rows are reclassified, claimed, or enqueued.
ALTER TABLE torrents ADD COLUMN enhancement_ready boolean NOT NULL DEFAULT false,
    ADD COLUMN enhancement_recent boolean NOT NULL DEFAULT false,
    ADD COLUMN enhancement_seeders bigint,
    ADD COLUMN enhancement_checked_at timestamptz NOT NULL DEFAULT now();
UPDATE torrents SET enhancement_recent = created_at >= now() - interval '7 days',
 enhancement_ready = EXISTS (SELECT 1 FROM torrent_contents tc WHERE tc.info_hash = torrents.info_hash);
UPDATE torrents t SET enhancement_seeders = s.seeders FROM
    (SELECT info_hash, max(seeders) seeders FROM torrents_torrent_sources GROUP BY info_hash) s
    WHERE s.info_hash = t.info_hash;
CREATE INDEX torrents_enhancement_rank ON torrents
    (enhancement_recent, (coalesce(enhancement_seeders, -1)) DESC, created_at, info_hash)
    WHERE enhancement_ready;
CREATE INDEX torrents_enhancement_boundary ON torrents (created_at, info_hash)
    WHERE enhancement_recent;
CREATE INDEX torrents_enhancement_reconsider ON torrents (enhancement_checked_at, info_hash)
    WHERE NOT enhancement_ready;
-- +goose StatementBegin
CREATE FUNCTION enhancement_source_rank() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE h bytea;
BEGIN
    h := CASE WHEN TG_OP = 'DELETE' THEN OLD.info_hash ELSE NEW.info_hash END;
    PERFORM 1 FROM torrents WHERE info_hash = h FOR UPDATE;
    UPDATE torrents SET enhancement_seeders =
      (SELECT max(seeders) FROM torrents_torrent_sources WHERE info_hash = h)
      WHERE info_hash = h;
    RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER enhancement_source_rank AFTER INSERT OR UPDATE OR DELETE ON torrents_torrent_sources
    FOR EACH ROW EXECUTE FUNCTION enhancement_source_rank();
-- +goose StatementBegin
CREATE FUNCTION enhancement_new_torrent() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.enhancement_recent := NEW.created_at >= now() - interval '7 days';
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER enhancement_new_torrent BEFORE INSERT ON torrents
    FOR EACH ROW EXECUTE FUNCTION enhancement_new_torrent();
CREATE TABLE enhancement_claims (
    info_hash bytea PRIMARY KEY REFERENCES torrents(info_hash) ON DELETE CASCADE,
    token text NOT NULL UNIQUE,
    claimed_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    started boolean NOT NULL DEFAULT false,
    generation bigint NOT NULL DEFAULT 0,
    input_fingerprint text NOT NULL DEFAULT '',
    recipe_fingerprint text NOT NULL DEFAULT ''
);
CREATE INDEX enhancement_claims_expiry ON enhancement_claims (expires_at);
CREATE TABLE enhancement_daily_spending (
    day date PRIMARY KEY,
    charged numeric NOT NULL DEFAULT 0 CHECK (charged >= 0)
);
CREATE TABLE enhancement_reservations (
    token text PRIMARY KEY,
    day date NOT NULL REFERENCES enhancement_daily_spending(day),
    cost numeric NOT NULL CHECK (cost > 0)
);
ALTER TABLE classification_evaluations ADD COLUMN attempt_input text NOT NULL DEFAULT '',
    ADD COLUMN attempt_recipe text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE classification_evaluations DROP COLUMN attempt_input, DROP COLUMN attempt_recipe;
DROP TABLE enhancement_reservations, enhancement_daily_spending, enhancement_claims;
DROP TRIGGER enhancement_source_rank ON torrents_torrent_sources;
DROP FUNCTION enhancement_source_rank();
DROP TRIGGER enhancement_new_torrent ON torrents;
DROP FUNCTION enhancement_new_torrent();
ALTER TABLE torrents DROP COLUMN enhancement_ready, DROP COLUMN enhancement_recent,
    DROP COLUMN enhancement_seeders, DROP COLUMN enhancement_checked_at;
