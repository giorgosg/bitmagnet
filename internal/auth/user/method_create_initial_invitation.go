package user

import (
	"context"
	"errors"
	"fmt"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"gorm.io/gen"
	"gorm.io/gorm"
)

// initialInvitationLockKey names the advisory lock guarding first-run
// bootstrap. Postgres advisory locks share one namespace per database, so the
// value is an arbitrary but fixed constant that nothing else in the schema may
// reuse.
const initialInvitationLockKey = 8021975263410019

type InitialInvitationStatus int

const (
	InitialInvitationNotRequired InitialInvitationStatus = iota
	InitialInvitationCreated
	InitialInvitationUnclaimed
	// InitialInvitationNone reports that no administrator exists and no
	// bootstrap invitation is outstanding. CreateInitialInvitation never
	// returns it, because it creates one instead; GetInitialInvitation does,
	// because it may not.
	InitialInvitationNone
)

type InitialInvitation struct {
	model.Invitation
	Status InitialInvitationStatus
}

// enabledAdminConditions and bootstrapInvitationConditions are the two queries
// that decide whether a first administrator is still outstanding. Both the
// create path and the read path ask exactly these questions, and they must keep
// asking the same ones: a predicate that drifts on one side makes the retrieval
// command answer about a different invitation than the one the boot hook
// issued. They were copies for one commit and had already diverged -- one
// spelled the role `rbac.RoleAdmin.String()` and the other the literal "admin".
func enabledAdminConditions(q *dao.Query) []gen.Condition {
	return []gen.Condition{
		q.User.RoleName.Eq(rbac.RoleAdmin.String()),
		q.User.Enabled.Is(true),
	}
}

// Attribution and expiry are what separate the bootstrap invitation from an
// admin invitation somebody issued through the API: that one belongs to whoever
// created it and may expire, and handing it out would disclose a third party's
// credential.
func bootstrapInvitationConditions(q *dao.Query) []gen.Condition {
	return []gen.Condition{
		q.Invitation.RoleName.Eq(rbac.RoleAdmin.String()),
		q.Invitation.CreatedBy.IsNull(),
		q.Invitation.ClaimedBy.IsNull(),
		q.Invitation.ExpiresAt.IsNull(),
	}
}

func (s *service) CreateInitialInvitation(ctx context.Context) (InitialInvitation, error) {
	var initialInvitation InitialInvitation

	err := s.DaoTransaction(func(tx *dao.Query) error {
		// Everything below is a check followed by an insert, and bitmagnet is
		// routinely run as more than one process against one database. Without
		// serialization every replica reads the same empty state and inserts its
		// own code: a synchronized 16-replica start produced 16 distinct,
		// non-expiring administrator invitations, each a permanent path to an
		// admin account.
		//
		// The lock is held to the end of the transaction and released on commit
		// or rollback, so a crashed replica cannot wedge the next start. It has
		// to be a database lock rather than a mutex, because the processes
		// racing here do not share memory.
		if err := tx.Invitation.WithContext(ctx).UnderlyingDB().
			Exec("SELECT pg_advisory_xact_lock(?)", initialInvitationLockKey).Error; err != nil {
			return err
		}

		count, err := tx.WithContext(ctx).User.Where(enabledAdminConditions(tx)...).Count()
		if err != nil {
			return err
		}

		if count > 0 {
			// we already have an admin user
			initialInvitation.Status = InitialInvitationNotRequired
			return nil
		}

		invitation, err := tx.WithContext(ctx).
			Invitation.
			Where(bootstrapInvitationConditions(tx)...).
			First()
		if err == nil {
			initialInvitation.Invitation = *invitation
			initialInvitation.Status = InitialInvitationUnclaimed

			return nil
		}

		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		invitation = &model.Invitation{
			Code:     newInvitationCode(),
			RoleName: rbac.RoleAdmin.String(),
		}

		err = tx.WithContext(ctx).Invitation.Create(invitation)
		if err != nil {
			return err
		}

		initialInvitation.Invitation = *invitation
		initialInvitation.Status = InitialInvitationCreated

		return nil
	})
	if err != nil {
		return InitialInvitation{}, fmt.Errorf("%w: %w: %w", Err, ErrInitialInvitation, err)
	}

	return initialInvitation, nil
}
