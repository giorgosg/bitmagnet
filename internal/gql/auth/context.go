package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/http_auth"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/identity"
	internal_httpserver "github.com/bitmagnet-io/bitmagnet/internal/httpserver"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
)

func IdentityFromContext(ctx context.Context) (identity.Identity, bool) {
	ginCtx, ok := internal_httpserver.GinContextFromContext(ctx)
	if !ok {
		return nil, false
	}

	return http_auth.GetIdentity(ginCtx)
}

func AuthenticationErrorFromContext(ctx context.Context) error {
	ginCtx, ok := internal_httpserver.GinContextFromContext(ctx)
	if !ok {
		return nil
	}

	resolution, ok := http_auth.GetResolution(ginCtx)
	if !ok {
		return nil
	}

	if resolution.Err == nil {
		return nil
	}

	return fmt.Errorf("%w: %w", ErrAuthenticationInfrastructure, resolution.Err)
}

// ErrAuthenticationInfrastructure marks a failure to resolve the request's
// credential because an authentication dependency could not answer. The
// underlying cause remains available to server-side error handling through
// errors.Is/errors.As, but the GraphQL boundary presents a stable public error.
var ErrAuthenticationInfrastructure = errors.New("authentication infrastructure failure")

func UserFromContext(ctx context.Context) (model.User, bool) {
	identity, ok := IdentityFromContext(ctx)
	if !ok {
		return model.User{}, false
	}

	self := identity.Self()

	if self.User == nil {
		return model.User{}, false
	}

	return *self.User, true
}

// ErrNotAuthenticated is returned when the request carries no user identity.
var ErrNotAuthenticated = errors.New("not authenticated")

// ErrAPIKeyMayNotManageKeys is returned when a request authenticated by an API
// key attempts to manage API keys.
var ErrAPIKeyMayNotManageKeys = errors.New("api keys may not manage api keys")

// UserSessionFromContext resolves the user only for an interactive session,
// refusing identities that are themselves backed by an API key.
//
// An API-key identity reports its owning user, and the top-level self boundary
// is deliberately reachable without a Role Permission. Without this distinction
// a key scoped to one narrow object action can call createAPIKey and mint a second
// key naming any registered object action, bounded only by the owner's role. Key
// management is an account operation and requires an account session.
func UserSessionFromContext(ctx context.Context) (model.User, error) {
	identity, ok := IdentityFromContext(ctx)
	if !ok {
		return model.User{}, ErrNotAuthenticated
	}

	self := identity.Self()

	if self.APIKey != nil {
		return model.User{}, ErrAPIKeyMayNotManageKeys
	}

	if self.User == nil {
		return model.User{}, ErrNotAuthenticated
	}

	return *self.User, nil
}
