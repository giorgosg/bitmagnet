package search_test

import (
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/database/query"
	"github.com/bitmagnet-io/bitmagnet/internal/database/search"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/stretchr/testify/require"
)

// The content type facet builds its column by hand rather than by converting the
// generated DAO field, because gorm.io/gen made those types generic and no longer
// inter-convertible. A wrong table or column name there would still compile, so
// this exercises the filter against a real database.
func TestTorrentContentTypeFacetFiltersByContentType(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	ctx := t.Context()

	for _, tc := range []struct {
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
		`, tc.infoHash, tc.name)
		require.NoError(t, err)

		_, err = db.Pool.Exec(ctx, `
			INSERT INTO torrent_contents (info_hash, content_type, created_at, updated_at)
			VALUES ($1, $2, now(), now())
		`, tc.infoHash, tc.contentType)
		require.NoError(t, err)
	}

	searchService, err := search.New(search.Params{
		Query: lazy.New(func() (*dao.Query, error) { return db.Query, nil }),
	}).Search.Get()
	require.NoError(t, err)

	result, err := searchService.TorrentContent(ctx,
		query.WithFacet(search.TorrentContentTypeFacet(
			query.FacetHasFilter(query.FacetFilter{"ebook": struct{}{}}),
		)),
	)
	require.NoError(t, err)

	hashes := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		hashes = append(hashes, string(item.InfoHash[:]))
	}

	require.Equal(t, []string{"abcdefghijabcdefghij"}, hashes)
}
