package api_key_test

import (
	"crypto/sha256"
	"database/sql"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/api_key"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// storedKey is a row as the repository would return it: an enabled owner and no
// expiry, so the only thing under test is the hash comparison.
func storedKey(id int, hash []byte) model.APIKey {
	return model.APIKey{
		ID:   id,
		Name: "test",
		Hash: hash,
		User: model.User{ID: 1, Username: "owner", Enabled: true},
	}
}

func encodedKey(t *testing.T, id int, secret []byte) string {
	t.Helper()

	return api_key.KeyData{ID: id, Secret: secret}.Encode()
}

// fixedSecret and its digest are spelled out rather than taken from NewSecret,
// so these tests state the stored format independently of the code that
// produces it.
func fixedSecret() []byte {
	return []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
}

func sha256Hash(secret []byte) []byte {
	sum := sha256.Sum256(secret)

	return sum[:]
}

// The hash a key is stored under must be a plain SHA-256 of the secret. The
// secret is 12 bytes from crypto/rand, so there is no offline search to slow
// down, and bcrypt's cost was being paid on every request that presented a key
// - before any authorization check, on an unauthenticated caller's input.
func TestNewSecretHashesWithSHA256(t *testing.T) {
	t.Parallel()

	secret, err := api_key.NewSecret()
	require.NoError(t, err)

	want := sha256.Sum256(secret.Secret)
	assert.Equal(t, want[:], secret.Hash)
	// The stored width is what tells the two hash formats apart on the read
	// path, so it is part of the contract rather than an implementation detail.
	assert.Len(t, secret.Hash, sha256.Size)
	assert.NotEqual(t, 60, len(secret.Hash), "60 bytes is a bcrypt hash")
}

func TestAuthAcceptsAKeyStoredAsSHA256(t *testing.T) {
	t.Parallel()

	h := newTestHarness(t)

	secret := fixedSecret()

	h.repository.EXPECT().Get(t.Context(), 7).Return(storedKey(7, sha256Hash(secret)), nil).Once()

	apiKey, err := h.service.Auth(t.Context(), encodedKey(t, 7, secret))
	require.NoError(t, err)
	assert.Equal(t, 7, apiKey.ID)
	assert.Nil(t, apiKey.Hash, "the hash must not be returned to the caller")
}

func TestAuthRejectsAWrongSecret(t *testing.T) {
	t.Parallel()

	h := newTestHarness(t)

	secret := fixedSecret()

	wrong := fixedSecret()
	wrong[0] ^= 0xff

	h.repository.EXPECT().Get(t.Context(), 7).Return(storedKey(7, sha256Hash(secret)), nil).Once()

	_, err := h.service.Auth(t.Context(), encodedKey(t, 7, wrong))
	require.Error(t, err)
	assert.ErrorIs(t, err, api_key.ErrMismatch)
}

// A row whose hash is the wrong width for either format must fail closed, not
// authenticate. An empty hash is the shape a dropped error used to store.
func TestAuthRejectsAHashOfNeitherFormat(t *testing.T) {
	t.Parallel()

	for name, hash := range map[string][]byte{
		"empty":     {},
		"truncated": sha256Hash(fixedSecret())[:16],
		"nonsense":  []byte("not a hash"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newTestHarness(t)

			h.repository.EXPECT().Get(t.Context(), 7).Return(storedKey(7, hash), nil).Once()

			_, err := h.service.Auth(t.Context(), encodedKey(t, 7, fixedSecret()))
			require.Error(t, err)
			require.ErrorIs(t, err, api_key.ErrMismatch)
			h.repository.AssertNotCalled(t, "UpdateHash")
		})
	}
}

// Keys minted before the hash changed are stored as bcrypt and must keep
// working: there is no migration, because recomputing a SHA-256 needs the
// plaintext secret, which the server does not hold.
func TestAuthStillAcceptsALegacyBcryptHash(t *testing.T) {
	t.Parallel()

	h := newTestHarness(t)

	secret, err := api_key.NewSecret()
	require.NoError(t, err)

	legacy, err := bcrypt.GenerateFromPassword(secret.Secret, bcrypt.MinCost)
	require.NoError(t, err)

	h.repository.EXPECT().Get(t.Context(), 7).Return(storedKey(7, legacy), nil).Once()
	h.repository.EXPECT().UpdateHash(t.Context(), 7, secret.Hash).Return(nil).Once()

	apiKey, err := h.service.Auth(t.Context(), encodedKey(t, 7, secret.Secret))
	require.NoError(t, err)
	assert.Equal(t, 7, apiKey.ID)
}

