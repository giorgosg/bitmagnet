package api_key_test

import (
	"crypto/sha256"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/api_key"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncoding(t *testing.T) {
	t.Parallel()

	secret, err := api_key.NewSecret()
	require.NoError(t, err)

	digest := sha256.Sum256(secret.Secret)
	require.Equal(t, digest[:], secret.Hash)

	apiKeyID := 12345

	keyData := &api_key.KeyData{
		ID:     apiKeyID,
		Secret: secret.Secret,
	}

	encoded := keyData.Encode()
	assert.Len(t, encoded, 22)

	keyData2 := &api_key.KeyData{}
	require.NoError(t, keyData2.Decode(encoded))

	assert.Equal(t, keyData, keyData2)
}
