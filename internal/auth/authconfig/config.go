package authconfig

import (
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/atomic"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/user"
	"golang.org/x/crypto/bcrypt"
)

// Config is the main-lineage equivalent of the parameters upstream/next declares
// through its plugin config builder. next resolves those via its plugin registry,
// which this lineage does not have, so they are expressed here as an ordinary
// config struct and converted to the atomic values the services expect.
type Config struct {
	// AnonymousAccess grants the anon role every registered object action, which
	// leaves the application open exactly as it is today. This is the switch that
	// makes authentication opt-in: while it is true, existing clients and the
	// bundled web UI keep working without credentials.
	AnonymousAccess bool

	JWTSecret string
	// A non-positive duration issues tokens that are already expired, which is
	// a lockout rather than a strict setting.
	JWTDuration time.Duration `validate:"gt=0"`
	// BrowserCookieName names the HttpOnly credential used by same-origin web
	// clients. The __Secure- prefix requires HTTPS and forbids insecure cookies.
	BrowserCookieName string `validate:"startswith=__Secure-"`

	RBACCacheTTL time.Duration

	InvitationRequired bool
	EmailRequired      bool
	// EmailVerification is inert: nothing reads it and no verification code is
	// ever issued. It defaults to false so that the configuration does not
	// advertise a check that is not performed. See docs/auth.md.
	EmailVerification bool
	// The constraints below are next's, which declares these parameters through
	// its plugin config builder with the same bounds. Expressing them as an
	// ordinary struct dropped the bounds along with the builder, and they are
	// not decorative: a zero PasswordMinEntropy accepts any password, and a
	// zero LoginRequestsPerMinute divides by zero when the limiter computes its
	// rate, taking the process down from config alone.
	PasswordMinEntropy float64 `validate:"min=50"`
	// bcrypt.DefaultCost and bcrypt.MaxCost; a cost outside them is rejected by
	// bcrypt itself, which breaks registration and the decoy comparison that
	// makes a login miss cost what a hit costs.
	PasswordHashingCost int `validate:"min=10,max=31"`

	LoginRequestsPerMinute int `validate:"gt=0"`
	LoginRequestBurst      int `validate:"gt=0"`
}

func NewDefaultConfig() Config {
	return Config{
		AnonymousAccess:        true,
		JWTDuration:            time.Hour * 24,
		BrowserCookieName:      "__Secure-bitmagnet",
		RBACCacheTTL:           time.Minute,
		InvitationRequired:     true,
		EmailVerification:      false,
		PasswordMinEntropy:     defaultPasswordMinEntropy,
		PasswordHashingCost:    bcrypt.DefaultCost,
		LoginRequestsPerMinute: defaultLoginRequestsPerMinute,
		LoginRequestBurst:      defaultLoginRequestBurst,
	}
}

// Defaults match the corresponding params on upstream/next, with one deliberate
// exception: next defaults EmailVerification to true, and this lineage defaults
// it to false because neither lineage implements it.
const (
	defaultPasswordMinEntropy     = 70
	defaultLoginRequestsPerMinute = 30
	defaultLoginRequestBurst      = 5
)

func (c Config) UserValues() UserConfigValues {
	return UserConfigValues{
		InvitationRequired:     atomic.NewValue(user.InvitationRequired(c.InvitationRequired)),
		EmailRequired:          atomic.NewValue(user.EmailRequired(c.EmailRequired)),
		EmailVerification:      atomic.NewValue(user.EmailVerification(c.EmailVerification)),
		PasswordMinEntropy:     atomic.NewValue(user.PasswordMinEntropy(c.PasswordMinEntropy)),
		PasswordHashingCost:    atomic.NewValue(user.PasswordHashingCost(c.PasswordHashingCost)),
		LoginRequestsPerMinute: atomic.NewValue(user.LoginRequestsPerMinute(c.LoginRequestsPerMinute)),
		LoginRequestBurst:      atomic.NewValue(user.LoginRequestBurst(c.LoginRequestBurst)),
	}
}

