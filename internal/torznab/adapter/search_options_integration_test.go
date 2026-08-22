package adapter

import (
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/database/search"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/torznab"
	"github.com/stretchr/testify/require"
)

func TestSearchIncludesBooksAlongsideOtherCategories(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	ctx := t.Context()

	for _, torrent := range []struct {
		infoHash    []byte
		name        string
		contentType string
	}{
		{[]byte("01234567890123456789"), "A TV show", "tv_show"},
		{[]byte("abcdefghijabcdefghij"), "An ebook", "ebook"},
		{[]byte("zyxwvutsrqzyxwvutsrq"), "A comic", "comic"},
	} {
		_, err := db.Pool.Exec(ctx, `
			INSERT INTO torrents (info_hash, name, size, private, created_at, updated_at)
			VALUES ($1, $2, 1, false, now(), now())
		`, torrent.infoHash, torrent.name)
		require.NoError(t, err)

		_, err = db.Pool.Exec(ctx, `
			INSERT INTO torrent_contents (info_hash, content_type, created_at, updated_at)
			VALUES ($1, $2, now(), now())
		`, torrent.infoHash, torrent.contentType)
		require.NoError(t, err)
	}

	searchService, err := search.New(search.Params{
		Query: lazy.New(func() (*dao.Query, error) { return db.Query, nil }),
	}).Search.Get()
	require.NoError(t, err)

	result, err := New(searchService).Search(ctx, torznab.SearchRequest{
		Profile: torznab.ProfileDefault,
		Type:    torznab.FunctionSearch,
		Cats:    []int{torznab.CategoryTV.ID, torznab.CategoryBooks.ID},
	})
	require.NoError(t, err)
	require.Len(t, result.Channel.Items, 3)
	require.ElementsMatch(t, []string{"A TV show", "An ebook", "A comic"}, []string{
		result.Channel.Items[0].Title,
		result.Channel.Items[1].Title,
		result.Channel.Items[2].Title,
	})

	ebookResult, err := New(searchService).Search(ctx, torznab.SearchRequest{
		Profile: torznab.ProfileDefault,
		Type:    torznab.FunctionSearch,
		Cats:    []int{torznab.CategoryBooksEBook.ID},
	})
	require.NoError(t, err)
	require.Len(t, ebookResult.Channel.Items, 1)
	require.Equal(t, "An ebook", ebookResult.Channel.Items[0].Title)
}

func TestSearchIgnoresQueryWhenTMDBIDProvided(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	ctx := t.Context()
	infoHash := []byte("01234567890123456789")

	_, err := db.Pool.Exec(ctx, `
		INSERT INTO torrents (info_hash, name, size, private, created_at, updated_at)
		VALUES ($1, 'The matching torrent', 1, false, now(), now())
	`, infoHash)
	require.NoError(t, err)

	_, err = db.Pool.Exec(ctx, `
		INSERT INTO content (type, source, id, title, created_at, updated_at)
		VALUES ('movie', 'tmdb', '42', 'The matching movie', now(), now())
	`)
	require.NoError(t, err)

	_, err = db.Pool.Exec(ctx, `
		INSERT INTO torrent_contents (
			info_hash, content_type, content_source, content_id, created_at, updated_at
		)
		VALUES ($1, 'movie', 'tmdb', '42', now(), now())
	`, infoHash)
	require.NoError(t, err)

	searchService, err := search.New(search.Params{
		Query: lazy.New(func() (*dao.Query, error) { return db.Query, nil }),
	}).Search.Get()
	require.NoError(t, err)

	result, err := New(searchService).Search(ctx, torznab.SearchRequest{
		Profile: torznab.ProfileDefault,
		Query:   "not a matching title",
		Type:    torznab.FunctionMovie,
		TMDBID:  model.NewNullString("42"),
	})
	require.NoError(t, err)
	require.Len(t, result.Channel.Items, 1)
	require.Equal(t, "The matching torrent", result.Channel.Items[0].Title)
}
