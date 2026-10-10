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

func TestPoolConnectsWithStandardConformingStringsOff(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)

	_, err := db.Pool.Exec(t.Context(), fmt.Sprintf(
		"ALTER DATABASE %s SET standard_conforming_strings = off",
		pgx.Identifier{db.Name}.Sanitize(),
	))
	require.NoError(t, err)

	result := newPool(t, db.DSN)

	pool, err := result.PgxPool.Get()
	require.NoError(t, err)
	require.NoError(t, pool.Ping(t.Context()))

	sqlDB, err := result.SQLDB.Get()
	require.NoError(t, err)
	require.NoError(t, sqlDB.PingContext(t.Context()))
}