// UserConfigValues is user.ConfigValues, aliased so existing callers keep the
// name they already use. The struct belongs to package user, which is what
// consumes it; declaring it there is what lets user.NewService take the group
// whole rather than seven separate arguments.
type UserConfigValues = user.ConfigValues

// authObject is the GraphQL object guarding user, role and invitation
// administration.
const authObject = "auth"

// anonymousExcludedObjects are the objects an anonymous caller never reaches,
// whatever the action's verb.
//
// Named by literal because this package cannot import http_auth: http_auth
// reaches browser_session, which imports this package back. A test in
// authconfig_test, which can import both, pins these strings against the real
// object actions so a rename fails loudly instead of quietly lapsing.
var anonymousExcludedObjects = map[string]struct{}{
	// Auth administration, excluded for its own reason — see the note on
	// AnonymousPermissions.
	authObject: {},
	// Profiling. /debug/pprof/heap and /goroutine?debug=2 dump process memory
	// structure and every stack, which on an instance that has configured auth
	// is a plausible route to the JWT signing key; /debug/pprof/profile runs a
	// CPU profile whose duration the caller chooses through ?seconds=, as often
	// as it likes, against a box already saturated by classification.
	"pprof": {},
	// The Prometheus scrape, which discloses index size, queue depth and crawl
	// rate. A scrape that used to work unauthenticated now needs a credential:
	// mint an API key scoped to http:metrics:query and give it to Prometheus as
	// a bearer token.
	"metrics": {},
}

// anonymousActions is the set of action verbs the open baseline grants: reads,
// and nothing else.
//
// It is an allow-list on the action rather than a denylist of the destructive
// ones, so that an object action registered later with a verb nobody anticipated
// is denied to anonymous callers instead of granted. The two mistakes are not
// symmetrical: a read wrongly withheld answers `unauthorized` and gets reported,
// while a write wrongly granted is silent until something is gone.
var anonymousActions = map[string]struct{}{
	"query": {},
}

// Anonymous access is a deny-override, not a source of grants. The anon role's
// permissions live in role_permissions like any other role's, an administrator
// edits them through putRole, and `auth.anonymous_access: false` drops them when
// the casbin policy is compiled. See rbac.AnonymousAccess and docs/auth.md.
//
// There used to be an AnonymousPermissions provider here, which granted anon the
// read surface from the flag alone and held it in memory. That left two sources of
// anonymous permission - the flag's and the database's - unioned, so the flag
// withheld only its own half and a grant written through putRole survived being
// switched off, with nothing in the configuration or the logs disagreeing. That
// was docs/issues/0012.
//
// AnonymousReadSurface is the set of registered object actions an open
// installation grants anonymous callers: the read verbs, minus the objects an
// anonymous caller never reaches. See AnonymousPermissions for why each
// exclusion is there.
//
// It is deliberately separate from AnonymousPermissions, because the two have
// different lifetimes. The permission provider above is the in-memory grant that
// `auth.anonymous_access` used to carry, and it goes away once the flag becomes a
// deny-override. This function is the rule itself, and it stays: it is what seeds
// the anon role on a fresh installation, and what translates the flag's old
// meaning into stored rows on an existing one. Sharing it is what keeps the
// baseline and the seed from drifting apart while both exist.
func AnonymousReadSurface(provider rbac.ObjectActionProvider) []rbac.ObjectAction {
	objectActions := provider()
	surface := make([]rbac.ObjectAction, 0, len(objectActions))

	for _, objectAction := range objectActions {
		if _, ok := anonymousExcludedObjects[objectAction.Object]; ok {
			continue
		}

		if _, ok := anonymousActions[objectAction.Action]; !ok {
			continue
		}

		surface = append(surface, objectAction)
	}

	return surface
}
