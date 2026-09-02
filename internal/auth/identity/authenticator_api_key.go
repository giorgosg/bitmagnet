package identity

import (
	"context"
	"errors"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/api_key"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
)

type authenticatorAPIKey struct {
	apiKeyService api_key.Service
	rbac          rbac.Service
}

// revokedAPIKey reports whether err means "this is not a usable credential", as
// opposed to "the credential could not be checked".
//
// The distinction is the chain's central invariant: a revoked credential reports
// no match and leaves the caller anonymous, while an infrastructure failure
// reports a match and an error, so a database outage can never silently downgrade
// a session instead of failing the request. Only ErrDecode used to fall through,
// which meant an expired key aborted the chain and the request carried no
// identity at all — every field refused, including the ones a client needs to be
// told its key is dead.
func revokedAPIKey(err error) bool {
	for _, revoked := range []error{
		api_key.ErrDecode,
		api_key.ErrNotFound,
		api_key.ErrMismatch,
		api_key.ErrExpired,
		api_key.ErrDisabled,
	} {
		if errors.Is(err, revoked) {
			return true
		}
	}

	return false
}

func (a authenticatorAPIKey) Authenticate(ctx context.Context, token string) (Identity, bool, error) {
	apiKey, err := a.apiKeyService.Auth(ctx, token)
	if err != nil {
		return nil, !revokedAPIKey(err), err
	}

	// Both roles in one lookup. The key's reported permissions are its selection
	// narrowed by the owning User's Role, so the Role is needed as well as anon
	// - and GetRoles answers both from the same cached snapshot rather than
	// taking it twice.
	roles, err := a.rbac.GetRoles(ctx, []rbac.Role{rbac.RoleAnon, rbac.Role(apiKey.User.RoleName)})
	if err != nil {
		return nil, true, err
	}

	identity := APIKey{
		APIKey:   apiKey,
		enforcer: a.rbac,
	}

	for _, role := range roles {
		if role.Role == rbac.RoleAnon {
			identity.anon = role
		}

		if role.Role == rbac.Role(apiKey.User.RoleName) {
			identity.userRole = role
		}
	}

	return identity, true, nil
}
