package user

import (
	"context"
	"errors"
	"fmt"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
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
)

type InitialInvitation struct {
	model.Invitation
	Status InitialInvitationStatus
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

		count, err := tx.WithContext(ctx).User.Where(
			tx.User.RoleName.Eq("admin"),
			tx.User.Enabled.Is(true),
		).Count()
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
			Where(
				tx.Invitation.RoleName.Eq("admin"),
				tx.Invitation.CreatedBy.IsNull(),
				tx.Invitation.ClaimedBy.IsNull(),
				tx.Invitation.ExpiresAt.IsNull(),
			).
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
			RoleName: "admin",
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
