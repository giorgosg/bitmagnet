package dbtest

// The seeded path: databases cloned from the template btm-testdb builds on this
// host, for tests that need an index with content in it.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	migrationssql "github.com/bitmagnet-io/bitmagnet/migrations"
	"github.com/jackc/pgx/v5/pgconn"
)

// SeededDSNEnvVar names the environment variable holding an admin connection
// string for the instance that hosts the seed template: the btm-testdb fixture
// instance on this host, e.g.
// postgres://postgres:postgres@127.0.0.1:5434/bitmagnet.
//
// It is deliberately separate from [DSNEnvVar]: the fixtures live on their own
// instance, and CI only ever sets the empty-database one.
const SeededDSNEnvVar = "TEST_POSTGRES_TEMPLATE_DSN"

// skipError marks a provisioning outcome that means "no fixtures in this
// checkout" — the caller skips the test rather than failing it. Any other error
// is a fixture setup that was asked for and did not work: that fails loudly,
// because silently skipping tests that were expected to run proves less than it
// appears to.
type skipError struct{ reason string }

func (e skipError) Error() string { return e.reason }

// seedNamePattern matches the database names btm-testdb gives its seed
// templates: btm_seed_<version>, where <version> is the highest goose migration
// applied when the template was built — the schema fingerprint the clone is
// checked against. Kept in step with btm-testdb's bin/cmd-seed.sh.
var seedNamePattern = regexp.MustCompile(`^btm_seed_(\d+)$`)

// migrationNamePattern extracts the version from a goose migration file name
// such as 00022_auth.sql.
var migrationNamePattern = regexp.MustCompile(`^(\d+)_`)

// seedTemplate is a template found on the fixture instance, together with the
// schema fingerprint its name carries.
type seedTemplate struct {
	name    string
	version int
}

// templateVersion parses the schema fingerprint out of a seed template name.
// Only exact matches count: the parsed version moves into SQL unquoted, so a
// name that only looks like a template must be rejected.
func templateVersion(name string) (int, bool) {
	m := seedNamePattern.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}

	version, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}

	return version, true
}

// latestSeedName picks the newest template out of the database names an
// instance reports: the highest version wins, numerically — lexicographic order
// would prefer btm_seed_9 over btm_seed_10.
func latestSeedName(names []string) (seedTemplate, bool) {
	best, found := seedTemplate{}, false
	for _, name := range names {
		if version, ok := templateVersion(name); ok && (!found || version > best.version) {
			best, found = seedTemplate{name: name, version: version}, true
		}
	}

	return best, found
}

// treeMigrationVersion reports the highest version among the migrations
// embedded in this tree — the schema the empty-database path migrates to, and
// what a template built from a checkout of this tree must carry.
func treeMigrationVersion() (int, error) {
	entries, err := fs.ReadDir(migrationssql.FS, ".")
	if err != nil {
		return 0, fmt.Errorf("reading the embedded migrations: %w", err)
	}

	highest := 0

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}

		m := migrationNamePattern.FindStringSubmatch(name)
		if m == nil {
			return 0, fmt.Errorf("unrecognised migration file name %q", name)
		}

		version, err := strconv.Atoi(m[1])
		if err != nil {
			return 0, fmt.Errorf("parsing the version out of %q: %w", name, err)
		}

		highest = max(highest, version)
	}

	return highest, nil
}

// checkFresh refuses a template whose schema fingerprint does not match the
// migrations in this tree. Both directions are wrong: a template older than the
// tree hands back a database missing columns this code writes to, a newer one a
// database with state this code does not know about — subtly wrong either way,
// and never something to test against silently.
func checkFresh(template seedTemplate, treeVersion int) error {
	switch {
	case template.version < treeVersion:
		return fmt.Errorf(
			"seed template %s was built at migration %d, but this tree has migrations up to %d: "+
				"the fixture is stale, and cloning it would hand back a subtly wrong schema. "+
				"Rebuild it with btm-testdb's `bin/testdb seed`",
			template.name, template.version, treeVersion,
		)
	case template.version > treeVersion:
		return fmt.Errorf(
			"seed template %s was built at migration %d, but this tree only migrates to %d: "+
				"the fixture is from a newer tree. Rebuild the fixture, or update this checkout",
			template.name, template.version, treeVersion,
		)
	}

	return nil
}

// NewSeeded provisions a fresh database by cloning the seed template btm-testdb
// builds on this host, and registers cleanup that drops it again. The clone
// arrives fully migrated and populated with the fixture corpus — roughly 100k
// torrent contents — for the cost of a file-level copy, about a second. Treat
// its content as read-only: one immutable template is what keeps that cost flat
// no matter how many tests clone it.
//
// It skips the test when [SeededDSNEnvVar] is unset, or when the instance it
// names carries no template. A template whose schema does not match the
// migrations in this tree is refused rather than handed back.
func NewSeeded(t *testing.T) *DB {
	t.Helper()

	adminDSN := os.Getenv(SeededDSNEnvVar)
	if adminDSN == "" {
		t.Skipf("%s is not set; skipping seeded-database test", SeededDSNEnvVar)
	}

	// Cancelled when the test ends, which is before cleanup runs — hence drop's
	// own context below.
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	db, sqlDB, err := provisionSeeded(ctx, adminDSN)

	var skip skipError
	if errors.As(err, &skip) {
		t.Skipf("%s; skipping seeded-database test", skip.Error())
	}

	if err != nil {
		t.Fatalf("provisioning a seeded database: %v", err)
	}

	t.Cleanup(func() { db.drop(adminDSN, sqlDB) })

	return db
}

