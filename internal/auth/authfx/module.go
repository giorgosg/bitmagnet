package authfx

import (
	"github.com/bitmagnet-io/bitmagnet/internal/auth/api_key"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/http_auth"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/identity"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/jwt"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/user"
	"github.com/bitmagnet-io/bitmagnet/internal/config/configfx"
	"github.com/bitmagnet-io/bitmagnet/internal/database"
	"go.uber.org/fx"
)

// New wires the ported upstream/next auth stack into the main lineage's fx graph.
//
// next assembles this through its plugin builder, which depends on the plugin
// registry, worker runner and ref system this lineage does not have, so the
// wiring is written directly against fx here. The service set, the value groups
// for object actions and permissions, and the lazy indirection that breaks the
// rbac dependency cycle all mirror internal/plugin/core/auth/plugin.go.
func New() fx.Option {
	return fx.Module(
		"auth",
		configfx.NewConfigModule[authconfig.Config]("auth", authconfig.NewDefaultConfig()),
		fx.Provide(
			// Config-derived values the services take directly.
			func(c authconfig.Config) jwt.Secret { return jwt.Secret(c.JWTSecret) },
			func(c authconfig.Config) jwt.Duration { return jwt.Duration(c.JWTDuration) },
			func(c authconfig.Config) rbac.CacheTTL { return rbac.CacheTTL(c.RBACCacheTTL) },

			newUserService,

			jwt.NewService,
			api_key.NewRepository,
			api_key.NewService,
			identity.NewAuthenticator,
			http_auth.NewMiddleware,
			http_auth.New,

			newBootstrapWorker,

			// Object actions and permissions are collected from value groups so
			// that other modules can contribute their own without this module
			// knowing about them.
			fx.Annotate(
				func(providers []rbac.ObjectActionProvider) rbac.ObjectActionProvider {
					return rbac.ObjectActionProviders(providers...)
				},
				fx.ParamTags(`group:"auth_object_actions"`),
			),
			fx.Annotate(
				func() rbac.PermissionProvider { return rbac.CorePermissions },
				fx.ResultTags(`group:"auth_permissions"`),
			),
			fx.Annotate(
				rbac.VerbatimPermissions,
				fx.ResultTags(`group:"auth_permissions"`),
			),
			fx.Annotate(
				authconfig.AnonymousPermissions,
				fx.ResultTags(`group:"auth_permissions"`),
			),
			fx.Annotate(
				func(providers []rbac.PermissionProvider) rbac.PermissionProvider {
					return rbac.PermissionProviders(providers...)
				},
				fx.ParamTags(`group:"auth_permissions"`),
			),

			newRBACService,

			// rbac.Service depends on providers that in turn need to enforce
			// against it. ServiceLazy breaks that cycle: it is injected everywhere
			// and has the real service set on it once the graph is built.
			fx.Annotate(
				rbac.NewServiceLazy,
				fx.As(new(rbac.Enforcer)),
				fx.As(new(rbac.Repository)),
				fx.As(new(rbac.Service)),
				fx.As(new(rbac.ServiceLazy)),
			),
		),
		fx.Invoke(func(s rbacService, lazy rbac.ServiceLazy) error {
			return lazy.SetService(s)
		}),
	)
}

// rbacService is a distinct type from rbac.Service so that the concrete service
// and the lazy indirection can coexist in the graph without fx resolving one to
// the other. Same purpose as next's unexported `service` alias.
type rbacService rbac.Service

func newRBACService(
	dao database.DaoTransactionProvider,
	objectActions rbac.ObjectActionProvider,
	permissions rbac.PermissionProvider,
	ttl rbac.CacheTTL,
) rbacService {
	return rbac.NewService(
		rbac.NewRepository(dao),
		objectActions,
		permissions,
		ttl,
	)
}

func newUserService(
	dao database.DaoTransactionProvider,
	jwtService jwt.Service,
	c authconfig.Config,
) user.Service {
	v := c.UserValues()

	return user.NewService(
		dao,
		jwtService,
		v.InvitationRequired,
		v.EmailRequired,
		v.EmailVerification,
		v.PasswordMinEntropy,
		v.PasswordHashingCost,
		v.LoginRequestsPerMinute,
		v.LoginRequestBurst,
	)
}
