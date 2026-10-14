-- +goose Up
-- Exact reverse membership lookup for preserving existing metadata on file import.
-- bytea literal keeps the frozen UTF-8 context immutable for an expression index.
CREATE INDEX torrents_moderation_identifier_v1_idx ON torrents
  ((encode(sha256('\x6d61676e65733a6d6f6465726174696f6e3a76313a627469683a'::bytea || info_hash), 'hex')));

-- +goose Down
DROP INDEX torrents_moderation_identifier_v1_idx;
