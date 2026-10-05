package authconfig_test

import (
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/http_auth"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// registered mirrors the real registered set: the three action verbs the schema
// and the HTTP guards actually use, across a read object, the destructive ones,
// and auth.
func registered() rbac.ObjectActionProvider {
	return func() []rbac.ObjectAction {
		return []rbac.ObjectAction{
			rbac.NewObjectAction("graphql", "torrentContent", "query"),
			rbac.NewObjectAction("graphql", "queue", "query"),
			rbac.NewObjectAction("graphql", "torrent", "query"),
			rbac.NewObjectAction("graphql", "torrent", "mutate"),
			rbac.NewObjectAction("graphql", "torrent", "delete"),
			rbac.NewObjectAction("graphql", "queue", "mutate"),
			rbac.NewObjectAction("graphql", "auth", "query"),
			rbac.NewObjectAction("graphql", "auth", "mutate"),
			rbac.NewObjectAction("http", "import", "mutate"),
			rbac.NewObjectAction("http", "metrics", "query"),
			rbac.NewObjectAction("http", "pprof", "query"),
		}
	}
}

// actions keys the grants by "object:action", because the policy now turns on
// the action and not only the object.
func actions(permissions []rbac.Permission) map[string]bool {
	seen := map[string]bool{}

	for _, p := range permissions {
		oa := p.ObjectAction()
		seen[oa.Object+":"+oa.Action] = true
	}

	return seen
}

// While anonymous access is on, an installation that never configured auth keeps
// its *read* surface reachable without a credential. It used to keep every
// registered object action, writes included; see
// TestAnonymousPermissionsGrantNoWrites for why that changed.
func TestAnonymousPermissionsGrantTheReadSurfaceWhenOpen(t *testing.T) {
	t.Parallel()

	cfg := authconfig.NewDefaultConfig()
	require.True(t, cfg.AnonymousAccess, "anonymous access must default to on")

	granted := actions(authconfig.AnonymousPermissions(cfg, registered())())

	assert.True(t, granted["torrentContent:query"], "the catalogue stays anonymous-readable")
	assert.True(t, granted["torrent:query"])
	assert.True(t, granted["queue:query"])
}

// The baseline granted every registered object action except those on auth,
// which meant torrent:delete, torrent:mutate, queue:mutate and import:mutate.
// allowed_origins still defaults to "*" and Content-Type: application/json is an
// allowed header, so any page the operator visited could delete torrents from a
// reachable instance — and for a delete the side effect is the damage, so it did
// not matter that the response was unreadable.
//
// The rule is an allow-list on the action, not a denylist: an object action with
// a verb nobody anticipated is denied to anonymous callers rather than granted.
// A read wrongly withheld reports `unauthorized` and gets noticed; a write
// wrongly granted does not.
func TestAnonymousPermissionsGrantNoWrites(t *testing.T) {
	t.Parallel()

	cfg := authconfig.NewDefaultConfig()
	granted := actions(authconfig.AnonymousPermissions(cfg, registered())())

	for _, objectAction := range []string{
		"torrent:delete",
		"torrent:mutate",
		"queue:mutate",
		"import:mutate",
	} {
		assert.False(t, granted[objectAction],
			"the anonymous baseline must not grant %s", objectAction)
	}
}

// An action verb this policy has never seen is not a read until someone says so.
func TestAnonymousPermissionsDenyUnknownActions(t *testing.T) {
	t.Parallel()

	cfg := authconfig.NewDefaultConfig()
	provider := func() []rbac.ObjectAction {
		return []rbac.ObjectAction{
			rbac.NewObjectAction("graphql", "torrent", "reprocess"),
		}
	}

	assert.Empty(t, authconfig.AnonymousPermissions(cfg, provider)(),
		"an unrecognised action must not be granted by default")
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

// The read surface is the *catalogue*, not the operator's instruments.
// /debug/pprof and /metrics are guarded by object actions whose verb is "query",
// so a baseline that grants every read granted them too — and that is a
// different kind of read:
//
//   - /debug/pprof/heap and /goroutine?debug=2 dump process memory structure and
//     every stack, which on an instance that has configured auth is a plausible
//     route to the JWT signing key.
//   - /debug/pprof/profile runs a CPU profile whose duration is caller-supplied
//     through ?seconds=, repeatedly, which is a cheap denial of service against a
//     box already saturated by classification.
//   - /metrics discloses index size, queue depth and crawl rate.
//
// None of it is the catalogue an open instance exists to serve, and the
// difference between a LAN address and a public one is exactly the move this
// matters for.
func TestAnonymousPermissionsExcludeOperationalEndpoints(t *testing.T) {
	t.Parallel()

	cfg := authconfig.NewDefaultConfig()
	granted := actions(authconfig.AnonymousPermissions(cfg, registered())())

	assert.False(t, granted["pprof:query"],
		"profiling must never be anonymous: it dumps process memory and burns CPU on demand")
	assert.False(t, granted["metrics:query"],
		"the Prometheus scrape must carry a credential")
}

// The baseline excludes those objects by name, because authconfig cannot import
// http_auth — http_auth reaches browser_session, which imports authconfig back.
// This test can import both, so the literals are pinned here: rename an object
// action and this fails rather than the exclusion silently lapsing.
func TestAnonymousExclusionsMatchTheRealObjectActions(t *testing.T) {
	t.Parallel()

	cfg := authconfig.NewDefaultConfig()
	granted := actions(authconfig.AnonymousPermissions(cfg, http_auth.ObjectActionProvider())())

	for _, excluded := range []rbac.ObjectAction{
		http_auth.ObjectActionPprof,
		http_auth.ObjectActionMetrics,
		http_auth.ObjectActionImport,
	} {
		assert.False(t, granted[excluded.Object+":"+excluded.Action],
			"the anonymous baseline must not grant %s:%s", excluded.Object, excluded.Action)
	}
}
