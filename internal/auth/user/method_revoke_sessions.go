package user

import (
	"context"
	"fmt"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
)

// RevokeSessions ends every outstanding session for a user by bumping the epoch
// their tokens carry. It is the only revocation available to a stateless JWT:
// the token cannot be recalled, so what changes is the row it is checked against.
//
// It revokes *all* of the user's sessions, not the caller's alone. A per-session
// revocation would need a record per session, and this deliberately does not
// keep one - see the note in docs/architecture/auth.md.
func (s *service) RevokeSessions(ctx context.Context, userID int) error {
	if err := s.DaoTransaction(func(tx *dao.Query) error {
		return revokeSessionsTx(ctx, tx, userID)
	}); err != nil {
		return fmt.Errorf("%w: %w: %w", Err, ErrRevokeSessions, err)
	}

	return nil
}

// revokeSessionsTx bumps the epoch inside a caller's transaction, so a password
// change and the revocation it implies commit together or not at all.
//
// The increment is computed by the database rather than read and written back:
// two concurrent revocations that both read the same value would otherwise write
// the same one, and the second would leave tokens minted against the first still
// valid.
func revokeSessionsTx(ctx context.Context, tx *dao.Query, userID int) error {
	info, err := tx.WithContext(ctx).User.
		Where(tx.User.ID.Eq(userID)).
		UpdateSimple(tx.User.TokenEpoch.Add(1))
	if err != nil {
		return err
	}

	if info.RowsAffected == 0 {
		return ErrNotFound
	}

	return nil
}
