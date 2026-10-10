package search_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/database/query"
	"github.com/bitmagnet-io/bitmagnet/internal/database/search"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/stretchr/testify/require"
)

type slowFacet struct{ query.FacetConfig }

func (slowFacet) Values(query.FacetContext) (map[string]string, error) {
	values := make(map[string]string, 32)

	for i := range 32 {
		key := fmt.Sprintf("value-%02d", i)
		values[key] = key
	}

	return values, nil
}

func (slowFacet) Criteria(filter query.FacetFilter) []query.Criteria {
	if len(filter) == 0 {
		return nil
	}

	return []query.Criteria{query.RawCriteria{Query: "EXISTS (SELECT 1 FROM pg_sleep(0.15))"}}
}

func TestFacetCountsDoNotExhaustTheConnectionPool(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	ctx := t.Context()

	_, err := db.Pool.Exec(ctx, `
		INSERT INTO torrents (info_hash, name, size, private, created_at, updated_at)
		VALUES ($1, 'one', 1, false, now(), now())
	`, []byte("01234567890123456789"))
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, `
		INSERT INTO torrent_contents (info_hash, content_type, created_at, updated_at)
		VALUES ($1, 'ebook', now(), now())
	`, []byte("01234567890123456789"))
	require.NoError(t, err)

	sqlDB, err := db.Gorm.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(40)

	searchService, err := search.New(search.Params{
		Query: lazy.New(func() (*dao.Query, error) { return db.Query, nil }),
	}).Search.Get()
	require.NoError(t, err)

	done := make(chan struct{})
	peak := make(chan int, 1)

	go func() {
		maxInUse := 0
		tick := time.NewTicker(time.Millisecond)

		defer tick.Stop()

		for {
			select {
			case <-done:
				peak <- maxInUse
				return
			case <-tick.C:
				maxInUse = max(maxInUse, sqlDB.Stats().InUse)
			}
		}
	}()

	result, searchErr := searchService.TorrentContent(ctx,
		query.Limit(0),
		query.WithFacet(slowFacet{query.NewFacetConfig(
			query.FacetHasKey("slow"), query.FacetUsesOrLogic(), query.FacetIsAggregated(),
		)}),
	)

	close(done)

	maxInUse := <-peak

	require.NoError(t, searchErr)
	require.Len(t, result.Aggregations["slow"].Items, 32)
	require.LessOrEqual(t, maxInUse, 8, "facet counts must leave connections for other work")
}

var _ query.Facet = slowFacet{}
