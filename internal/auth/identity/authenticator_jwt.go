package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/jwt"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/user"
)

type authenticatorJWT struct {
	jwtService  jwt.Service
	userService user.Service
	rbac        rbac.Service
}

func (a authenticatorJWT) Authenticate(ctx context.Context, token string) (Identity, bool, error) {
	claims, err := a.jwtService.Parse(token)
	if err != nil {
		return nil, errors.Is(err, jwt.ErrTokenInvalidClaims), err
	}

	usr, err := a.userService.Get(ctx, claims.UserID)
	if err != nil {
		return nil, true, err
	}

	// A token outlives the account state it was issued against, so disabling an
	// account has to be checked here as well as at login — otherwise it revokes
	// nothing until the token expires.
	if !usr.Enabled {
		return nil, true, fmt.Errorf("%w: %w", user.Err, user.ErrDisabled)
	}

	role, err := a.rbac.GetRole(ctx, rbac.Role(usr.RoleName))
	if err != nil {
		return nil, true, err
	}

	return User{
		User:     usr,
		role:     role,
		enforcer: a.rbac,
	}, true, nil
}
