package identity_test

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/api_key"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// API key secrets are stored as a plain SHA-256; keys minted before that was
// true are stored as bcrypt, and there is no migration for them, because a
// bcrypt hash cannot be turned into a SHA-256 of the same secret without the
// plaintext. So a legacy row is rewritten the first time it verifies.
//
// This is the half of that path the mocked unit tests cannot reach: that the
// UPDATE actually lands on a bytea column, and that what it writes verifies
// afterwards. The write is best-effort and unlogged, so a silent failure here
// would leave every legacy key paying bcrypt forever with nothing to say so.
func TestLegacyBcryptAPIKeyIsRehashedWhenItVerifies(t *testing.T) {
	t.Parallel()

	stack := newScopeStack(t)
	admin := stack.registerAdmin(t)

	secret, err := api_key.NewSecret()
	require.NoError(t, err)

	legacyHash, err := bcrypt.GenerateFromPassword(secret.Secret, bcrypt.MinCost)
	require.NoError(t, err)
	require.Len(t, legacyHash, 60, "a bcrypt hash is what this test is about")

	id, err := stack.apiKeyRepository.Create(
		t.Context(),
		admin.ID,
		"legacy",
		legacyHash,
		[]rbac.ObjectAction{scopeTestObjectAction},
		time.Time{},
	)
	require.NoError(t, err)

	key := api_key.KeyData{ID: id, Secret: secret.Secret}.Encode()

	resolved, matched, err := stack.authenticator.Authenticate(t.Context(), key)
	require.NoError(t, err)
	require.True(t, matched)
	require.NotNil(t, resolved.Self().APIKey, "a legacy key must still authenticate")

	stored, err := stack.query.WithContext(t.Context()).APIKey.
		Where(stack.query.APIKey.ID.Eq(id)).
		First()
	require.NoError(t, err)

	digest := sha256.Sum256(secret.Secret)
	assert.Equal(t, digest[:], stored.Hash, "the row must have been rewritten as SHA-256")

	// The rewritten hash has to be one the read path accepts, or the upgrade
	// would authenticate once and lock the key out afterwards.
	resolved, matched, err = stack.authenticator.Authenticate(t.Context(), key)
	require.NoError(t, err)
	require.True(t, matched)
	require.NotNil(t, resolved.Self().APIKey, "the rewritten hash must verify")
}

// A wrong secret must not rewrite anything. It is also the attack this change
// exists to stop - a valid key id with a varying wrong secret - so the row it
// aims at must be left exactly as it was found.
func TestWrongSecretDoesNotRehashALegacyAPIKey(t *testing.T) {
	t.Parallel()

	stack := newScopeStack(t)
	admin := stack.registerAdmin(t)

	secret, err := api_key.NewSecret()
	require.NoError(t, err)

	legacyHash, err := bcrypt.GenerateFromPassword(secret.Secret, bcrypt.MinCost)
	require.NoError(t, err)

	id, err := stack.apiKeyRepository.Create(
		t.Context(), admin.ID, "legacy", legacyHash, nil, time.Time{},
	)
	require.NoError(t, err)

	wrong := make([]byte, len(secret.Secret))
	copy(wrong, secret.Secret)
	wrong[0] ^= 0xff

	resolved, matched, err := stack.authenticator.Authenticate(
		t.Context(),
		api_key.KeyData{ID: id, Secret: wrong}.Encode(),
	)
	require.NoError(t, err, "a wrong secret must fall through to anonymous, not error")
	require.True(t, matched)
	assert.Nil(t, resolved.Self().APIKey)

	stored, err := stack.query.WithContext(t.Context()).APIKey.
		Where(stack.query.APIKey.ID.Eq(id)).
		First()
	require.NoError(t, err)
	assert.Equal(t, legacyHash, stored.Hash, "the stored hash must be untouched")
}