func TestAuthRejectsAWrongSecretAgainstALegacyBcryptHash(t *testing.T) {
	t.Parallel()

	h := newTestHarness(t)

	secret, err := api_key.NewSecret()
	require.NoError(t, err)

	legacy, err := bcrypt.GenerateFromPassword(secret.Secret, bcrypt.MinCost)
	require.NoError(t, err)

	wrong := make([]byte, len(secret.Secret))
	copy(wrong, secret.Secret)
	wrong[0] ^= 0xff

	h.repository.EXPECT().Get(t.Context(), 7).Return(storedKey(7, legacy), nil).Once()

	_, err = h.service.Auth(t.Context(), encodedKey(t, 7, wrong))
	require.Error(t, err)
	require.ErrorIs(t, err, api_key.ErrMismatch)
	h.repository.AssertNotCalled(t, "UpdateHash")
}

// A row whose hash is already SHA-256 must not be written on every request.
func TestAuthDoesNotRewriteAHashThatIsAlreadySHA256(t *testing.T) {
	t.Parallel()

	h := newTestHarness(t)

	secret, err := api_key.NewSecret()
	require.NoError(t, err)

	h.repository.EXPECT().Get(t.Context(), 7).Return(storedKey(7, secret.Hash), nil).Once()

	_, err = h.service.Auth(t.Context(), encodedKey(t, 7, secret.Secret))
	require.NoError(t, err)
	h.repository.AssertNotCalled(t, "UpdateHash")
}

// The upgrade is opportunistic. A key that verifies must authenticate even if
// the row cannot be rewritten - the alternative is an outage on the read path
// for the sake of an optimisation.
func TestAuthSucceedsWhenTheUpgradeWriteFails(t *testing.T) {
	t.Parallel()

	h := newTestHarness(t)

	secret, err := api_key.NewSecret()
	require.NoError(t, err)

	legacy, err := bcrypt.GenerateFromPassword(secret.Secret, bcrypt.MinCost)
	require.NoError(t, err)

	h.repository.EXPECT().Get(t.Context(), 7).Return(storedKey(7, legacy), nil).Once()
	h.repository.EXPECT().
		UpdateHash(t.Context(), 7, secret.Hash).
		Return(assert.AnError).
		Once()

	apiKey, err := h.service.Auth(t.Context(), encodedKey(t, 7, secret.Secret))
	require.NoError(t, err)
	assert.Equal(t, 7, apiKey.ID)
}

// A refused credential's row is still re-hashed, and the refusal still stands.
//
// This is the case the first version of this change got backwards. Upgrading
// only after the expiry and enabled checks looks tidier - no write on behalf of
// a credential that cannot be used - but those are exactly the rows that would
// otherwise go on paying 57 ms per attempt indefinitely: an expired key an
// *arr client keeps polling, or the keys of an account disabled today and
// enabled next week. The write grants nothing, because it replaces a hash of a
// secret with another hash of the same secret.
func TestAuthUpgradesARefusedKeyAndStillRefusesIt(t *testing.T) {
	t.Parallel()

	secret, err := api_key.NewSecret()
	require.NoError(t, err)

	legacy, err := bcrypt.GenerateFromPassword(secret.Secret, bcrypt.MinCost)
	require.NoError(t, err)

	for name, tt := range map[string]struct {
		mutate  func(*model.APIKey)
		wantErr error
	}{
		"expired": {
			mutate: func(k *model.APIKey) {
				k.ExpiresAt = sql.NullTime{Time: time.Now().Add(-time.Hour), Valid: true}
			},
			wantErr: api_key.ErrExpired,
		},
		"owner disabled": {
			mutate:  func(k *model.APIKey) { k.User.Enabled = false },
			wantErr: api_key.ErrDisabled,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newTestHarness(t)

			key := storedKey(7, legacy)
			tt.mutate(&key)

			h.repository.EXPECT().Get(t.Context(), 7).Return(key, nil).Once()
			h.repository.EXPECT().UpdateHash(t.Context(), 7, secret.Hash).Return(nil).Once()

			_, err := h.service.Auth(t.Context(), encodedKey(t, 7, secret.Secret))
			require.Error(t, err)
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}
