package jwt_test

import (
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/jwt"
	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJWTService_GenerateAndValidateToken(t *testing.T) {
	t.Parallel()

	jwtService := jwt.NewService("test-secret-key", jwt.Duration(time.Minute))

	userID := 123
	username := "testuser"

	// Generate token
	token, err := jwtService.Generate(userID, username)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	// Validate token
	claims, err := jwtService.Parse(token)
	require.NoError(t, err)
	require.NotNil(t, claims)

	assert.Equal(t, userID, claims.UserID)
	assert.Equal(t, username, claims.Username)
	assert.Equal(t, "bitmagnet", claims.Issuer)
	assert.True(t, claims.ExpiresAt.After(time.Now()))
}

func TestJWTService_InvalidToken(t *testing.T) {
	t.Parallel()

	jwtService := jwt.NewService("test-secret-key", jwt.Duration(time.Minute))

	// Test with invalid token
	_, err := jwtService.Parse("invalid-token")
	require.Error(t, err)

	// Test with empty token
	_, err = jwtService.Parse("")
	require.Error(t, err)
}

func TestJWTService_TokenWithDifferentSecret(t *testing.T) {
	t.Parallel()

	jwtService1 := jwt.NewService("secret1", jwt.Duration(time.Minute))
	jwtService2 := jwt.NewService("secret2", jwt.Duration(time.Minute))

	userID := 123
	username := "testuser"

	// Generate token with first service
	token, err := jwtService1.Generate(userID, username)
	require.NoError(t, err)

	// Try to validate with second service (different secret)
	_, err = jwtService2.Parse(token)
	assert.Error(t, err)
}

// A signing key identifies the cryptographic trust root, not the token's
// application. If the same secret is reused, accepting an arbitrary issuer
// lets a token minted for another service impersonate a bitmagnet user ID.
func TestJWTService_RejectsTokenFromDifferentIssuer(t *testing.T) {
	t.Parallel()

	const secret = "shared-test-secret"

	service := jwt.NewService(secret, jwt.Duration(time.Minute))
	token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, jwt.Claims{
		UserID:   123,
		Username: "testuser",
		RegisteredClaims: jwtlib.RegisteredClaims{
			Issuer:    "another-application",
			ExpiresAt: jwtlib.NewNumericDate(time.Now().Add(time.Minute)),
		},
	})
	signed, err := token.SignedString([]byte(secret))
	require.NoError(t, err)

	_, err = service.Parse(signed)
	require.Error(t, err, "tokens issued for another application must be rejected")
}
