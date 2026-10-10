package search_test

import (
	"database/sql"
	"net/url"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/database/exclause"
	"github.com/bitmagnet-io/bitmagnet/internal/database/query"
	"github.com/bitmagnet-io/bitmagnet/internal/database/search"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Search keeps user values bound in the count and item paths, including the
// budgeted EXPLAIN and the CTE strategy.
//
// Every payload goes through the search string, which passes the tsquery lexer
// first, and through tag names and a tag facet filter, which do not. Two
// torrents exist and none of the payloads matches either, so a payload that
// defeats a predicate shows up as a count or an item that moved, and one that
// smuggles in a statement leaves the canary table behind.
//
// A NUL byte is not here: the protocol refuses it before the server parses
// anything ("invalid byte sequence for encoding UTF8: 0x00").
func TestSearchInputsCannotEscapeTheirLiterals(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	ctx := t.Context()

	for _, row := range []struct{ infoHash, name string }{
		{"01234567890123456789", "Ubuntu install image"},
		{"abcdefghijabcdefghij", "Debian install image"},
	} {
		_, err := db.Pool.Exec(ctx, `
			INSERT INTO torrents (info_hash, name, size, private, created_at, updated_at)
			VALUES ($1, $2, 1, false, now(), now())
		`, []byte(row.infoHash), row.name)
		require.NoError(t, err)

		_, err = db.Pool.Exec(ctx, `
			INSERT INTO torrent_contents (info_hash, content_type, created_at, updated_at)
			VALUES ($1, 'software', now(), now())
		`, []byte(row.infoHash))
		require.NoError(t, err)

		_, err = db.Pool.Exec(ctx, `
			INSERT INTO torrent_tags (info_hash, name, created_at, updated_at)
			VALUES ($1, 'linux', now(), now())
		`, []byte(row.infoHash))
		require.NoError(t, err)
	}

	searchService, err := search.New(search.Params{
		Query: lazy.New(func() (*dao.Query, error) { return db.Query, nil }),
	}).Search.Get()
	require.NoError(t, err)

	const canary = "injection_canary"

	payloads := []string{
		`'`,
		`''`,
		`\'`,
		`\\'`,
		`'; CREATE TABLE ` + canary + ` (i int); --`,
		`\') ; CREATE TABLE ` + canary + ` (i int) --`,
		`\') UNION SELECT info_hash FROM torrents --`,
		`' OR 'x'='x`,
		`\' OR 1=1 --`,
		`')) OR true --`,
		`linux' OR name <> '`,
		`-- line comment`,
		`/* block */ comment`,
		`$$ OR true $$`,
		`$tag$ OR true $tag$`,
		`E'\x27 OR true --`,
		`U&'\0027 OR true --`,
		"new\nline' OR true --",
	}

	inputs := map[string]func(string) query.Option{
		// Anchored to a term nothing matches. The lexer strips a payload made
		// only of punctuation, and an empty search rightly matches everything;
		// with the anchor, the only way to a non-zero count is a broken literal.
		"search string": func(p string) query.Option {
			return query.SearchString("nomatchanchor " + p)
		},
		"tag criteria": func(p string) query.Option {
			return query.Where(search.TorrentTagCriteria(p))
		},
		"tag facet filter": func(p string) query.Option {
			return query.WithFacet(search.TorrentTagsFacet(
				query.FacetHasFilter(query.FacetFilter{p: struct{}{}}),
			))
		},
	}

	budgets := map[string]float64{
		"exact count":    0,
		"budgeted count": 5_000,
	}

	for inputName, input := range inputs {
		for budgetName, budget := range budgets {
			for _, payload := range payloads {
				result, searchErr := searchService.TorrentContent(ctx,
					search.TorrentContentDefaultOption(),
					input(payload),
					query.WithTotalCount(true),
					query.WithHasNextPage(true),
					query.WithAggregationBudget(budget),
					query.Limit(10),
				)

				require.NoError(t, searchErr, "%s, %s: %q", inputName, budgetName, payload)
				assert.Zero(t, result.TotalCount, "%s, %s: %q", inputName, budgetName, payload)
				assert.Empty(t, result.Items, "%s, %s: %q", inputName, budgetName, payload)
			}
		}
	}

	var leftBehind *string
	require.NoError(t, db.Pool.QueryRow(ctx, "SELECT to_regclass($1)::text", canary).Scan(&leftBehind))
	assert.Nil(t, leftBehind, "a payload created a table")

	// The rows are there to be miscounted: an honest query for them finds both,
	// so a zero above means the predicate held rather than that nothing exists.
	honest, err := searchService.TorrentContent(ctx,
		search.TorrentContentDefaultOption(),
		query.Where(search.TorrentTagCriteria("linux")),
		query.WithTotalCount(true),
	)
	require.NoError(t, err)
	assert.Equal(t, uint(2), honest.TotalCount)
}

func TestSearchInputsStayBoundWithNonconformingStrings(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	ctx := t.Context()
	_, err := db.Pool.Exec(ctx, `
		INSERT INTO torrents (info_hash, name, size, private, created_at, updated_at)
		VALUES ('01234567890123456789', 'Ubuntu install image', 1, false, now(), now())
	`)
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, `
		INSERT INTO torrent_contents (info_hash, content_type, created_at, updated_at)
		VALUES ('01234567890123456789', 'software', now(), now())
	`)
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, `
		INSERT INTO torrent_tags (info_hash, name, created_at, updated_at)
		VALUES ('01234567890123456789', 'linux', now(), now())
	`)
	require.NoError(t, err)

	uri, err := url.Parse(db.DSN)
	require.NoError(t, err)

	params := uri.Query()
	params.Set("options", "-c standard_conforming_strings=off")
	uri.RawQuery = params.Encode()
	sqlDB, err := sql.Open("pgx", uri.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, sqlDB.PingContext(ctx))

	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}),
		&gorm.Config{DisableAutomaticPing: true, Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, gormDB.Use(exclause.New()))
	searchService, err := search.New(search.Params{
		Query: lazy.New(func() (*dao.Query, error) { return dao.Use(gormDB), nil }),
	}).Search.Get()
	require.NoError(t, err)

	for _, budget := range []float64{0, 5000} {
		for _, input := range []query.Option{
			query.SearchString(`nomatchanchor \'`),
			query.Where(search.TorrentTagCriteria(`\'`)),
			query.WithFacet(search.TorrentTagsFacet(
				query.FacetHasFilter(query.FacetFilter{`\'`: {}}))),
		} {
			result, searchErr := searchService.TorrentContent(ctx,
				search.TorrentContentDefaultOption(), input,
				query.WithTotalCount(true), query.WithHasNextPage(true),
				query.WithAggregationBudget(budget), query.Limit(10),
			)
			require.NoError(t, searchErr, "budget %g", budget)
			assert.Zero(t, result.TotalCount, "budget %g", budget)
			assert.Empty(t, result.Items, "budget %g", budget)
		}
	}
}
