package postgres_test

import (
	"fmt"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/database/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newPool(t *testing.T, dsn string) postgres.Result {
	t.Helper()

	result, err := postgres.New(postgres.Params{
		Config: postgres.Config{DSN: dsn, MaxConns: 2, MinConns: 1},
		Logger: zap.NewNop().Sugar(),
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, result.AppHook.OnStop(t.Context()))
	})

	return result
}

func TestPoolConnectsWithStandardConformingStrings(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)

	pool, err := newPool(t, db.DSN).PgxPool.Get()
	require.NoError(t, err)
	require.NoError(t, pool.Ping(t.Context()))
}

// Search renders its own SQL to a string, inlining values with quotes doubled,
// and re-executes it (docs: architecture/data.md). That escaping is only sound
// while backslash is an ordinary character in a string literal. With
// standard_conforming_strings off, `\'` in a search term closes the literal and
// what follows runs as SQL, so the pool must refuse such a database outright.
func TestPoolRefusesStandardConformingStringsOff(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)

	_, err := db.Pool.Exec(t.Context(), fmt.Sprintf(
		"ALTER DATABASE %s SET standard_conforming_strings = off",
		pgx.Identifier{db.Name}.Sanitize(),
	))
	require.NoError(t, err)

	result := newPool(t, db.DSN)

	_, err = result.PgxPool.Get()
	require.ErrorContains(t, err, "standard_conforming_strings")

	_, err = result.SQLDB.Get()
	require.ErrorContains(t, err, "standard_conforming_strings")
}
