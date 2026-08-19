package user

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type RegisterRequest struct {
	Username       string
	Password       string
	Email          string
	InvitationCode string
}

func (s *service) Register(ctx context.Context, request RegisterRequest) (model.User, error) {
	if !regexUsername.MatchString(request.Username) {
		return model.User{}, fmt.Errorf(
			"%w: %w: %w: must match %s",
			Err,
			ErrRegister,
			ErrUsernameInvalid,
			regexUsername.String(),
		)
	}

	user := model.User{
		Username:  request.Username,
		RoleName:  rbac.RoleUser.String(),
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if request.InvitationCode == "" && s.invitationRequired.Get() {
		return model.User{}, ErrInvitationCodeMissing
	}

	if request.Email == "" && s.emailRequired.Get() {
		return model.User{}, fmt.Errorf("%w: %w: %w", Err, ErrRegister, ErrEmailMissing)
	}

	if request.Email != "" {
		if !regexEmail.MatchString(request.Email) {
			return model.User{}, fmt.Errorf("%w: %w: %w", Err, ErrRegister, ErrEmailInvalid)
		}

		user.Email = model.NewNullString(request.Email)
	}

	if !s.PasswordEntropy(request.Password).Valid {
		return model.User{}, fmt.Errorf("%w: %w: %w", Err, ErrRegister, ErrPasswordInsufficientEntropy)
	}

	hashedPassword, err := bcrypt.GenerateFromPassword(
		[]byte(request.Password),
		int(s.passwordHashingCost.Get()),
	)
	if err != nil {
		return model.User{}, fmt.Errorf("%w: %w: %w", Err, ErrRegister, err)
	}

	user.Password = hashedPassword

	var errUser error

	errTx := s.DaoTransaction(func(tx *dao.Query) error {
		// Check invitation validity
		if request.InvitationCode != "" {
			invitation, err := tx.WithContext(ctx).Invitation.
				Where(tx.Invitation.Code.Eq(request.InvitationCode)).
				First()
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					errUser = ErrInvitationNotFound

					return nil
				}

				return err
			}

			if invitation.ClaimedBy.Valid {
				errUser = ErrInvitationClaimed
				return nil
			}

			// Expired means the expiry is in the past. The comparison was
			// inverted, which both accepted expired invitations and refused
			// valid ones; api_key.Auth gets the same test right.
			if invitation.ExpiresAt.Valid && invitation.ExpiresAt.Time.Before(time.Now()) {
				errUser = ErrInvitationExpired
				return nil
			}

			user.RoleName = invitation.RoleName
		}

		// Check if user already exists
		if existing, err := tx.WithContext(ctx).
			User.Where(tx.User.Username.Eq(request.Username)).
			Count(); err != nil {
			return err
		} else if existing > 0 {
			errUser = ErrAlreadyExists

			return nil
		}

		err := tx.WithContext(ctx).User.Create(&user)
		if err != nil {
			return err
		}

		if request.InvitationCode != "" {
			// Claim atomically. The check above is a plain read, so concurrent
			// registrations can all observe the invitation as unclaimed; an
			// unconditional update then lets every one of them through, with
			// the last writer simply overwriting the rest. Predicating the
			// update on the code still being unclaimed makes exactly one win.
			result, err := tx.WithContext(ctx).
				Invitation.
				Where(
					tx.Invitation.Code.Eq(request.InvitationCode),
					tx.Invitation.ClaimedBy.IsNull(),
				).
				UpdateColumn(tx.Invitation.ClaimedBy, user.ID)
			if err != nil {
				return err
			}

			if result.RowsAffected == 0 {
				// Lost the race. Roll back, so the user is not created against
				// an invitation someone else claimed.
				errUser = ErrInvitationClaimed

				return errInvitationRace
			}
		}

		return nil
	})
	if errTx != nil && !errors.Is(errTx, errInvitationRace) {
		return model.User{}, fmt.Errorf("%w: %w: %w: %w", Err, ErrRegister, ErrTransaction, errTx)
	} else if errUser != nil {
		return model.User{}, fmt.Errorf("%w: %w: %w", Err, ErrRegister, errUser)
	}

	// Clear password from response
	user.Password = nil

	return user, nil
}

// errInvitationRace rolls the registration back when another caller claimed the
// invitation first. It never escapes this file: the caller sees ErrInvitationClaimed.
var errInvitationRace = errors.New("invitation claimed concurrently")
