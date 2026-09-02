package api_key

import (
	"context"
	"fmt"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
)

type CreateRequest struct {
	UserID      int
	Name        string
	Permissions []rbac.ObjectAction
	Expiry      time.Duration
}

type CreateResult struct {
	ID        int
	APIKey    string
	Name      string
	ExpiresAt time.Time
}

func (s service) Create(ctx context.Context, req CreateRequest) (CreateResult, error) {
	if err := s.validatePermissions(req.Permissions); err != nil {
		return CreateResult{}, fmt.Errorf("%w: %w: %w", Err, ErrCreate, err)
	}

	secret, err := NewSecret()
	if err != nil {
		return CreateResult{}, fmt.Errorf("%w: %w: %w", Err, ErrCreate, err)
	}

	var expiresAt time.Time
	if req.Expiry > 0 {
		expiresAt = time.Now().Add(req.Expiry)
	}

	apiKeyID, err := s.repository.Create(ctx, req.UserID, req.Name, secret.Hash, req.Permissions, expiresAt)
	if err != nil {
		return CreateResult{}, fmt.Errorf("%w: %w: %w", Err, ErrCreate, err)
	}

	return CreateResult{
		ID: apiKeyID,
		APIKey: KeyData{
			ID:     apiKeyID,
			Secret: secret.Secret,
		}.Encode(),
		Name:      req.Name,
		ExpiresAt: expiresAt,
	}, nil
}

// validatePermissions requires every requested object action to be one the
// application actually registered.
//
// api_key_permissions has no foreign key to a registry, because there is no
// registry table to point at, so the repository stores whatever it is handed.
// Two things followed from that. A typo'd action produced a key granting
// nothing, reported as success and indistinguishable through the API from a
// working one. And a wildcard in a key's own selection is a casbin request
// subject matched by globMatch, so the key's half of the enforcement decision
// degenerated to "anything" - contained only by the other half still requiring
// the owning User's Role.
//
// Exact membership answers both: no registered action is a glob pattern.
func (s service) validatePermissions(permissions []rbac.ObjectAction) error {
	if len(permissions) == 0 {
		return nil
	}

	registered := make(map[rbac.ObjectAction]struct{})
	for _, objectAction := range s.objectActions() {
		registered[objectAction] = struct{}{}
	}

	for _, requested := range permissions {
		if _, ok := registered[requested]; !ok {
			return fmt.Errorf("%w: %s::%s::%s",
				ErrPermissionInvalid, requested.Namespace, requested.Object, requested.Action)
		}
	}

	return nil
}
