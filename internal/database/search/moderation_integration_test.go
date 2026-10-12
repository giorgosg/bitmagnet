package search_test

import (
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/cache"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/database/query"
	"github.com/bitmagnet-io/bitmagnet/internal/database/search"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestModerationCatalogueSearch(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	hashes := dbtest.ModerationCatalogue(t, db)
	s, err := search.New(
		search.Params{Query: lazy.New(func() (*dao.Query, error) { return db.Query, nil })},
	).Search.Get()
	require.NoError(t, err)
	result, err := s.Torrents(t.Context(), query.WithTotalCount(true))
	require.NoError(t, err)

	got := make([]protocol.ID, 0, len(result.Items))
	for _, item := range result.Items {
		got = append(got, item.InfoHash)
	}

	require.ElementsMatch(t, []protocol.ID{hashes[0], hashes[2]}, got)
	require.Equal(t, uint(2), result.TotalCount)
	direct, err := s.TorrentsWithMissingInfoHashes(t.Context(), hashes)
	require.NoError(t, err)
	require.ElementsMatch(t, []protocol.ID{hashes[1], hashes[3]}, direct.MissingInfoHashes)
	record, err := moderation.NewService(db.Gorm).Get(t.Context(), moderation.IdentifierForInfoHash(hashes[1]))
	require.NoError(t, err)
	require.Equal(t, []string{"Pending/harmless.txt"}, record.Evidence.Files)
}

func TestModerationContentFilesAndTags(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	hashes := dbtest.ModerationCatalogue(t, db)
	s, err := search.New(
		search.Params{Query: lazy.New(func() (*dao.Query, error) { return db.Query, nil })},
	).Search.Get()
	require.NoError(t, err)
	t.Run("content and facets", func(t *testing.T) {
		t.Parallel()
		result, err := s.TorrentContent(
			t.Context(),
			search.TorrentContentCoreJoins(),
			query.WithTotalCount(true),
			query.WithFacet(
				search.TorrentTagsFacet(query.FacetIsAggregated()),
				search.TorrentContentTypeFacet(query.FacetIsAggregated()),
			),
		)
		require.NoError(t, err)

		got := make([]protocol.ID, 0, len(result.Items))
		for _, item := range result.Items {
			got = append(got, item.InfoHash)
		}

		require.ElementsMatch(t, []protocol.ID{hashes[0], hashes[2]}, got)
		require.Equal(t, uint(2), result.TotalCount)
		require.NotContains(t, result.Aggregations[search.TorrentTagFacetKey].Items, "pending")
		require.NotContains(t, result.Aggregations[search.TorrentTagFacetKey].Items, "banned")
		require.Equal(t, uint(2), result.Aggregations[search.TorrentContentTypeFacetKey].Items["ebook"].Count)
	})
	t.Run("files", func(t *testing.T) {
		t.Parallel()
		result, err := s.TorrentFiles(t.Context(), query.Where(search.TorrentFileInfoHashCriteria(hashes...)))
		require.NoError(t, err)

		got := make([]string, 0, len(result.Items))
		for _, item := range result.Items {
			got = append(got, item.Path)
		}

		require.ElementsMatch(t, []string{"Visible/harmless.txt", "Allowed/harmless.txt"}, got)
	})
	t.Run("suggestions", func(t *testing.T) {
		t.Parallel()
		result, err := s.TorrentSuggestTags(t.Context(), search.SuggestTagsQuery{})
		require.NoError(t, err)

		got := make([]string, 0, len(result.Suggestions))
		for _, item := range result.Suggestions {
			got = append(got, item.Name)
		}

		require.ElementsMatch(t, []string{"visible", "allowed"}, got)
	})
}

func TestQuarantineCannotReturnCachedCatalogue(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	hashes := dbtest.ModerationCatalogue(t, db)
	cacher := cache.NewInMemoryCacher(
		cache.Params{Config: cache.NewDefaultConfig(), Logger: zap.NewNop().Sugar()},
	).Cacher
	require.NoError(
		t,
		db.Gorm.Use(
			cache.NewPlugin(cache.PluginParams{Config: cache.NewDefaultConfig(), Cacher: cacher}).Plugin,
		),
	)

	s, err := search.New(
		search.Params{Query: lazy.New(func() (*dao.Query, error) { return db.Query, nil })},
	).Search.Get()
	require.NoError(t, err)

	options := []query.Option{query.Cached(), query.Where(search.TorrentInfoHashCriteria(hashes[0]))}
	first, err := s.Torrents(t.Context(), options...)
	require.NoError(t, err)
	require.Len(t, first.Items, 1)
	require.NoError(
		t,
		moderation.NewService(db.Gorm).
			Flag(t.Context(), moderation.IdentifierForInfoHash(hashes[0]), moderation.Evidence{}, "reviewer"),
	)
	second, err := s.Torrents(t.Context(), options...)
	require.NoError(t, err)
	require.Empty(t, second.Items, "a cached pre-quarantine row must not escape")
}

func TestModerationMetadataAndDynamicFacetValues(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	hashes := dbtest.ModerationCatalogue(t, db)
	result := search.New(search.Params{Query: lazy.New(func() (*dao.Query, error) { return db.Query, nil })})
	s, err := result.Search.Get()
	require.NoError(t, err)
	{
		rows, err := s.Content(t.Context())
		require.NoError(t, err)

		titles := make([]string, 0, len(rows.Items))
		for _, item := range rows.Items {
			titles = append(titles, item.Title)
		}

		require.ElementsMatch(t, []string{"Visible book", "Allowed book"}, titles)
	}
	{
		rows, err := s.TorrentContent(
			t.Context(),
			search.TorrentContentCoreJoins(),
			query.Limit(0),
			query.WithFacet(
				search.TorrentSourceFacet(
					query.FacetIsAggregated(),
					query.FacetHasFilter(query.FacetFilter{"pending": {}}),
				),
				search.TorrentContentGenreFacet(
					query.FacetIsAggregated(),
					query.FacetHasFilter(query.FacetFilter{"tmdb:banned": {}}),
				),
				search.ReleaseYearFacet(
					query.FacetIsAggregated(),
					query.FacetHasFilter(query.FacetFilter{"2001": {}}),
				),
			),
		)
		require.NoError(t, err)
		require.NotContains(t, rows.Aggregations[search.TorrentSourceFacetKey].Items, "pending")
		require.NotContains(t, rows.Aggregations[search.ContentGenreFacetKey].Items, "tmdb:banned")
		require.NotContains(t, rows.Aggregations[search.ReleaseYearFacetKey].Items, "2001")
	}
	// A hidden torrent can share a visible content record. Its association must not
	// cause the shared metadata to disappear, or contribute to ordinary counts.
	_, err = db.Pool.Exec(
		t.Context(),
		`UPDATE torrent_contents SET content_id='visible' WHERE info_hash=$1`,
		hashes[1][:],
	)
	require.NoError(t, err)
	rows, err := s.Content(
		t.Context(),
		query.Where(
			search.ContentCanonicalIdentifierCriteria(
				model.ContentRef{Type: model.ContentTypeEbook, Source: "tmdb", ID: "visible"},
			),
		),
	)
	require.NoError(t, err)
	require.Len(t, rows.Items, 1)
	joined, err := s.Content(
		t.Context(),
		search.ContentDefaultOption(),
		query.RequireJoin(model.TableNameTorrentContent),
		query.WithTotalCount(true),
	)
	require.NoError(t, err)
	require.Equal(t, uint(2), joined.TotalCount, "hidden backing associations must not inflate metadata counts")
}

func TestUnassociatedMetadataKeepsBaselineVisibility(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	_, err := db.Pool.Exec(t.Context(), `INSERT INTO content (type,source,id,title,created_at,updated_at)
 VALUES ('ebook','tmdb','unassociated','Unassociated book',now(),now())`)
	require.NoError(t, err)
	_, err = db.Pool.Exec(t.Context(), `INSERT INTO content_collections (type,source,id,name,created_at,updated_at)
 VALUES ('genre','tmdb','unassociated','Unassociated genre',now(),now())`)
	require.NoError(t, err)
	s, err := search.New(
		search.Params{Query: lazy.New(func() (*dao.Query, error) { return db.Query, nil })},
	).Search.Get()
	require.NoError(t, err)
	result, err := s.Content(t.Context())
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.Equal(t, "Unassociated book", result.Items[0].Title)
	facets, err := s.TorrentContent(t.Context(), search.TorrentContentCoreJoins(), query.Limit(0), query.WithFacet(
		search.TorrentContentGenreFacet(
			query.FacetIsAggregated(),
			query.FacetHasFilter(query.FacetFilter{"tmdb:unassociated": {}}),
		),
	))
	require.NoError(t, err)
	require.Contains(t, facets.Aggregations[search.ContentGenreFacetKey].Items, "tmdb:unassociated")
}
