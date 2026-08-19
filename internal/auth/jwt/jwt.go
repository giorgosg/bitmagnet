package jwt

import (
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

var ErrTokenInvalidClaims = jwt.ErrTokenInvalidClaims

// issuer names this application in the tokens it mints, and is required of the
// tokens it accepts.
const issuer = "bitmagnet"

type Claims struct {
	UserID   int    `json:"user_id"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

type Service interface {
	Generate(userID int, username string) (string, error)
	Parse(token string) (*Claims, error)
}

type service struct {
	parser        *jwt.Parser
	secretKey     []byte
	tokenDuration time.Duration
}

func NewService(secretKey Secret, duration Duration) Service {
	if secretKey == "" {
		secretKey = Secret(auth.GenerateRandomString(32))
	}

	return &service{
		// Tokens are signed with the one symmetric method below, so accept only
		// that one. Leaving the set open lets a token nominate its own algorithm,
		// which is the shape of every algorithm-confusion attack on JWT.
		//
		// The issuer is checked as well as emitted. A shared signing key proves
		// only that some holder of that key minted the token, not that it was
		// minted for this application — so where an operator reuses a secret
		// across services, a token issued by one of them would otherwise be
		// accepted here as whatever user ID it names.
		parser: jwt.NewParser(
			jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
			jwt.WithIssuer(issuer),
		),
		secretKey:     []byte(secretKey),
		tokenDuration: time.Duration(duration),
	}
}

func (j *service) Generate(userID int, username string) (string, error) {
	claims := &Claims{
		UserID:   userID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(j.tokenDuration)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    issuer,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	return token.SignedString(j.secretKey)
}

func (j *service) Parse(token string) (*Claims, error) {
	parsed, err := j.parser.ParseWithClaims(token, &Claims{}, func(*jwt.Token) (interface{}, error) {
		return j.secretKey, nil
	})
	if err != nil {
		return nil, err
	}

	if claims, ok := parsed.Claims.(*Claims); ok && parsed.Valid {
		return claims, nil
	}

	return nil, jwt.ErrTokenMalformed
}
