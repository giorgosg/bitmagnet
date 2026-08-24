package authconfig_test

import (
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/go-playground/validator/v10"
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

// The default config is what an installation that has configured nothing runs
// with, so it has to satisfy its own constraints; a default that fails
// validation would refuse to start.
func TestDefaultConfigIsValid(t *testing.T) {
	t.Parallel()

	require.NoError(t, validator.New().Struct(authconfig.NewDefaultConfig()))
}

// EmailVerification is inert — no verification code is issued and nothing reads
// the value — so it defaults off rather than advertising a check that does not
// happen.
func TestEmailVerificationDefaultsOff(t *testing.T) {
	t.Parallel()

	assert.False(t, authconfig.NewDefaultConfig().EmailVerification)
}

func TestBrowserCookieNameDefaultsToSecurePrefix(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "__Secure-bitmagnet", authconfig.NewDefaultConfig().BrowserCookieName)
}

// next declares these parameters with bounds; expressing them as a plain struct
// dropped the bounds, leaving values that disable a control or crash the
// process reachable from a config file.
func TestConfigRejectsValuesNextWouldReject(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		mutate func(*authconfig.Config)
	}{
		{
			// time.Minute / 0 in the limiter's rate computation.
			"zero login requests per minute",
			func(c *authconfig.Config) { c.LoginRequestsPerMinute = 0 },
		},
		{
			"zero login request burst",
			func(c *authconfig.Config) { c.LoginRequestBurst = 0 },
		},
		{
			// Accepts any password whatsoever.
			"zero password entropy",
			func(c *authconfig.Config) { c.PasswordMinEntropy = 0 },
		},
		{
			// Rejected by bcrypt itself, breaking registration and the decoy
			// comparison that hides whether an account exists.
			"bcrypt cost below the minimum",
			func(c *authconfig.Config) { c.PasswordHashingCost = 3 },
		},
		{
			"bcrypt cost above the maximum",
			func(c *authconfig.Config) { c.PasswordHashingCost = 32 },
		},
		{
			// Issues tokens that have already expired.
			"zero jwt duration",
			func(c *authconfig.Config) { c.JWTDuration = 0 },
		},
		{
			"browser cookie without secure prefix",
			func(c *authconfig.Config) { c.BrowserCookieName = "bitmagnet" },
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := authconfig.NewDefaultConfig()
			tt.mutate(&cfg)

			assert.Error(t, validator.New().Struct(cfg))
		})
	}
}

// The bounds have to leave the useful range alone.
func TestConfigAcceptsPlausibleHardening(t *testing.T) {
	t.Parallel()

	cfg := authconfig.NewDefaultConfig()
	cfg.PasswordMinEntropy = 100
	cfg.PasswordHashingCost = 14
	cfg.LoginRequestsPerMinute = 5
	cfg.LoginRequestBurst = 1
	cfg.JWTDuration = time.Minute * 15

	assert.NoError(t, validator.New().Struct(cfg))
}
