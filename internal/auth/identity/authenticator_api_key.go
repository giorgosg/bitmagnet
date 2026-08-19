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

	anon, err := a.rbac.GetRole(ctx, rbac.RoleAnon)
	if err != nil {
		return nil, true, err
	}

	return APIKey{
		APIKey:   apiKey,
		anon:     anon,
		enforcer: a.rbac,
	}, true, nil
}
