package api_key

import (
	"context"
	"fmt"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/model"
)

func (s service) Auth(ctx context.Context, key string) (model.APIKey, error) {
	keyData := &KeyData{}

	if err := keyData.Decode(key); err != nil {
		return model.APIKey{}, fmt.Errorf("%w: %w: %w: %w", Err, ErrAuth, ErrDecode, err)
	}

	apiKey, err := s.repository.Get(ctx, keyData.ID)

	legacyHash := false

	if err == nil {
		var verified bool

		verified, legacyHash = verifySecret(apiKey.Hash, keyData.Secret)
		if !verified {
			err = ErrMismatch
		}
	}

	if err != nil {
		return model.APIKey{}, fmt.Errorf("%w: %w: %w", Err, ErrAuth, err)
	}

	if apiKey.ExpiresAt.Valid && apiKey.ExpiresAt.Time.Before(time.Now()) {
		return model.APIKey{}, fmt.Errorf("%w: %w: %w", Err, ErrAuth, ErrExpired)
	}

	// API keys have no expiry by default, so without this a disabled account's
	// keys would outlive it indefinitely.
	if !apiKey.User.Enabled {
		return model.APIKey{}, fmt.Errorf("%w: %w: %w", Err, ErrAuth, ErrDisabled)
	}

	// A key still stored as bcrypt is re-hashed here, having verified: this is
	// the only moment the server holds the plaintext secret, so it is the only
	// place the row can be migrated. It happens once per key, and after the
	// expiry and enabled checks, so no write is made for a credential that
	// cannot be used.
	//
	// Deliberately best-effort, and deliberately unlogged, matching how Login
	// treats its last_login_at write: the request has already authenticated,
	// and failing it because this could not be written would make a hot read
	// path depend on a write. The cost of it failing is that the key keeps
	// paying bcrypt until it next verifies.
	if legacyHash {
		_ = s.repository.UpdateHash(ctx, apiKey.ID, hashSecret(keyData.Secret))
	}

	apiKey.Hash = nil
	apiKey.User.Password = nil

	return apiKey, nil
}
