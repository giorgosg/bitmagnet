package catalogue_test

import (
	"encoding/json"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/catalogue"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/stretchr/testify/require"
)

func TestVisibilityUsesFrozenDigestVectors(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)

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
		_, err := db.Pool.Exec(
			t.Context(),
			`INSERT INTO moderation_decisions(identifier,status,origin,created_at,
updated_at)
VALUES ($1,'banned','moderator',now(),now())`,
			vector.digest,
		)
		require.NoError(t, err)

		selection := "SELECT " + catalogue.VisibleInfoHash("candidate.info_hash") +
			" FROM (SELECT $1::bytea AS info_hash) AS candidate"

		var visible bool
		require.NoError(
			t,
			db.Pool.QueryRow(t.Context(), selection, hash[:]).
				Scan(&visible),
		)
		require.False(t, visible, "frozen independently computed digest must conceal its raw byte hash")
	}
}

type visibilityPlan struct {
	Plan visibilityPlanNode `json:"Plan"`
}
type visibilityPlanNode struct {
	TotalCost float64 `json:"Total Cost"`
	PlanRows  int     `json:"Plan Rows"`
	NodeType  string  `json:"Node Type"`
}

func TestVisibilityPlansAtFixtureScale(t *testing.T) {
	t.Parallel()
	db := dbtest.NewSeeded(t)
	hashes := dbtest.ModerationCatalogue(t, db)
	_, err := db.Pool.Exec(
		t.Context(),
		`INSERT INTO moderation_decisions(identifier,status,origin,created_at,updated_at)
SELECT encode(sha256(convert_to('magnes:moderation:v1:btih:','UTF8') ||
 decode(lpad(to_hex(i),40,'0'),'hex')),'hex'),'pending','automatic',now(),now()
 FROM generate_series(10,10009) AS i`,
	)
	require.NoError(t, err)
	_, err = db.Pool.Exec(t.Context(), `ANALYZE moderation_decisions`)
	require.NoError(t, err)
	// All candidate hashes and ledger rows are synthetic. Never select or publish
	// names/infohashes from the fixture corpus while measuring plans.
	for _, test := range []struct {
		name, sql string
		args      []any
	}{
		{"direct baseline", `SELECT info_hash FROM torrents WHERE info_hash=$1`, []any{hashes[0][:]}},
		{"direct guarded", `SELECT info_hash FROM torrents WHERE info_hash=$1 AND ` +
			catalogue.VisibleInfoHash("torrents.info_hash"), []any{hashes[0][:]}},
		{"ordered baseline", `SELECT info_hash FROM torrent_contents ORDER BY published_at DESC LIMIT 100`, nil},
		{"ordered guarded", `SELECT info_hash FROM torrent_contents WHERE ` +
			catalogue.VisibleInfoHash("torrent_contents.info_hash") + ` ORDER BY published_at DESC LIMIT 100`, nil},
	} {
		var raw []byte
		require.NoError(
			t,
			db.Pool.QueryRow(t.Context(), "EXPLAIN (FORMAT JSON) "+test.sql, test.args...).Scan(&raw),
		)

		var plans []visibilityPlan
		require.NoError(t, json.Unmarshal(raw, &plans))
		require.Len(t, plans, 1)
		require.Greater(t, plans[0].Plan.TotalCost, 0.0)
		t.Logf(
			"%s: node=%s cost=%.2f rows=%d",
			test.name,
			plans[0].Plan.NodeType,
			plans[0].Plan.TotalCost,
			plans[0].Plan.PlanRows,
		)
	}
}
