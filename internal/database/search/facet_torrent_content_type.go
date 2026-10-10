package search

import (
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/query"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"gorm.io/gen/field"
)

const TorrentContentTypeFacetKey = "content_type"

func TorrentContentTypeFacet(options ...query.FacetOption) query.Facet {
	return torrentContentTypeFacet{
		torrentContentAttributeFacet[model.ContentType]{
			FacetConfig: query.NewFacetConfig(
				append([]query.FacetOption{
					query.FacetHasKey(TorrentContentTypeFacetKey),
					query.FacetHasLabel("Content Type"),
					query.FacetUsesOrLogic(),
				}, options...)...,
			),
			field: func(q *dao.Query) field.Field {
				return field.NewField(
					model.TableNameTorrentContent,
					q.TorrentContent.ContentType.ColumnName().String(),
				)
			},
			parse: model.ParseContentType,
		},
	}
}

type torrentContentTypeFacet struct {
	torrentContentAttributeFacet[model.ContentType]
}

func (f torrentContentTypeFacet) GroupedCounts(ctx query.FacetContext, valueCount int) (map[string]uint, bool, error) {
	return groupFacetCounts(ctx, f, valueCount,
		"torrent_contents.content_type::text AS key",
		"(?) AS facet_rows",
		"facet_rows.key",
	)
}

func (torrentContentTypeFacet) Values(query.FacetContext) (map[string]string, error) {
	values := make(map[string]string)
	values["null"] = "Unknown"

	for _, contentType := range model.ContentTypeValues() {
		values[string(contentType)] = contentType.Label()
	}

	return values, nil
}