// provisionSeeded creates the test's database by cloning the seed template.
//
// A [skipError] return means the fixtures are absent here and the test should
// skip: [SeededDSNEnvVar] unset is caught by [NewSeeded], but an instance with
// no template on it skips here. Every other error is a fixture setup that was
// explicitly configured and did not work — a misconfiguration, not an absent
// fixture.
func provisionSeeded(ctx context.Context, adminDSN string) (*DB, *sql.DB, error) {
	admin, err := sql.Open("pgx", adminDSN)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to the fixture instance: %w", err)
	}

	defer func() { _ = admin.Close() }()

	// sql.Open is lazy; ping so an instance that is not up is reported here,
	// with the address, instead of surfacing confusingly on the first query.
	if err := admin.PingContext(ctx); err != nil {
		return nil, nil, fmt.Errorf("connecting to the fixture instance at %s: %w", redactedDSN(adminDSN), err)
	}

	template, err := findSeedTemplate(ctx, admin)
	if err != nil {
		return nil, nil, err
	}

	treeVersion, err := treeMigrationVersion()
	if err != nil {
		return nil, nil, err
	}

	if err := checkFresh(template, treeVersion); err != nil {
		return nil, nil, err
	}

	name := newTestDBName()

	// Neither name can be parameterised. The test database name is generated
	// above, never caller-supplied, and the template name only ever comes back
	// from [findSeedTemplate], which has already matched it against
	// [seedNamePattern].
	if _, err = admin.ExecContext(ctx, `CREATE DATABASE "`+name+`" TEMPLATE "`+template.name+`"`); err != nil {
		return nil, nil, cloneError(template.name, name, err)
	}

	dsn := replaceDatabase(adminDSN, name)

	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to %s: %w", name, err)
	}

	db, err := assemble(ctx, name, dsn, sqlDB)
	if err != nil {
		_ = sqlDB.Close()

		// The clone already exists; best-effort removal so a failed wiring does
		// not leave it behind on the fixture instance.
		_, _ = admin.ExecContext(ctx, `DROP DATABASE IF EXISTS "`+name+`" WITH (FORCE)`)

		return nil, nil, err
	}

	return db, sqlDB, nil
}

// findSeedTemplate reports the seed template to clone: the newest btm_seed_<n>
// database on the fixture instance. A [skipError] comes back when there is none,
// which is what "the fixtures have not been built here" looks like.
func findSeedTemplate(ctx context.Context, admin *sql.DB) (seedTemplate, error) {
	// A regular expression rather than LIKE, so the underscore in btm_seed is
	// matched literally and versions are anchored at both ends.
	rows, err := admin.QueryContext(ctx, `SELECT datname FROM pg_database WHERE datname ~ '^btm_seed_[0-9]+$'`)
	if err != nil {
		return seedTemplate{}, fmt.Errorf("listing the databases on the fixture instance: %w", err)
	}

	defer rows.Close()

	var names []string

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return seedTemplate{}, fmt.Errorf("reading the database names on the fixture instance: %w", err)
		}

		names = append(names, name)
	}

	if err := rows.Err(); err != nil {
		return seedTemplate{}, fmt.Errorf("reading the database names on the fixture instance: %w", err)
	}

	template, ok := latestSeedName(names)
	if !ok {
		return seedTemplate{}, skipError{
			"no seed template (btm_seed_<version>) exists on the fixture instance; " +
				"build the fixtures with btm-testdb's `bin/testdb seed`",
		}
	}

	return template, nil
}

// cloneError decorates a failed CREATE DATABASE … TEMPLATE. The one failure
// mode worth spelling out is SQLSTATE 55006: Postgres refuses to clone a
// template while anything is connected to it. btm-testdb seals its templates
// against connections once built, so on a healthy setup this only happens while
// the template is being rebuilt.
func cloneError(template, name string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "55006" { // object_in_use
		return fmt.Errorf(
			"cloning %s as %s: the template is in use. Postgres refuses to clone a "+
				"template while anything is connected to it; btm-testdb seals its templates "+
				"against connections once built, so this usually means a rebuild "+
				"(`bin/testdb seed`) is running right now — wait for it to finish and retry. "+
				"See btm-testdb's docs/resetting.md: %w",
			template, name, err,
		)
	}

	return fmt.Errorf("cloning seed template %s as %s: %w", template, name, err)
}

// redactedDSN strips the credentials out of a connection string, for error
// messages — the scheme, host, port and database stay, since those are what
// point a person at the wrong instance.
func redactedDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return "(unparsable DSN)"
	}

	u.User = nil

	return u.String()
}
