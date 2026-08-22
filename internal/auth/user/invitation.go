package user

import (
	"context"
	"errors"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"gorm.io/gorm"
)

// checkInvitation resolves an invitation code and reports whether it may still
// be claimed. It is deliberately usable against either the plain dao or an open
// transaction, because registration checks a code twice: once cheaply, before
// committing to a bcrypt hash, and once inside the transaction that claims it.
//
// Errors the caller caused are returned as the bare sentinels, so the two call
// sites can tell them apart from an infrastructure failure with
// isInvitationUserError and wrap them accordingly.
func checkInvitation(ctx context.Context, q *dao.Query, code string) (*model.Invitation, error) {
	invitation, err := q.WithContext(ctx).Invitation.
		Where(q.Invitation.Code.Eq(code)).
		First()
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInvitationNotFound
		}

		return nil, err
	}

	if invitation.ClaimedBy.Valid {
		return nil, ErrInvitationClaimed
	}

	// Expired means the expiry is in the past. The comparison was inverted,
	// which both accepted expired invitations and refused valid ones;
	// api_key.Auth gets the same test right.
	if invitation.ExpiresAt.Valid && invitation.ExpiresAt.Time.Before(time.Now()) {
		return nil, ErrInvitationExpired
	}

	return invitation, nil
}

// isInvitationUserError distinguishes a refusal the caller can act on from a
// database failure they cannot.
func isInvitationUserError(err error) bool {
	return errors.Is(err, ErrInvitationNotFound) ||
		errors.Is(err, ErrInvitationClaimed) ||
		errors.Is(err, ErrInvitationExpired)
}
