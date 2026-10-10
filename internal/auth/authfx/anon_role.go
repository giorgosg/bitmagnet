package authfx

import (
	"context"
	"strconv"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/database"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/slice"
	"github.com/bitmagnet-io/bitmagnet/internal/worker"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"gorm.io/gorm/clause"
)

const (
	anonRoleEmptyDefaultKey       = "auth.anon_role_empty_default"
	anonRoleUpgradeLockKey  int64 = 0x616e6f6e5f726f6c
)

type anonRoleParams struct {
	fx.In
	Dao    database.DaoTransactionProvider
	Config authconfig.Config
	Logger *zap.SugaredLogger
}

type anonRoleResult struct {
	fx.Out
	Worker worker.Worker `group:"workers"`
}

func newAnonRoleWorker(p anonRoleParams) anonRoleResult {
	return anonRoleResult{
		Worker: worker.NewWorker("auth_anon_role_translation", anonRoleHook(p)),
	}
}

// The worker registry starts enabled workers in map order, and operators can
// select only http_server. Run the reset inside that worker's startup too, so
// no listener can expose the previous anon grants before the reset commits.
func newAnonRoleHTTPDecorator(p anonRoleParams) worker.Decorator {
	return worker.Decorator{
		Key: "http_server",
		Decorate: func(hook fx.Hook) fx.Hook {
			return fx.Hook{
				OnStart: func(ctx context.Context) error {
					if err := translateAnonRole(ctx, p); err != nil {
						return err
					}

					if hook.OnStart != nil {
						return hook.OnStart(ctx)
					}

					return nil
				},
				OnStop: hook.OnStop,
			}
		},
	}
}

func anonRoleHook(p anonRoleParams) fx.Hook {
	return fx.Hook{
		OnStart: func(ctx context.Context) error {
			return translateAnonRole(ctx, p)
		},
	}
}

// translateAnonRole clears the anon role once per installation. The prior
// automatic read grants and administrator grants share rows, so an upgrade
// cannot identify and remove only the former. Subsequent role edits persist.
func translateAnonRole(ctx context.Context, p anonRoleParams) error {
	return TranslateAnonRole(ctx, p.Dao, p.Config, p.Logger)
}

// TranslateAnonRole runs the same one-time reset for a stack assembled by hand.
func TranslateAnonRole(
	ctx context.Context,
	provider database.DaoTransactionProvider,
	cfg authconfig.Config,
	log *zap.SugaredLogger,
) error {
	logger := log.Named("auth")

	return provider.DaoTransaction(func(tx *dao.Query) error {
		// Replicas must serialize the marker check and deletion. Otherwise a
		// second startup can erase grants made after the first has committed.
		if err := tx.KeyValue.WithContext(ctx).UnderlyingDB().
			Exec("SELECT pg_advisory_xact_lock(?)", anonRoleUpgradeLockKey).Error; err != nil {
			return err
		}

		marker, err := tx.WithContext(ctx).KeyValue.
			Where(tx.KeyValue.Key.Eq(anonRoleEmptyDefaultKey)).
			Count()
		if err != nil {
			return err
		}

		if marker > 0 {
			return nil
		}

		removed, err := tx.WithContext(ctx).RolePermission.
			Where(tx.RolePermission.RoleName.Eq(string(rbac.RoleAnon))).Delete()
		if err != nil {
			return err
		}

		if err = tx.WithContext(ctx).KeyValue.Create(&model.KeyValue{
			Key:   anonRoleEmptyDefaultKey,
			Value: strconv.FormatBool(cfg.AnonymousAccess),
		}); err != nil {
			return err
		}

		message := "anonymous role is empty; grant read actions to allow anonymous browsing"
		logger.Infow(
			message,
			"anonymous_access", cfg.AnonymousAccess,
			"permissions_removed", removed,
		)

		return nil
	})
}

// GrantAnonReadSurface is used by the development fixture when its
// --anonymous-access option is set. Production starts with no anon grants.
func GrantAnonReadSurface(
	ctx context.Context,
	provider database.DaoTransactionProvider,
	objectActions rbac.ObjectActionProvider,
) error {
	surface := authconfig.AnonymousReadSurface(objectActions)
	if len(surface) == 0 {
		return nil
	}

	permissions := slice.Map(surface, func(objectAction rbac.ObjectAction) *model.RolePermission {
		return &model.RolePermission{
			RoleName:  string(rbac.RoleAnon),
			Namespace: objectAction.Namespace,
			Object:    objectAction.Object,
			Action:    objectAction.Action,
		}
	})

	return provider.DaoTransaction(func(tx *dao.Query) error {
		return tx.WithContext(ctx).RolePermission.
			Clauses(clause.OnConflict{DoNothing: true}).
			Create(permissions...)
	})
}
