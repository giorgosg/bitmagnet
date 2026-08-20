// Package dbtest provisions real PostgreSQL databases for integration tests.
//
// Most of bitmagnet's core — the DHT crawler, the queue, the importer — talks to
// Postgres for everything it does, and none of it can be meaningfully tested
// against a mock. Tests that need a database call [New], which hands back an
// isolated, fully migrated database and tears it down afterwards.
//
// Tests are opt-in via the TEST_POSTGRES_DSN environment variable, pointing at a
// server the test may freely create and drop databases on:
//
//	TEST_POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/postgres' go test ./...
//
// When it is unset, [New] skips the test rather than failing, so the default
// `go test ./...` stays fast and needs no services. CI sets it.
package dbtest

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/exclause"
	migrationssql "github.com/bitmagnet-io/bitmagnet/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	goose "github.com/pressly/goose/v3"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// DSNEnvVar names the environment variable holding the admin connection string.
const DSNEnvVar = "TEST_POSTGRES_DSN"

// DB is an isolated, migrated database for the duration of one test.
type DB struct {
	// Gorm is the connection the DAO is built on.
	Gorm *gorm.DB
	// Query is the generated DAO, the way most of the codebase reaches Postgres.
	Query *dao.Query
	// Pool is a pgx pool over the same database, for code that needs pgx
	// directly — LISTEN/NOTIFY, in particular.
	Pool *pgxpool.Pool
	// DSN points at this test's own database.
	DSN string
	// Name is the generated database name.
	Name string
}

// New provisions a fresh database, applies every migration, and registers
// cleanup that drops it again. It skips the test when TEST_POSTGRES_DSN is unset.
func New(t *testing.T) *DB {
	t.Helper()

	adminDSN := os.Getenv(DSNEnvVar)
	if adminDSN == "" {
		t.Skipf("%s is not set; skipping database integration test", DSNEnvVar)
	}

	// Cancelled when the test ends, which is before cleanup runs — hence drop's
	// own context below.
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	name := fmt.Sprintf("bitmagnet_test_%d_%d", time.Now().UnixNano(), rand.IntN(1_000_000))

	admin, err := sql.Open("pgx", adminDSN)
	if err != nil {
		t.Fatalf("connecting to %s: %v", DSNEnvVar, err)
	}

	// Database names cannot be parameterised, hence the interpolation. The name
	// is generated above, never caller-supplied.
	if _, err = admin.ExecContext(ctx, `CREATE DATABASE "`+name+`"`); err != nil {
		_ = admin.Close()

		t.Fatalf("creating database %s: %v", name, err)
	}

	if err = admin.Close(); err != nil {
		t.Fatalf("closing admin connection: %v", err)
	}

	dsn := replaceDatabase(adminDSN, name)

	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("connecting to %s: %v", name, err)
	}

	migrate(ctx, t, sqlDB)

	gormDB, err := gorm.Open(
		postgres.New(postgres.Config{Conn: sqlDB}),
		&gorm.Config{
			Logger:               gormlogger.Discard,
			DisableAutomaticPing: true,
		},
	)
	if err != nil {
		t.Fatalf("opening gorm on %s: %v", name, err)
	}

	if err = gormDB.Use(exclause.New()); err != nil {
		t.Fatalf("registering the exclause plugin: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("creating a pgx pool on %s: %v", name, err)
	}

	db := &DB{
		Gorm:  gormDB,
		Query: dao.Use(gormDB),
		Pool:  pool,
		DSN:   dsn,
		Name:  name,
	}

	t.Cleanup(func() { db.drop(adminDSN, sqlDB) })

	return db
}

// drop closes everything and removes the database. Postgres refuses to drop a
// database with live connections, so ordering matters here.
func (db *DB) drop(adminDSN string, sqlDB *sql.DB) {
	db.Pool.Close()
	_ = sqlDB.Close()

	// The test's own context is cancelled by the time cleanup runs.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := sql.Open("pgx", adminDSN)
	if err != nil {
		return
	}

	defer func() { _ = admin.Close() }()

	_, _ = admin.ExecContext(ctx, `DROP DATABASE IF EXISTS "`+db.Name+`" WITH (FORCE)`)
}

// replaceDatabase swaps the database name in a URL-style DSN, so the admin
// connection string can be reused to reach the per-test database.
func replaceDatabase(dsn, name string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}

	u.Path = "/" + name

	return u.String()
}

// migrate applies the embedded goose migrations.
//
// goose's package-level API — SetBaseFS, SetLogger, SetDialect — writes globals,
// so calling it from tests that run in parallel is a data race. [goose.Provider]
// holds the same configuration per instance and is documented safe for concurrent
// use, so each test migrates through a provider of its own and no global is
// touched.
func migrate(ctx context.Context, t *testing.T, sqlDB *sql.DB) {
	t.Helper()

	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		sqlDB,
		migrationssql.FS,
		goose.WithLogger(goose.NopLogger()),
	)
	if err != nil {
		t.Fatalf("creating the goose provider: %v", err)
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("applying migrations: %v", err)
	}
}
