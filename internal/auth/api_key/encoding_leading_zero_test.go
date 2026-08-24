package api_key

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// base62 encoding drops leading zero bytes, so a secret beginning with 0x00
// encodes to fewer bytes than it started with. The decoder must restore the
// full payload width, or roughly one generated key in 256 cannot be decoded.
func TestKeyDataRoundTripWithLeadingZeroSecret(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		secret []byte
	}{
		{"leading zero", append([]byte{0x00}, make([]byte, secretLength-1)...)},
		{"two leading zeros", append([]byte{0x00, 0x00}, make([]byte, secretLength-2)...)},
		{"all zeroes", make([]byte, secretLength)},
		{"no leading zero", func() []byte {
			b := make([]byte, secretLength)
			for i := range b {
				b[i] = 0xff
			}

			return b
		}()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			original := KeyData{ID: 42, Secret: tt.secret}
			encoded := original.Encode()
			require.Len(t, encoded, keyLength)

			var decoded KeyData

			require.NoError(t, decoded.Decode(encoded), "every generated key must decode")
			assert.Equal(t, original.ID, decoded.ID)
			assert.Equal(t, original.Secret, decoded.Secret)
		})
	}
}
