package http_auth

import (
	"strings"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/browser_session"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/identity"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/user"
	"github.com/gin-gonic/gin"
)

const (
	AuthorizationHeader = "Authorization"
	BearerPrefix        = "Bearer "
	resolutionKey       = "auth_resolution"
)

type CredentialSource string

const (
	CredentialSourceNone   CredentialSource = ""
	CredentialSourceBearer CredentialSource = "bearer"
	CredentialSourceCookie CredentialSource = "cookie"
)

type Resolution struct {
	Identity identity.Identity
	Source   CredentialSource
	Rejected bool
	Err      error
}

type Middleware interface {
	AttachAuth() gin.HandlerFunc
}

type authMiddleware struct {
	authenticator identity.Authenticator
	browserCookie browser_session.Cookie
}

func NewMiddleware(authenticator identity.Authenticator, browserCookie browser_session.Cookie) Middleware {
	return &authMiddleware{
		authenticator: authenticator,
		browserCookie: browserCookie,
	}
}

func (a *authMiddleware) AttachAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// The login throttle is keyed partly by where a request came from, and
		// this is the only layer that knows. It goes on the request context
		// rather than the gin one because resolvers downstream only ever see
		// the former.
		c.Request = c.Request.WithContext(
			user.ContextWithLoginSource(c.Request.Context(), c.ClientIP()),
		)

		credential, source := a.credential(c)
		resolved, matched, err := a.authenticator.Authenticate(c, credential)
		resolution := Resolution{
			Source: source,
			Err:    err,
		}

		if err == nil && matched {
			resolution.Identity = resolved
			resolution.Rejected = source != CredentialSourceNone && isAnonymous(resolved)
		}

		if resolution.Source == CredentialSourceCookie && resolution.Rejected {
			if expireErr := a.browserCookie.Expire(c.Request.Context()); expireErr != nil {
				resolution.Err = expireErr
				resolution.Identity = nil
			}
		}

		c.Set(resolutionKey, resolution)

		c.Next()
	}
}

func (a *authMiddleware) credential(c *gin.Context) (string, CredentialSource) {
	if c.GetHeader(AuthorizationHeader) != "" {
		return extractToken(c), CredentialSourceBearer
	}

	credential, ok := a.browserCookie.Credential(c.Request)
	if ok {
		return credential, CredentialSourceCookie
	}

	return "", CredentialSourceNone
}

func isAnonymous(resolved identity.Identity) bool {
	if resolved == nil {
		return false
	}

	self := resolved.Self()

	return self.User == nil && self.APIKey == nil
}

func extractToken(c *gin.Context) string {
	authHeader := c.GetHeader(AuthorizationHeader)
	if authHeader == "" {
		return ""
	}

	if !strings.HasPrefix(authHeader, BearerPrefix) {
		return ""
	}

	return strings.TrimPrefix(authHeader, BearerPrefix)
}

// GetIdentity retrieves the current authenticated identity from the Gin context
func GetIdentity(c *gin.Context) (identity.Identity, bool) {
	resolution, ok := GetResolution(c)
	if !ok || resolution.Identity == nil {
		return nil, false
	}

	return resolution.Identity, true
}

func GetResolution(c *gin.Context) (Resolution, bool) {
	raw, exists := c.Get(resolutionKey)
	if !exists {
		return Resolution{}, false
	}

	resolution, ok := raw.(Resolution)

	return resolution, ok
}
