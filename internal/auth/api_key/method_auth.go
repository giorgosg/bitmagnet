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

	// A key still stored as bcrypt is re-hashed here, having verified: this is
	// the only moment the server holds the plaintext secret, so it is the only
	// place the row can be migrated. It happens once per key.
	//
	// It runs *before* the refusals below, which looks wrong and is not. The
	// point of the rewrite is to take 57 ms of CPU off this row, and a row
	// belonging to a refused credential is precisely one that would otherwise
	// go on paying it - an expired key that an *arr client keeps polling, or
	// the keys of an account that is disabled today and enabled next week. The
	// write grants nothing: it replaces a hash of a secret with another hash of
	// the same secret, and the refusal below is unaffected by it.
	//
	// Deliberately best-effort, and deliberately unlogged, matching how Login
	// treats its last_login_at write: the request has already presented a valid
	// secret, and failing it because this could not be written would make a hot
	// read path depend on a write. The cost of it failing is that the key keeps
	// paying bcrypt until it next verifies.
	if legacyHash {
		_ = s.repository.UpdateHash(ctx, apiKey.ID, hashSecret(keyData.Secret))
	}

	if apiKey.ExpiresAt.Valid && apiKey.ExpiresAt.Time.Before(time.Now()) {
		return model.APIKey{}, fmt.Errorf("%w: %w: %w", Err, ErrAuth, ErrExpired)
	}

	// API keys have no expiry by default, so without this a disabled account's
	// keys would outlive it indefinitely.
	if !apiKey.User.Enabled {
		return model.APIKey{}, fmt.Errorf("%w: %w: %w", Err, ErrAuth, ErrDisabled)
	}

	apiKey.Hash = nil
	apiKey.User.Password = nil

	return apiKey, nil
}
