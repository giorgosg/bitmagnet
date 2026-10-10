package search

import (
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/query"
	"gorm.io/gorm"
)

// groupFacetCounts applies the same base filters as a per-value count, omitting
// only the current OR facet's own filter. A grouped plan over budget leaves the
// caller on the existing estimate-capable per-value path.
func groupFacetCounts(
	ctx query.FacetContext,
	facet query.Facet,
	valueCount int,
	baseSelection, from, groupKey string,
) (map[string]uint, bool, error) {
	sq, err := ctx.NewAggregationQueryForFacet(facet.Key(), facet.AggregationOption)
	if err != nil {
		return nil, false, err
	}

	base := sq.UnderlyingDB().Select(baseSelection)
	grouped := ctx.Query().TorrentContent.WithContext(ctx.Context()).ReadDB().UnderlyingDB().
		Session(&gorm.Session{NewDB: true}).
		WithContext(ctx.Context()).
		Table(from, base).
		Select("COALESCE(" + groupKey + ", 'null') AS key, COUNT(*) AS count").
		Group(groupKey)

	// The configured budget applies to each of the value counts this replaces.
	// Preserve their combined ceiling, while making one grouped pass preferable
	// to many individually affordable scans. A much larger dataset still falls
	// back to per-value estimates.
	withinBudget, err := dao.WithinCostBudget(grouped, ctx.AggregationBudget()*float64(valueCount))
	if err != nil || !withinBudget {
		return nil, withinBudget, err
	}

	var rows []struct {
		Key   string
		Count uint
	}
	if err = grouped.Scan(&rows).Error; err != nil {
		return nil, false, err
	}

	counts := make(map[string]uint, len(rows))
	for _, row := range rows {
		counts[row.Key] = row.Count
	}

	return counts, true, nil
}
