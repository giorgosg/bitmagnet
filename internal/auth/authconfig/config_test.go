package authconfig_test

import (
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func registered() rbac.ObjectActionProvider {
	return func() []rbac.ObjectAction {
		return []rbac.ObjectAction{
			rbac.NewObjectAction("graphql", "torrentContent", "query"),
			rbac.NewObjectAction("graphql", "queue", "query"),
			rbac.NewObjectAction("graphql", "auth", "query"),
			rbac.NewObjectAction("graphql", "auth", "mutate"),
			rbac.NewObjectAction("http", "import", "mutate"),
		}
	}
}

func objects(permissions []rbac.Permission) map[string]bool {
	seen := map[string]bool{}
	for _, p := range permissions {
		seen[p.ObjectAction().Object] = true
	}

	return seen
}

// While anonymous access is on, an installation that never configured auth must
// behave as it always did — every registered object action reachable.
func TestAnonymousPermissionsGrantTheRegisteredSetWhenOpen(t *testing.T) {
	t.Parallel()

	cfg := authconfig.NewDefaultConfig()
	require.True(t, cfg.AnonymousAccess, "anonymous access must default to on")

	granted := objects(authconfig.AnonymousPermissions(cfg, registered())())

	assert.True(t, granted["torrentContent"], "the catalogue stays anonymous-readable")
	assert.True(t, granted["queue"])
	assert.True(t, granted["import"])
}

// Auth administration is the exception, and it is the important one: role grants
// persist in the database while this grant is only in memory, so a wildcard
// written onto the anon role while the instance was open would survive turning
// anonymous access off — a permanent bypass with nothing to show for it.
func TestAnonymousPermissionsNeverGrantAuthAdministration(t *testing.T) {
	t.Parallel()

	cfg := authconfig.NewDefaultConfig()

	for _, permission := range authconfig.AnonymousPermissions(cfg, registered())() {
		assert.NotEqual(t, "auth", permission.ObjectAction().Object,
			"anonymous callers must never administer auth, even in open mode")
	}
}

func TestAnonymousPermissionsGrantNothingWhenClosed(t *testing.T) {
	t.Parallel()

	cfg := authconfig.NewDefaultConfig()
	cfg.AnonymousAccess = false

	assert.Empty(t, authconfig.AnonymousPermissions(cfg, registered())())
}
