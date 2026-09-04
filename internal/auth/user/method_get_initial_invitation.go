package user

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// GetInitialInvitation reports the outstanding first-administrator invitation
// without ever creating one.
//
// It exists because CreateInitialInvitation is a create-or-return, and an
// operator asking "what is the code?" must not be the same act as issuing one.
// An instance can be deliberately left with no administrator invitation — the
// bootstrap worker only runs when the workers do — and a retrieval path that
// minted on a miss would turn every such instance into one carrying a permanent
// path to an admin account, which is the thing the whole feature is careful
// about.
//
// The four states are the ones an operator actually hits, and each is reported
// rather than collapsed into an empty result:
//
//   - InitialInvitationNotRequired: an enabled administrator already exists.
//   - InitialInvitationUnclaimed: an invitation is outstanding; Code carries it.
//   - InitialInvitationNone: neither exists, so there is nothing to print.
//
// InitialInvitationCreated is never returned; this method does not create.
//
// Unlike the create path there is no advisory lock and no transaction. That lock
// serialises a check followed by an insert across replicas, and there is no
// insert here. Two reads under READ COMMITTED can straddle a concurrent
// registration, but the worst outcome is a report one moment out of date on a
// command the operator can simply run again.
func (s *service) GetInitialInvitation(ctx context.Context) (InitialInvitation, error) {
	q, err := s.Dao()
	if err != nil {
		return InitialInvitation{}, fmt.Errorf("%w: %w: %w", Err, ErrInitialInvitation, err)
	}

	admins, err := q.WithContext(ctx).User.Where(enabledAdminConditions(q)...).Count()
	if err != nil {
		return InitialInvitation{}, fmt.Errorf("%w: %w: %w", Err, ErrInitialInvitation, err)
	}

	if admins > 0 {
		return InitialInvitation{Status: InitialInvitationNotRequired}, nil
	}

	invitation, err := q.WithContext(ctx).
		Invitation.
		Where(bootstrapInvitationConditions(q)...).
		First()
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return InitialInvitation{Status: InitialInvitationNone}, nil
		}

		return InitialInvitation{}, fmt.Errorf("%w: %w: %w", Err, ErrInitialInvitation, err)
	}

	return InitialInvitation{
		Invitation: *invitation,
		Status:     InitialInvitationUnclaimed,
	}, nil
}
