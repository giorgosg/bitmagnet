package search_test

import (
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/database/query"
	"github.com/bitmagnet-io/bitmagnet/internal/database/search"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/stretchr/testify/require"
)

func TestInferredMetadataSearchAndYearFallback(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	hash := protocol.ID{91}
	torrent := model.Torrent{
		InfoHash:    hash,
		Name:        "opaque.archive",
		FilesStatus: model.FilesStatusSingle,
	}
	require.NoError(t, db.Gorm.Create(&torrent).Error)
	tc := model.TorrentContent{
		InfoHash:      hash,
		Torrent:       torrent,
		InferredTitle: "Hidden Orchard",
		InferredYear:  1998,
	}
	tc.UpdateTsv()
	require.NoError(t, db.Gorm.Omit("Torrent", "Content").Create(&tc).Error)

	s, err := search.New(
		search.Params{Query: lazy.New(func() (*dao.Query, error) { return db.Query, nil })},
	).Search.Get()
	require.NoError(t, err)
	rows, err := s.TorrentContent(
		t.Context(),
		search.TorrentContentDefaultOption(),
		query.SearchString("Hidden Orchard"),
		query.WithFacet(
			search.ReleaseYearFacet(
				query.FacetIsAggregated(),
				query.FacetHasFilter(query.FacetFilter{"1998": {}}),
			),
		),
	)
	require.NoError(t, err)
	require.Len(t, rows.Items, 1)
	require.Equal(t, "Hidden Orchard (1998)", rows.Items[0].Title())
	require.Equal(t, uint(1), rows.Aggregations[search.ReleaseYearFacetKey].Items["1998"].Count)
	require.Equal(t, "opaque.archive", rows.Items[0].Torrent.Name)
	// Catalogue identity wins over inferred values even when both are present.
	content := model.Content{
		Type:        model.ContentTypeMovie,
		Source:      model.SourceTmdb,
		ID:          "101",
		Title:       "Catalogue Orchard",
		ReleaseYear: 2001,
		ReleaseDate: model.NewDateFromParts(2001, 1, 1),
	}
	require.NoError(t, db.Gorm.Create(&content).Error)
	require.NoError(
		t,
		db.Gorm.Model(&model.TorrentContent{}).
			Where("info_hash = ?", hash).
			Updates(map[string]any{
				"content_type":   model.ContentTypeMovie,
				"content_source": model.SourceTmdb,
				"content_id":     "101",
			}).
			Error,
	)
	rows, err = s.TorrentContent(
		t.Context(),
		search.TorrentContentDefaultOption(),
		query.WithFacet(
			search.ReleaseYearFacet(
				query.FacetIsAggregated(),
				query.FacetHasFilter(query.FacetFilter{"2001": {}}),
			),
		),
	)
	require.NoError(t, err)
	require.Len(t, rows.Items, 1)
	require.Equal(t, "Catalogue Orchard (2001)", rows.Items[0].Title())
}
