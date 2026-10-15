-- +goose Up
CREATE TABLE classification_evaluations (
    info_hash bytea NOT NULL REFERENCES torrents(info_hash) ON DELETE CASCADE,
    stage text NOT NULL CHECK (stage IN ('ordinary', 'enhancement')),
    input_fingerprint text NOT NULL DEFAULT '',
    recipe_fingerprint text NOT NULL DEFAULT '',
    outcome text NOT NULL DEFAULT '',
    completed_at timestamptz,
    accepted_input text NOT NULL DEFAULT '',
    accepted_output text NOT NULL DEFAULT '',
    ordinary_output text NOT NULL DEFAULT '',
    generation bigint NOT NULL DEFAULT 0,
    attempt_outcome text NOT NULL DEFAULT '',
    attempted_at timestamptz,
    PRIMARY KEY (info_hash, stage)
);
ALTER TABLE torrent_contents ADD COLUMN inferred_title text NOT NULL DEFAULT '';
ALTER TABLE torrent_contents ADD COLUMN inferred_year smallint;
CREATE INDEX torrent_contents_inferred_year_idx ON torrent_contents (inferred_year)
    WHERE inferred_year IS NOT NULL;

-- +goose Down
ALTER TABLE torrent_contents DROP COLUMN inferred_year, DROP COLUMN inferred_title;
DROP TABLE classification_evaluations;
