package moderation_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/stretchr/testify/require"
)

func TestBanImportIndexedLookupMatchesFrozenIndependentVectors(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	service := moderation.NewService(db.Gorm)

	for _, vector := range []struct{ hash, digest string }{
		{
			"0000000000000000000000000000000000000000",
			"20fed20512528a3e12b7bcda59b0c84eff6bbe33007773edc0ecc785d2c450b6",
		},
		{
			"ffffffffffffffffffffffffffffffffffffffff",
			"9afaa45890d2acf0f8760eb8c7bcb8f55193d8eadf816f1165a02410b68912be",
		},
		{
			"000102030405060708090a0b0c0d0e0f10111213",
			"41a75932cf9e555dca1575a5029f6a665d8ec8ef568e978298a9dbff8c9a913b",
		},
		{
			"0080ff017f0281fe030405060708090a0b0c0d0e",
			"25e64157619734bf1d1fc41154d6cf1caecc7cab0ca5446f7b308c34dd2f1091",
		},
	} {
		hash := protocol.MustParseID(vector.hash)
		require.NoError(
			t,
			db.Gorm.Create(
				&model.Torrent{
					InfoHash:    hash,
					Name:        "Harmless indexed vector",
					Size:        42,
					FilesStatus: model.FilesStatusSingle,
				},
			).Error,
		)

		data, err := json.Marshal(
			moderation.BanFile{
				Version:          1,
				IdentifierFormat: moderation.IdentifierFormatV1,
				Entries: []moderation.BanEntry{
					{Digest: vector.digest, Origin: moderation.Moderator},
				},
			},
		)
		require.NoError(t, err)
		_, err = service.Import(t.Context(), bytes.NewReader(data))
		require.NoError(t, err)
		id, err := moderation.ParseIdentifier(moderation.IdentifierFormatV1, vector.digest)
		require.NoError(t, err)
		record, err := service.Get(t.Context(), id)
		require.NoError(t, err)
		require.NotNil(t, record.Evidence)
		require.Equal(t, &hash, record.Evidence.InfoHash)
		require.Equal(t, []string{"Harmless indexed vector"}, record.Evidence.Files)
	}
}

func TestBanImportReverseLookupUsesIndexAtFixtureScale(t *testing.T) {
	t.Parallel()
	db := dbtest.NewSeeded(t)

	var plan string

	rows, err := db.Pool.Query(t.Context(), `EXPLAIN (COSTS OFF) SELECT info_hash FROM torrents
 WHERE encode(sha256('\x6d61676e65733a6d6f6465726174696f6e3a76313a627469683a'::bytea || info_hash),'hex') =
 '20fed20512528a3e12b7bcda59b0c84eff6bbe33007773edc0ecc785d2c450b6' LIMIT 1`)
	require.NoError(t, err)

	defer rows.Close()

	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		plan += line + "\n"
	}

	require.NoError(t, rows.Err())
	require.Contains(t, plan, "torrents_moderation_identifier_v1_idx")
	require.NotContains(t, plan, "Seq Scan")
}
