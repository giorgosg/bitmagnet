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

// AnonymousPermissions grants the anon role the registered *read* object actions
// while anonymous access is enabled, preserving the open behaviour of an
// installation that has never configured authentication. On next this decision
// is distributed across plugins, each granting its own object actions to anon;
// without a plugin registry the same effect is achieved centrally.
//
// Writes are deliberately excluded. The baseline used to be every registered
// object action except those on auth, which meant it carried torrent:delete,
// torrent:mutate, queue:mutate and import:mutate — and because
// http_server.cors.allowed_origins still defaults to "*" with Content-Type:
// application/json among the allowed headers, any page the operator visited
// could issue those cross-origin against a reachable instance. For a delete the
// side effect is the damage, so it never mattered that the response was
// unreadable.
//
// Nothing an open installation served before authentication existed is lost:
// reads stay anonymous, and the write surfaces were reachable only because this
// lineage had no authentication at all to put in front of them. An operator who
// wants anonymous writes grants them to the anon role explicitly.
//
// Auth administration is excluded on top of that, and for a different reason.
// Granting it made the open default a trapdoor rather than a starting point: an
// anonymous caller could call putRole to give the anon role a wildcard
// permission, and because role grants live in the database while this grant is
// only in memory, that survived setting anonymous_access to false — the instance
// stayed wide open with no outward sign. The same access also listed the
// bootstrap invitation, so an anonymous caller could claim the first
// administrator account.
//
// The read surface is the catalogue, not the operator's instruments:
// anonymousExcludedObjects also withholds /debug/pprof and /metrics, whose
// object actions carry the "query" verb but are not what an open instance exists
// to serve.
//
// Bootstrapping does not need any of it: the first administrator registers with
// the invitation code through self.register, and the top-level self boundary is
// outside the object-action model entirely.
func AnonymousPermissions(cfg Config, provider rbac.ObjectActionProvider) rbac.PermissionProvider {
	return func() []rbac.Permission {
		if !cfg.AnonymousAccess {
			return nil
		}

		objectActions := provider()
		permissions := make([]rbac.Permission, 0, len(objectActions))

		for _, objectAction := range objectActions {
			if _, ok := anonymousExcludedObjects[objectAction.Object]; ok {
				continue
			}

			if _, ok := anonymousActions[objectAction.Action]; !ok {
				continue
			}

			permissions = append(permissions, rbac.NewPermission(
				rbac.SubjectRole{Role: rbac.RoleAnon},
				objectAction,
			))
		}

		return permissions
	}
}
