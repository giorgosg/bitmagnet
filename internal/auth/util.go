package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// GenerateRandomString returns length hex characters drawn from the system
// entropy source. It is the source of the JWT secret when none is configured,
// and of invitation codes — credentials whose whole value is unpredictability.
//
// The module requires Go 1.24, where crypto/rand.Read is documented never to
// fail: it crashes the program if the system source is unavailable. That is the
// behaviour wanted here, because the alternative on the older signature was to
// return the zeroed buffer, minting an all-zero secret with nothing to show for
// it. The error is still checked rather than discarded, so the guarantee is
// enforced here and not left implied by the go directive.
func GenerateRandomString(length int) string {
	bytes := make([]byte, (length+1)/2)
	if _, err := rand.Read(bytes); err != nil {
		panic(fmt.Errorf("auth: reading random bytes: %w", err))
	}

	return hex.EncodeToString(bytes)[:length]
}
