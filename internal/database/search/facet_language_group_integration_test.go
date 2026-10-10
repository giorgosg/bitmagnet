package search_test

import (
	"context"
	"database/sql"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/database/query"
	"github.com/bitmagnet-io/bitmagnet/internal/database/search"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type planningQueryCounter struct {
	gorm.ConnPool
	calls atomic.Int64
}

func (c *planningQueryCounter) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	if strings.HasPrefix(query, "EXPLAIN (FORMAT JSON)") {
		c.calls.Add(1)
	}

	return c.ConnPool.QueryRowContext(ctx, query, args...)
}

func TestLanguageFacetGroupsCountsInsteadOfQueryingEveryLanguage(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	ctx := t.Context()

	for _, row := range []struct {
		hash        []byte
		contentType string
		languages   string
	}{
		{[]byte("01234567890123456789"), "ebook", `["en","en","fr"]`},
		{[]byte("abcdefghijabcdefghij"), "tv_show", `["en"]`},
	} {
		_, err := db.Pool.Exec(ctx, `
			INSERT INTO torrents (info_hash, name, size, private, created_at, updated_at)
			VALUES ($1, 'one', 1, false, now(), now())
		`, row.hash)
		require.NoError(t, err)
		_, err = db.Pool.Exec(ctx, `
			INSERT INTO torrent_contents (info_hash, content_type, languages, created_at, updated_at)
			VALUES ($1, $2, $3::jsonb, now(), now())
		`, row.hash, row.contentType, row.languages)
		require.NoError(t, err)
	}

	planQueries := &planningQueryCounter{ConnPool: db.Gorm.Statement.ConnPool}
	gormDB := db.Gorm.Session(&gorm.Session{NewDB: true})
	gormDB.ConnPool = planQueries
	gormDB.Statement.ConnPool = planQueries
	searchService, err := search.New(search.Params{
		Query: lazy.New(func() (*dao.Query, error) { return dao.Use(gormDB), nil }),
	}).Search.Get()
	require.NoError(t, err)

	result, err := searchService.TorrentContent(ctx,
		query.Limit(0),
		query.WithAggregationBudget(5000),
		query.WithFacet(search.TorrentContentLanguageFacet(
			query.FacetIsAggregated(),
			query.FacetHasFilter(query.FacetFilter{"fr": {}}),
		)),
	)
	require.NoError(t, err)
	require.Equal(t, uint(2), result.Aggregations[search.LanguageFacetKey].Items["en"].Count)
	require.Equal(t, uint(1), result.Aggregations[search.LanguageFacetKey].Items["fr"].Count)
	require.LessOrEqual(t, planQueries.calls.Load(), int64(2),
		"language aggregation must not issue one count for every language")

	planQueries.calls.Store(0)

	limited, err := searchService.TorrentContent(ctx,
		query.Limit(0),
		query.WithAggregationBudget(0.01),
		query.WithFacet(search.TorrentContentLanguageFacet(
			query.FacetIsAggregated(),
			query.FacetHasFilter(query.FacetFilter{"fr": {}}),
		)),
	)
	require.NoError(t, err)
	require.Greater(t, planQueries.calls.Load(), int64(2),
		"an over-budget grouped plan must fall back to per-value estimates")
	require.True(t, limited.Aggregations[search.LanguageFacetKey].Items["fr"].IsEstimate)

	filtered, err := searchService.TorrentContent(ctx,
		query.Limit(0),
		query.WithAggregationBudget(5000),
		query.WithFacet(
			search.TorrentContentLanguageFacet(query.FacetIsAggregated()),
			search.TorrentContentTypeFacet(query.FacetHasFilter(query.FacetFilter{"ebook": {}})),
		),
	)
	require.NoError(t, err)
	require.Equal(t, uint(1), filtered.Aggregations[search.LanguageFacetKey].Items["en"].Count,
		"other facet filters must apply to the grouped base query")
}

func TestLanguageFacetUsesGroupedCountsOnSeededCorpus(t *testing.T) {
	t.Parallel()

	db := dbtest.NewSeeded(t)
	planQueries := &planningQueryCounter{ConnPool: db.Gorm.Statement.ConnPool}
	gormDB := db.Gorm.Session(&gorm.Session{NewDB: true})
	gormDB.ConnPool = planQueries
	gormDB.Statement.ConnPool = planQueries
	searchService, err := search.New(search.Params{
		Query: lazy.New(func() (*dao.Query, error) { return dao.Use(gormDB), nil }),
	}).Search.Get()
	require.NoError(t, err)

	result, err := searchService.TorrentContent(t.Context(),
		query.Limit(0),
		query.WithAggregationBudget(5000),
		query.WithFacet(search.TorrentContentLanguageFacet(query.FacetIsAggregated())),
	)
	require.NoError(t, err)
	require.NotEmpty(t, result.Aggregations[search.LanguageFacetKey].Items)
	require.LessOrEqual(t, planQueries.calls.Load(), int64(2),
		"a realistic corpus should use the grouped plan rather than exhausting the pool")
}
