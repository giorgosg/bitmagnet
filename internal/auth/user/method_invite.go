package user

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
)

type InviteRequest struct {
	Email     string
	Role      string
	CreatedBy int
	Expiry    time.Duration
}

func (s *service) Invite(ctx context.Context, request InviteRequest) (model.Invitation, error) {
	// InviteInput.role is optional. Registration defaults to the same role when
	// no invitation names one, and invitations.role_name carries that default in
	// the schema; supplying it here makes the name concrete before it is checked,
	// so validation never has to reason about the empty string.
	roleName := request.Role
	if roleName == "" {
		roleName = rbac.RoleUser.String()
	}

	invitation := model.Invitation{
		RoleName: roleName,
	}

	if request.Email != "" {
		if !regexEmail.MatchString(request.Email) {
			return model.Invitation{}, fmt.Errorf("%w: %w: %w", Err, ErrInvite, ErrEmailInvalid)
		}

		invitation.Email = model.NewNullString(request.Email)
	}

	if request.CreatedBy > 0 {
		invitation.CreatedBy = model.NewNullInt(request.CreatedBy)
	}

	if request.Expiry > 0 {
		invitation.ExpiresAt = sql.NullTime{
			Time:  time.Now().Add(request.Expiry),
			Valid: true,
		}
	}

	invitation.Code = newInvitationCode()

	var userErr error

	err := s.DaoTransaction(func(tx *dao.Query) error {
		// The role is a foreign key to roles(name), so an unknown one was already
		// refused - as a constraint violation naming a column, which the GraphQL
		// boundary can only present as INTERNAL_SERVER_ERROR. Checking it here
		// makes a caller's typo a caller error with a code. SetRole does the same
		// for the same reason.
		count, err := tx.WithContext(ctx).Role.Where(tx.Role.Name.Eq(invitation.RoleName)).Count()
		if err != nil {
			return err
		}

		if count == 0 {
			userErr = ErrRoleNotFound

			return nil
		}

		err = tx.WithContext(ctx).Invitation.Create(&invitation)
		if err != nil {
			return err
		}

		if request.CreatedBy > 0 {
			createdBy, err := tx.WithContext(ctx).User.Where(tx.User.ID.Eq(request.CreatedBy)).First()
			if err != nil {
				return err
			}

			invitation.CreatedByUser = *createdBy
		}

		return nil
	})
	if err != nil {
		return model.Invitation{}, fmt.Errorf("%w: %w: %w: %w", Err, ErrInvite, ErrTransaction, err)
	}

	if userErr != nil {
		return model.Invitation{}, fmt.Errorf("%w: %w: %w", Err, ErrInvite, userErr)
	}

	return invitation, nil
}
