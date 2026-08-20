package identity_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/api_key"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// docs/auth.md states the invariant as: "Every way a credential can fail —
// unparseable, expired, naming a deleted account, naming a disabled one — falls
// through to the anonymous authenticator rather than aborting. Only an
// infrastructure failure [...] produces an error and no identity."
//
// The JWT authenticator was changed to hold that line. The API key authenticator
// still reports a match for every failure except a decode error, so an expired
// key aborts the chain and the request carries no identity at all — every field
// is refused, rather than the caller being told anonymously what they may do.
func TestExpiredAPIKeyFallsBackToAnonymous(t *testing.T) {
	t.Parallel()

	stack := newScopeStack(t)
	admin := stack.registerAdmin(t)

	created, err := stack.apiKeyService.Create(t.Context(), api_key.CreateRequest{
		UserID:      admin.ID,
		Name:        "expiring",
		Permissions: []rbac.ObjectAction{scopeTestObjectAction},
		Expiry:      time.Hour,
	})
	require.NoError(t, err)

	// Move the expiry into the past rather than sleeping.
	_, err = stack.query.WithContext(t.Context()).APIKey.
		Where(stack.query.APIKey.ID.Eq(created.ID)).
		UpdateSimple(stack.query.APIKey.ExpiresAt.Value(sql.NullTime{
			Time:  time.Now().Add(-time.Hour),
			Valid: true,
		}))
	require.NoError(t, err)

	resolved, matched, err := stack.authenticator.Authenticate(t.Context(), created.APIKey)

	require.NoError(t, err, "an expired key must not abort the chain")
	require.True(t, matched, "the anonymous authenticator must still match")
	require.NotNil(t, resolved)
	assert.Nil(t, resolved.Self().APIKey, "an expired key resolves to nobody")
	assert.Nil(t, resolved.Self().User)
}

// A well-formed string that decodes but names no key is the anonymous-caller
// version of the same thing: it is simply not a credential, and must leave the
// caller anonymous rather than failing their request outright.
func TestUnknownAPIKeyFallsBackToAnonymous(t *testing.T) {
	t.Parallel()

	stack := newScopeStack(t)

	// Encoded the way a real key is, so it decodes cleanly — it just names an id
	// no row has. A string of 22 arbitrary characters would not do: the widest
	// base62 values overflow the payload and are rejected as a decode error,
	// which is the one failure that already falls through.
	unknown := api_key.KeyData{
		ID:     999_999,
		Secret: make([]byte, 12),
	}.Encode()

	resolved, matched, err := stack.authenticator.Authenticate(t.Context(), unknown)

	require.NoError(t, err, "an unknown key must not abort the chain")
	require.True(t, matched)
	require.NotNil(t, resolved)
	assert.Nil(t, resolved.Self().APIKey)
	assert.Nil(t, resolved.Self().User)
}
