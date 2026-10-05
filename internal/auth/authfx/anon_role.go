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

// anonRoleTranslationKey marks that this installation's `auth.anonymous_access`
// has been written into the anon role's stored permissions. It lives in
// key_values, which has existed since migration 00009 and which no other
// application code reads or writes.
const anonRoleTranslationKey = "auth.anon_role_translated"

type anonRoleParams struct {
	fx.In
	Dao           database.DaoTransactionProvider
	Config        authconfig.Config
	ObjectActions rbac.ObjectActionProvider
	Logger        *zap.SugaredLogger
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

func anonRoleHook(p anonRoleParams) fx.Hook {
	return fx.Hook{
		OnStart: func(ctx context.Context) error {
			return translateAnonRole(ctx, p)
		},
	}
}

// translateAnonRole writes the current meaning of `auth.anonymous_access` into
// the anon role's stored permissions, exactly once per installation.
//
// It exists so that the flag can stop being a source of grants without changing
// anybody's behaviour. An installation that was open keeps its anonymous reads
// because they are now rows; one that was closed stays closed because nothing is
// written. Neither is flipped silently.
//
// Everything happens in one transaction, so a failure part-way through leaves no
// marker and the next start tries again rather than leaving a half-seeded role.
func translateAnonRole(ctx context.Context, p anonRoleParams) error {
	return TranslateAnonRole(ctx, p.Dao, p.Config, p.ObjectActions, p.Logger)
}

// TranslateAnonRole is translateAnonRole without the fx parameter struct, for a
// stack assembled by hand. internal/dev/fixtureserver builds the auth services
// directly rather than through this module, and the seed is part of what an
// installation looks like once `auth.anonymous_access` stops granting anything -
// so a fixture that skipped it would test an instance no real deployment is in.
func TranslateAnonRole(
	ctx context.Context,
	provider database.DaoTransactionProvider,
	cfg authconfig.Config,
	objectActions rbac.ObjectActionProvider,
	log *zap.SugaredLogger,
) error {
	logger := log.Named("auth")

	return provider.DaoTransaction(func(tx *dao.Query) error {
		// Idempotency is on the marker alone, never on "does anon hold any rows".
		// Zero rows is a legitimate state an administrator may have chosen, and
		// re-seeding it would hand back access they deliberately revoked.
		marker, err := tx.WithContext(ctx).KeyValue.
			Where(tx.KeyValue.Key.Eq(anonRoleTranslationKey)).
			Count()
		if err != nil {
			return err
		}

		if marker > 0 {
			return nil
		}

		written := 0

		if cfg.AnonymousAccess {
			surface := authconfig.AnonymousReadSurface(objectActions)

			if len(surface) > 0 {
				// The anon role's row is inserted by migration 00022 and DeleteRole
				// refuses core roles, so the foreign key below always has a parent.
				permissions := slice.Map(
					surface,
					func(objectAction rbac.ObjectAction) *model.RolePermission {
						return &model.RolePermission{
							RoleName:  string(rbac.RoleAnon),
							Namespace: objectAction.Namespace,
							Object:    objectAction.Object,
							Action:    objectAction.Action,
						}
					},
				)

				// Insert, not replace: an administrator who granted anon something
				// before upgrading keeps it, and a row the seed also wants is not an
				// error.
				if err = tx.WithContext(ctx).RolePermission.
					Clauses(clause.OnConflict{DoNothing: true}).
					Create(permissions...); err != nil {
					return err
				}

				written = len(permissions)
			}
		}

		if err = tx.WithContext(ctx).KeyValue.Create(&model.KeyValue{
			Key: anonRoleTranslationKey,
			// The flag's value at translation time, which is the one piece of
			// forensics worth keeping: it says which way an installation went.
			Value: strconv.FormatBool(cfg.AnonymousAccess),
		}); err != nil {
			return err
		}

		logger.Infow("translated auth.anonymous_access into the anon role",
			"anonymous_access", cfg.AnonymousAccess,
			"permissions_written", written)

		return nil
	})
}
