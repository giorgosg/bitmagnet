package dbtest_test

import (
	"database/sql"
	"os"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A caller with no *testing.T -- the dev fixture command -- has to tell "the
// fixtures were never built here" from "the fixture setup is broken", because it
// says different things about each. An instance that is up but carries no
// template is the first case.
func TestOpenSeededReportsAnAbsentTemplate(t *testing.T) {
	t.Parallel()

	adminDSN := os.Getenv(dbtest.DSNEnvVar)
	if adminDSN == "" {
		t.Skipf("%s is not set; skipping database integration test", dbtest.DSNEnvVar)
	}

	// The empty-database instance deliberately: it is reachable and has no
	// btm_seed_* database on it, which is exactly the case under test.
	_, err := dbtest.OpenSeeded(t.Context(), adminDSN)
	require.Error(t, err)
	assert.ErrorIs(t, err, dbtest.ErrNoFixtures)
}

// An unreachable instance is a broken setup, not an absent fixture, and must not
// be mistaken for one.
func TestOpenSeededDoesNotCallAnUnreachableInstanceAnAbsentFixture(t *testing.T) {
	t.Parallel()

	_, err := dbtest.OpenSeeded(t.Context(), "postgres://postgres:postgres@127.0.0.1:1/postgres")
	require.Error(t, err)
	assert.NotErrorIs(t, err, dbtest.ErrNoFixtures)
}

// The clone is the caller's to close, and closing it removes it.
func TestOpenSeededClonesAndCloses(t *testing.T) {
	t.Parallel()

	adminDSN := os.Getenv(dbtest.SeededDSNEnvVar)
	if adminDSN == "" {
		t.Skipf("%s is not set; skipping seeded-database test", dbtest.SeededDSNEnvVar)
	}

	db, err := dbtest.OpenSeeded(t.Context(), adminDSN)
	require.NoError(t, err)

	require.NotEmpty(t, db.Name)

	var count int64
	require.NoError(t, db.Gorm.Raw("SELECT count(*) FROM torrents").Scan(&count).Error)
	assert.Positive(t, count, "a clone of the seed template is not empty")

	name := db.Name
	db.Close()

	// A plain admin connection, not a second clone: asking whether a ~1GB copy
	// was removed by making another one is not a reasonable way to find out.
	admin, err := sql.Open("pgx", adminDSN)
	require.NoError(t, err)

	t.Cleanup(func() { _ = admin.Close() })

	var remaining int
	require.NoError(t, admin.
		QueryRowContext(t.Context(), `SELECT count(*) FROM pg_database WHERE datname = $1`, name).
		Scan(&remaining))
	assert.Zero(t, remaining, "Close must drop the clone")
}
