package dbtest

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTemplateVersion(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		version int
		ok      bool
	}{
		{"btm_seed_22", 22, true},
		{"btm_seed_0", 0, true},
		{"btm_seed_123", 123, true},
		// Everything else must be rejected: the version moves into SQL, so a
		// mis-parsed name is a database we clone from blindly.
		{"bitmagnet", 0, false},
		{"postgres", 0, false},
		{"btm_seed_", 0, false},
		{"btm_seed_abc", 0, false},
		{"BTM_SEED_22", 0, false},
		{"btm_seed_22 ", 0, false},
		{"", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			version, ok := templateVersion(tc.name)
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.version, version)
		})
	}
}

func TestPickSeedTemplate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		names       []string
		treeVersion int
		want        seedTemplate
		ok          bool
	}{
		{
			// The instance accumulates templates: btm-testdb never drops a
			// superseded one. A checkout must get the template built at its own
			// migration version, even when a newer one sits beside it, or a
			// maintenance branch is refused a fixture that is present.
			name:        "exact match wins over a newer template",
			names:       []string{"btm_seed_22", "btm_seed_23"},
			treeVersion: 22,
			want:        seedTemplate{name: "btm_seed_22", version: 22},
			ok:          true,
		},
		{
			name:        "exact match wins from the middle of the pile",
			names:       []string{"btm_seed_21", "btm_seed_23", "btm_seed_22"},
			treeVersion: 22,
			want:        seedTemplate{name: "btm_seed_22", version: 22},
			ok:          true,
		},
		{
			// No exact match: hand back the newest and let checkFresh name it
			// stale, which says more than "no template here" would.
			name:        "newest wins when nothing matches exactly",
			names:       []string{"btm_seed_9", "btm_seed_10"},
			treeVersion: 11,
			want:        seedTemplate{name: "btm_seed_10", version: 10},
			ok:          true,
		},
		{
			// Numeric order, not lexicographic: btm_seed_9 is older than btm_seed_10.
			name:        "newest is numeric, not lexicographic",
			names:       []string{"btm_seed_10", "btm_seed_9"},
			treeVersion: 22,
			want:        seedTemplate{name: "btm_seed_10", version: 10},
			ok:          true,
		},
		{
			name:        "non-templates are ignored",
			names:       []string{"btm_seed_22", "btm_seed_21", "bitmagnet", "postgres"},
			treeVersion: 22,
			want:        seedTemplate{name: "btm_seed_22", version: 22},
			ok:          true,
		},
		{
			name:        "no templates at all",
			names:       []string{"bitmagnet", "postgres", "template0"},
			treeVersion: 22,
		},
		{
			name:        "no databases at all",
			names:       nil,
			treeVersion: 22,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			template, ok := pickSeedTemplate(tc.names, tc.treeVersion)
			require.Equal(t, tc.ok, ok)

			if tc.ok {
				require.Equal(t, tc.want, template)
			}
		})
	}
}

func TestTreeMigrationVersion(t *testing.T) {
	t.Parallel()

	version, err := treeMigrationVersion()
	require.NoError(t, err)
	// The tree's high-water mark at the time this test was written; it only
	// moves up, so this stays green as migrations land.
	require.GreaterOrEqual(t, version, 22)
}

func TestCheckFresh(t *testing.T) {
	t.Parallel()

	require.NoError(t, checkFresh(seedTemplate{name: "btm_seed_22", version: 22}, 22))

	err := checkFresh(seedTemplate{name: "btm_seed_22", version: 22}, 23)
	require.Error(t, err)
	require.Contains(t, err.Error(), "stale")
	require.Contains(t, err.Error(), "22")
	require.Contains(t, err.Error(), "23")
	require.Contains(t, err.Error(), "bin/testdb seed")

	err = checkFresh(seedTemplate{name: "btm_seed_23", version: 23}, 22)
	require.Error(t, err)
	require.Contains(t, err.Error(), "newer")
}

// The fixture instance is configured but unreachable. That is a misconfiguration,
// not an absent fixture: it must be an error, not a skip.
func TestProvisionSeededUnreachableInstanceIsAnError(t *testing.T) {
	t.Parallel()

	_, _, err := provisionSeeded(t.Context(), "postgres://postgres:postgres@127.0.0.1:54399/postgres")
	require.Error(t, err)

	var skip skipError
	require.NotErrorAs(t, err, &skip)
}

// A reachable instance with no btm_seed_* database on it — the same instance the
// empty-database tests use — is an absent fixture: skip, not fail.
func TestProvisionSeededSkipsWithoutTemplate(t *testing.T) {
	adminDSN := os.Getenv(DSNEnvVar)
	if adminDSN == "" {
		t.Skipf("%s is not set; skipping", DSNEnvVar)
	}

	t.Parallel()

	_, _, err := provisionSeeded(t.Context(), adminDSN)

	var skip skipError
	require.ErrorAs(t, err, &skip)
}

// Runs only where the btm-testdb fixtures exist: TEST_POSTGRES_TEMPLATE_DSN names
// the fixture instance, and the seed template it hosts must be fresh against this
// tree. CI never sets the variable, so this skips there.
func TestNewSeededClonesSeedTemplate(t *testing.T) {
	t.Parallel()

	start := time.Now()
	db := NewSeeded(t)
	elapsed := time.Since(start)

	// The whole point of the template clone: corpus-sized content for the cost
	// of a file-level copy, not a migration run.
	require.Less(t, elapsed, 5*time.Second, "seeding should cost about a second")
	t.Logf("cloned a seeded database in %s", elapsed)

	// torrent_contents is what the corpus is measured in — btm-testdb samples it
	// and carries the torrents belonging to the sampled hashes across with it, so
	// counting torrents alone would pass on a template whose contents table came
	// across truncated.
	var contents int64
	require.NoError(t, db.Gorm.Raw(`SELECT count(*) FROM torrent_contents`).Scan(&contents).Error)
	require.GreaterOrEqual(
		t, contents, int64(50_000),
		"expected the fixture corpus (~100k torrent contents), got a fraction of it")

	var torrents int64
	require.NoError(t, db.Gorm.Raw(`SELECT count(*) FROM torrents`).Scan(&torrents).Error)
	require.GreaterOrEqual(
		t, torrents, int64(50_000), "expected the fixture corpus (~100k torrents), got a fraction of it")

	// The clone must arrive at this tree's schema version, not one migration off.
	var version int
	require.NoError(t, db.Gorm.Raw(`SELECT max(version_id) FROM goose_db_version`).Scan(&version).Error)

	treeVersion, err := treeMigrationVersion()
	require.NoError(t, err)
	require.Equal(t, treeVersion, version)
}
