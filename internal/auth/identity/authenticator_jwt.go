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
		// A token this authenticator cannot parse is not its business: report no
		// match so the chain falls through to the anonymous authenticator.
		//
		// Claiming the match instead is a hard lockout. jwt/v5 wraps an expired
		// token in ErrTokenInvalidClaims, so an expired session aborted the
		// chain, left no identity on the request, and had every field refused —
		// including self.login, the one call needed to recover. The only way out
		// was clearing browser storage by hand.
		return nil, false, err
	}

	usr, err := a.userService.Get(ctx, claims.UserID)
	if err != nil {
		// An account that no longer exists is a revoked credential, not a
		// failure to look one up: fall through, exactly as for a token that
		// could not be parsed. Any other error is the database talking, and
		// must not be allowed to silently downgrade a session to anonymous.
		if errors.Is(err, user.ErrNotFound) {
			return nil, false, err
		}

		return nil, true, err
	}

	// A token outlives the account state it was issued against, so disabling an
	// account has to be checked here as well as at login — otherwise it revokes
	// nothing until the token expires.
	//
	// Reporting no match rather than an error is what makes the revocation
	// recoverable. Claiming the match aborts the chain, so no identity reaches
	// the request and self.identity itself answers `unauthorized` — the very
	// query the UI polls to notice its token is dead and clear it. The session
	// stayed wedged, re-sending the revoked token on every reload, until the
	// operator emptied browser storage by hand. Falling through leaves the
	// caller anonymous, which is what a revoked credential should mean.
	if !usr.Enabled {
		return nil, false, fmt.Errorf("%w: %w", user.Err, user.ErrDisabled)
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
