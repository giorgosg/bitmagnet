package classifier

import (
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/database/search"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/stretchr/testify/require"
)

func TestLocalClassificationCanReuseUnlistedMetadata(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	hashes := dbtest.ModerationCatalogue(t, db)
	_, err := db.Pool.Exec(
		t.Context(),
		`INSERT INTO content (type,source,id,title,tsv,created_at,
updated_at)
VALUES ('movie','tmdb','synthetic-42','Harmless Library',to_tsvector('simple','Harmless Library'),now(),now())`,
	)
	require.NoError(t, err)
	_, err = db.Pool.Exec(
		t.Context(),
		`UPDATE torrent_contents
SET content_type='movie',content_source='tmdb',content_id='synthetic-42'
WHERE info_hash=$1`,
		hashes[3][:],
	)
	require.NoError(t, err)

	result := search.New(search.Params{Query: lazy.New(func() (*dao.Query, error) { return db.Query, nil })})
	s, err := result.MetadataSearch.Get()
	require.NoError(t, err)

	lookup := localSearch{s}
	byID, err := lookup.ContentByID(
		t.Context(),
		model.ContentRef{Type: model.ContentTypeMovie, Source: "tmdb", ID: "synthetic-42"},
	)
	require.NoError(t, err)
	require.Equal(t, "Harmless Library", byID.Title)
	byTitle, err := lookup.ContentBySearch(t.Context(), model.ContentTypeMovie, "Harmless Library", 0)
	require.NoError(t, err)
	require.Equal(t, "synthetic-42", byTitle.ID)
}
