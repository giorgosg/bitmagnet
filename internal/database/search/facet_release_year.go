package search

import (
	"fmt"
	"strconv"

	"github.com/bitmagnet-io/bitmagnet/internal/database/catalogue"
	"github.com/bitmagnet-io/bitmagnet/internal/database/query"
	"github.com/bitmagnet-io/bitmagnet/internal/maps"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
)

const ReleaseYearFacetKey = "release_year"

func ReleaseYearFacet(options ...query.FacetOption) query.Facet {
	return yearFacet{
		FacetConfig: query.NewFacetConfig(
			append([]query.FacetOption{
				query.FacetHasKey(ReleaseYearFacetKey),
				query.FacetHasLabel("Release Year"),
				query.FacetUsesOrLogic(),
				// avoids counting different versions of the same piece of content,
				// but has issues when combined with other filters:
				// query.FacetHasAggregationOption(
				// 	query.Table(model.TableNameContent),
				// 	ContentCoreJoins(),
				// ),
			}, options...)...,
		),
		field: "release_year",
	}
}

type yearFacet struct {
	query.FacetConfig
	field string
}

func (yearFacet) Criteria(filter query.FacetFilter) []query.Criteria {
	return []query.Criteria{
		query.GenCriteria(func(ctx query.DBContext) (query.Criteria, error) {
			years := make([]uint16, 0, len(filter))
			hasNull := false

			for _, v := range filter.Values() {
				if v == "null" {
					hasNull = true
					continue
				}

				strYear := v

				vInt, intErr := strconv.Atoi(strYear)
				if intErr != nil {
					return nil, fmt.Errorf("invalid year filter specified: %w", intErr)
				} else if vInt < 1000 || vInt > 9999 {
					return nil, fmt.Errorf("out-of-bounds year filter specified: %s", strYear)
				}

				years = append(years, uint16(vInt))
			}

			yearField := "COALESCE(content.release_year, torrent_contents.inferred_year)"
			joins := maps.NewInsertMap(maps.MapEntry[string, struct{}]{Key: model.TableNameContent})

			var or []query.Criteria
			if len(years) > 0 {
				or = append(or, query.RawCriteria{
					Query: ctx.Query().
						Content.
						UnderlyingDB().
						Where(yearField+" IN ?", years),
					Joins: joins,
				})
			}

			if hasNull {
				or = append(or, query.RawCriteria{
					Query: ctx.Query().Content.UnderlyingDB().Where(yearField + " IS NULL"),
					Joins: joins,
				})
			}

			return query.Or(or...), nil
		}),
	}
}

func (yearFacet) Values(ctx query.FacetContext) (map[string]string, error) {
	var years []model.Year

	// Keep the catalogue lookup separate from the sparse inferred-year index: a
	// coalesced DISTINCT over the joined torrent index would scan every release.
	err := ctx.Query().UnderlyingDB().WithContext(catalogue.Context(ctx.Context())).Raw(`
 SELECT DISTINCT content.release_year AS year FROM content
 WHERE content.release_year BETWEEN 1000 AND 9999 AND ` + catalogue.VisibleContent("content") + `
 UNION
 SELECT DISTINCT torrent_contents.inferred_year AS year FROM torrent_contents
 WHERE torrent_contents.inferred_year BETWEEN 1000 AND 9999
 AND ` + catalogue.VisibleInfoHash("torrent_contents.info_hash") + `
 AND NOT EXISTS (SELECT 1 FROM content
 WHERE content.type=torrent_contents.content_type
 AND content.source=torrent_contents.content_source AND content.id=torrent_contents.content_id
 AND content.release_year IS NOT NULL)`).Scan(&years).Error
	if err != nil {
		return nil, err
	}

	values := make(map[string]string, len(years)+1)
	values["null"] = "Unknown"

	for _, y := range years {
		values[y.String()] = y.String()
	}

	return values, nil
}
