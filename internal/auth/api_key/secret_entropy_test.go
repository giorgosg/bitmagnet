package api_key

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The secret's width is the whole security argument for storing a fast hash.
//
// bcrypt would have protected a *weak* secret; SHA-256 does not. What makes
// that safe is that no caller supplies the secret and no caller influences its
// length: it is secretLength bytes straight from crypto/rand, so there is no
// search for a work factor to slow down. Shortening it - or ever accepting a
// caller-supplied secret - is a security change, not a parameter change, and
// this test exists to make that argument fail loudly at the place someone would
// make it.
//
// 12 bytes is 96 bits. The reasoning is in docs/architecture/auth.md.
func TestSecretLengthIsPinnedBecauseTheHashIsFast(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 12, secretLength,
		"96 bits of entropy is what lets the stored hash be a plain SHA-256; "+
			"read docs/architecture/auth.md before changing this")
}

// Two secrets in a row must not be equal. This is not a test of crypto/rand but
// of the call to it: an ignored error from rand.Read left the buffer zeroed
// under Go's older signature, which is the defect adr/0001 is about.
func TestNewSecretDoesNotRepeatOrReturnZeroes(t *testing.T) {
	t.Parallel()

	first, err := NewSecret()
	require.NoError(t, err)

	second, err := NewSecret()
	require.NoError(t, err)

	require.Len(t, first.Secret, secretLength)
	assert.NotEqual(t, first.Secret, second.Secret)
	assert.NotEqual(t, make([]byte, secretLength), first.Secret)
}
