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

type UserConfigValues struct {
	InvitationRequired     *atomic.Value[user.InvitationRequired]
	EmailRequired          *atomic.Value[user.EmailRequired]
	EmailVerification      *atomic.Value[user.EmailVerification]
	PasswordMinEntropy     *atomic.Value[user.PasswordMinEntropy]
	PasswordHashingCost    *atomic.Value[user.PasswordHashingCost]
	LoginRequestsPerMinute *atomic.Value[user.LoginRequestsPerMinute]
	LoginRequestBurst      *atomic.Value[user.LoginRequestBurst]
}

// authObject is the GraphQL object guarding user, role and invitation
// administration.
const authObject = "auth"

// AnonymousPermissions grants the anon role the registered object actions while
// anonymous access is enabled, preserving the open behaviour of an installation
// that has never configured authentication. On next this decision is distributed
// across plugins, each granting its own object actions to anon; without a plugin
// registry the same effect is achieved centrally.
//
// Auth administration is deliberately excluded. Granting it made the open
// default a trapdoor rather than a starting point: an anonymous caller could
// call putRole to give the anon role a wildcard permission, and because role
// grants live in the database while this grant is only in memory, that survived
// setting anonymous_access to false — the instance stayed wide open with no
// outward sign. The same access also listed the bootstrap invitation, so an
// anonymous caller could claim the first administrator account.
//
// Nothing is lost by excluding it: the auth surface is new, so no previously
// open installation had it to begin with. Bootstrapping does not need it either
// — the first administrator registers with the invitation code from the startup
// log, through self.register, which the baseline grants.
func AnonymousPermissions(cfg Config, provider rbac.ObjectActionProvider) rbac.PermissionProvider {
	return func() []rbac.Permission {
		if !cfg.AnonymousAccess {
			return nil
		}

		objectActions := provider()
		permissions := make([]rbac.Permission, 0, len(objectActions))

		for _, objectAction := range objectActions {
			if objectAction.Object == authObject {
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
