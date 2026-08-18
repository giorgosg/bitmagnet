package dbtest_test

import (
	"context"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewProvisionsAMigratedDatabase is a smoke test for the harness itself: if
// this fails, every other integration test is suspect.
func TestNewProvisionsAMigratedDatabase(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	ctx := context.Background()

	t.Run("migrations have been applied", func(t *testing.T) {
		t.Parallel()

		var applied int64

		require.NoError(t,
			db.Gorm.WithContext(ctx).Raw("select count(*) from goose_db_version").Scan(&applied).Error,
		)
		assert.Positive(t, applied, "expected goose to have recorded applied migrations")
	})

	t.Run("core tables exist", func(t *testing.T) {
		t.Parallel()

		for _, table := range []string{"torrents", "torrent_files", "torrent_contents", "queue_jobs"} {
			var exists bool

			require.NoError(t, db.Gorm.WithContext(ctx).Raw(
				"select exists (select 1 from information_schema.tables"+
					" where table_schema = 'public' and table_name = ?)",
				table,
			).Scan(&exists).Error)
			assert.True(t, exists, "table %s should exist after migrating", table)
		}
	})

	t.Run("the dao is usable", func(t *testing.T) {
		t.Parallel()

		count, err := db.Query.QueueJob.WithContext(ctx).Count()
		require.NoError(t, err)
		assert.Zero(t, count, "a freshly migrated database should have no queue jobs")
	})

	t.Run("the pgx pool reaches the same database", func(t *testing.T) {
		t.Parallel()

		var name string

		require.NoError(t, db.Pool.QueryRow(ctx, "select current_database()").Scan(&name))
		assert.Equal(t, db.Name, name)
	})
}

// TestNewIsolatesDatabases guards the property the whole harness rests on: two
// tests must not be able to see each other's writes.
func TestNewIsolatesDatabases(t *testing.T) {
	t.Parallel()

	first, second := dbtest.New(t), dbtest.New(t)
	require.NotEqual(t, first.Name, second.Name)

	ctx := context.Background()

	_, err := first.Pool.Exec(ctx, "create table isolation_probe (id int)")
	require.NoError(t, err)

	var exists bool

	require.NoError(t, second.Pool.QueryRow(ctx,
		"select exists (select 1 from information_schema.tables where table_name = 'isolation_probe')",
	).Scan(&exists))
	assert.False(t, exists, "databases provisioned for different calls must be isolated")
}
