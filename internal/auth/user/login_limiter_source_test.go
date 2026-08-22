package user_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/http_auth"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/identity"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/user"
	"github.com/bitmagnet-io/bitmagnet/internal/httpserver"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unmatchedAuthenticator stands in for the real chain. AttachAuth only needs
// something to call; these tests are about the login source it records, not
// about which identity it resolves.
type unmatchedAuthenticator struct{}

func (unmatchedAuthenticator) Authenticate(context.Context, string) (identity.Identity, bool, error) {
	return nil, false, errors.New("no match")
}

// loginThroughMiddleware drives Login the way a real request reaches it: through
// AttachAuth, which is the layer that decides what the throttle keys on.
// forwardedFor is sent as X-Forwarded-For; an empty value omits the header.
func loginThroughMiddleware(
	t *testing.T,
	service user.Service,
	username, password, forwardedFor string,
) error {
	t.Helper()

	var loginErr error

	// Built the way the real server builds it, so the proxy-trust settings that
	// decide what ClientIP returns are the shipped ones.
	engine, err := httpserver.NewEngine(httpserver.NewDefaultConfig())
	require.NoError(t, err)

	engine.Use(http_auth.NewMiddleware(unmatchedAuthenticator{}).AttachAuth())
	engine.POST("/login", func(c *gin.Context) {
		_, loginErr = service.Login(c.Request.Context(), username, password)
	})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/login", nil)
	// A fixed peer address: the socket the packets actually arrive on never
	// changes in these tests, only the header does.
	req.RemoteAddr = "203.0.113.9:44321"

	if forwardedFor != "" {
		req.Header.Set("X-Forwarded-For", forwardedFor)
	}

	engine.ServeHTTP(httptest.NewRecorder(), req)

	return loginErr
}

// The control: a single source really is throttled. Without this passing, the
// bypass below would prove nothing.
func TestLoginIsThrottledForOneSource(t *testing.T) {
	t.Parallel()

	service, query := newUserService(t)
	putInvitation(t, query, "throttlesrc1", sql.NullTime{})

	_, err := service.Register(t.Context(), user.RegisterRequest{
		InvitationCode: "throttlesrc1",
		Username:       "victim",
		Password:       testPassword,
	})
	require.NoError(t, err)

	// The default burst is 5.
	for i := range 5 {
		err = loginThroughMiddleware(t, service, "victim", "wrong-password", "198.51.100.7")
		require.ErrorIs(t, err, user.ErrCredentialsInvalid, "attempt %d should reach the password check", i)
	}

	err = loginThroughMiddleware(t, service, "victim", "wrong-password", "198.51.100.7")
	assert.ErrorIs(t, err, user.ErrLoginRequestLimiter,
		"a sixth attempt from the same source must be refused")
}

// The login throttle keys on gin's ClientIP(), and gin will read that from
// X-Forwarded-For for any peer it considers a trusted proxy. Its default is to
// trust every proxy, so before http_server.trusted_proxies existed the client
// address was simply a header the caller wrote: rotating it handed the attacker
// a fresh bucket per request, and 30 guesses against one account from one socket
// went entirely unthrottled.
//
// The throttle only bounds anything if the key is something the attacker cannot
// choose, so this asserts the untrusted default holds.
func TestLoginThrottleIsNotBypassableByForwardedForHeader(t *testing.T) {
	t.Parallel()

	service, query := newUserService(t)
	putInvitation(t, query, "throttlesrc2", sql.NullTime{})

	_, err := service.Register(t.Context(), user.RegisterRequest{
		InvitationCode: "throttlesrc2",
		Username:       "victim",
		Password:       testPassword,
	})
	require.NoError(t, err)

	// Well past the per-(account, source) burst of 5 and the wider per-source
	// budget of 20. Every request comes from the same socket and names the same
	// account; only the header the caller made up differs. Something in here
	// has to be refused, or the throttle bounds nothing an attacker cannot
	// choose for themselves.
	const attempts = 30

	throttled := false

	for i := range attempts {
		err = loginThroughMiddleware(
			t, service, "victim", "wrong-password",
			fmt.Sprintf("198.51.100.%d", i%256),
		)
		if errors.Is(err, user.ErrLoginRequestLimiter) {
			throttled = true

			break
		}

		require.ErrorIs(t, err, user.ErrCredentialsInvalid)
	}

	assert.True(t, throttled,
		"%d password guesses against one account from one peer went unthrottled "+
			"by varying X-Forwarded-For alone", attempts)
}
