-- +goose Up
-- +goose StatementBegin
-- Discard opaque legacy blocks, never translate them into moderation decisions.
-- Preserve unrelated filter keys, and unlink only an unshared, existing object.
SELECT lo_unlink(legacy.oid)
FROM bloom_filters AS legacy
WHERE legacy.key = 'blocked_torrents'
  AND legacy.oid IS NOT NULL
  AND EXISTS (SELECT 1 FROM pg_largeobject_metadata WHERE oid = legacy.oid)
  AND NOT EXISTS (
    SELECT 1 FROM bloom_filters AS other
    WHERE other.key <> legacy.key AND other.oid = legacy.oid
  );
DELETE FROM bloom_filters WHERE key = 'blocked_torrents';
-- +goose StatementEnd

-- +goose Down
-- Intentionally irreversible: discarded Bloom entries have no review provenance.
SELECT 1;
