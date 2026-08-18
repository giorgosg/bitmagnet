package adapter

import (
	"strconv"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/search"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/torznab"
	"github.com/stretchr/testify/require"
)

func TestTorrentContentResultUsesResolutionCategory(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name        string
		contentType model.ContentType
		resolution  model.VideoResolution
		categoryID  int
	}{
		{"TV SD", model.ContentTypeTvShow, model.VideoResolutionV480p, torznab.CategoryTVSD.ID},
		{"TV HD", model.ContentTypeTvShow, model.VideoResolutionV1080p, torznab.CategoryTVHD.ID},
		{"TV UHD", model.ContentTypeTvShow, model.VideoResolutionV2160p, torznab.CategoryTVUHD.ID},
		{"movie SD", model.ContentTypeMovie, model.VideoResolutionV480p, torznab.CategoryMoviesSD.ID},
		{"movie HD", model.ContentTypeMovie, model.VideoResolutionV1080p, torznab.CategoryMoviesHD.ID},
		{"movie UHD", model.ContentTypeMovie, model.VideoResolutionV2160p, torznab.CategoryMoviesUHD.ID},
		{"ebook", model.ContentTypeEbook, "", torznab.CategoryBooksEBook.ID},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			videoResolution := model.NullVideoResolution{}
			if tt.resolution != "" {
				videoResolution = model.NewNullVideoResolution(tt.resolution)
			}

			item := torrentContentResultItemToTorznabResultItem(search.TorrentContentResultItem{
				TorrentContent: model.TorrentContent{
					ContentType:     model.NewNullContentType(tt.contentType),
					VideoResolution: videoResolution,
				},
			})

			require.Equal(t, strconv.Itoa(tt.categoryID), torznabAttribute(item, torznab.AttrCategory))
		})
	}
}

func TestTorrentContentResultIncludesTVDBID(t *testing.T) {
	t.Parallel()

	item := torrentContentResultItemToTorznabResultItem(search.TorrentContentResultItem{
		TorrentContent: model.TorrentContent{
			Content: model.Content{
				Attributes: []model.ContentAttribute{{
					Source: "tvdb",
					Key:    "id",
					Value:  "12345",
				}},
			},
		},
	})

	require.Equal(t, "12345", torznabAttribute(item, torznab.AttrTvdb))
}

func torznabAttribute(item torznab.SearchResultItem, name string) string {
	for _, attr := range item.TorznabAttrs {
		if attr.AttrName == name {
			return attr.AttrValue
		}
	}

	return ""
}
