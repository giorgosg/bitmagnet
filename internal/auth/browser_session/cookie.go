package browser_session

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	internalhttpserver "github.com/bitmagnet-io/bitmagnet/internal/httpserver"
)

const cookiePath = "/graphql"

var errHTTPContextRequired = errors.New("browser session cookie requires an HTTP request context")

// Cookie manages the browser credential without exposing HTTP header assembly
// to GraphQL resolvers.
type Cookie interface {
	Credential(request *http.Request) (string, bool)
	Issue(ctx context.Context, credential string) error
	Expire(ctx context.Context) error
}

type cookie struct {
	name     string
	duration time.Duration
}

func NewCookie(cfg authconfig.Config) Cookie {
	return cookie{name: cfg.BrowserCookieName, duration: cfg.JWTDuration}
}

func (c cookie) Credential(request *http.Request) (string, bool) {
	if request.URL.Path != cookiePath {
		return "", false
	}

	requestCookie, err := request.Cookie(c.name)
	if err != nil {
		return "", false
	}

	return requestCookie.Value, true
}

func (c cookie) Issue(ctx context.Context, credential string) error {
	return c.write(ctx, credential, time.Now().Add(c.duration), int(c.duration.Seconds()))
}

func (c cookie) Expire(ctx context.Context) error {
	return c.write(ctx, "", time.Unix(1, 0), -1)
}

func (c cookie) write(ctx context.Context, value string, expires time.Time, maxAge int) error {
	ginCtx, ok := internalhttpserver.GinContextFromContext(ctx)
	if !ok {
		return errHTTPContextRequired
	}

	http.SetCookie(ginCtx.Writer, &http.Cookie{
		Name:     c.name,
		Value:    value,
		Path:     cookiePath,
		Expires:  expires,
		MaxAge:   maxAge,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})

	return nil
}
