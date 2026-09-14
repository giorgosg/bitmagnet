package resolvers_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Logging out has to end the session, not just drop the client's copy of it.
// Clearing the cookie leaves the JWT it carried valid for its whole 24-hour
// lifetime, so anything that captured the token before the logout keeps the
// account until then.
func TestBrowserLogoutRevokesTheTokenItCleared(t *testing.T) {
	t.Parallel()

	server, code := newAuthTestServer(t)
	_ = loginAsAdmin(t, server, code)

	cookie := loginBrowserCookie(t, server, "admin", strongTestPassword)

	loggedOut, _, _ := queryWithOrigin(
		t, server, "", cookie, sameOrigin(t, server), `mutation { self { logoutBrowser } }`,
	)
	requireNoGqlErrors(t, loggedOut)

	// The same cookie the browser was told to forget, replayed by someone who
	// kept it.
	replayed, _, _ := queryWithOrigin(t, server, "", cookie, sameOrigin(t, server),
		`{ self { identity { user { username } } } }`)
	requireNoGqlErrors(t, replayed)
	assert.JSONEq(t, `{"self":{"identity":{"user":null}}}`, string(replayed.Data),
		"a token cleared by logout must no longer resolve to its user")
}

// A bearer token is the same session by another transport, and the UI keeps one
// in localStorage. Logging out of the browser session must end that too.
func TestBrowserLogoutRevokesOutstandingBearerTokens(t *testing.T) {
	t.Parallel()

	server, code := newAuthTestServer(t)
	token := loginAsAdmin(t, server, code)

	cookie := loginBrowserCookie(t, server, "admin", strongTestPassword)
	loggedOut, _, _ := queryWithOrigin(
		t, server, "", cookie, sameOrigin(t, server), `mutation { self { logoutBrowser } }`,
	)
	requireNoGqlErrors(t, loggedOut)

	replayed := query(t, server, token, `{ self { identity { user { username } } } }`)
	requireNoGqlErrors(t, replayed)
	assert.JSONEq(t, `{"self":{"identity":{"user":null}}}`, string(replayed.Data),
		"logging out must end every session for that user, not only the cookie's")
}

// The usual answer to a leaked token is to rotate the password. There was no
// operation to do it with: the service method existed and the schema did not
// expose it.
func TestUpdatePasswordChangesTheCredential(t *testing.T) {
	t.Parallel()

	server, code := newAuthTestServer(t)
	token := loginAsAdmin(t, server, code)

	const newPassword = "a-completely-different-passphrase-71"

	updated := query(t, server, token, `mutation { self { updatePassword(input: {
		currentPassword: "`+strongTestPassword+`", newPassword: "`+newPassword+`"
	}) } }`)
	requireNoGqlErrors(t, updated)

	refused := query(t, server, "", `mutation { self { login(
		username: "admin", password: "`+strongTestPassword+`"
	) { token } } }`)
	requireGraphQLErrorCode(t, refused, "INVALID_CREDENTIALS")

	accepted := query(t, server, "", `mutation { self { login(
		username: "admin", password: "`+newPassword+`"
	) { token } } }`)
	requireNoGqlErrors(t, accepted)

	var login loginResponse

	require.NoError(t, json.Unmarshal(accepted.Data, &login))
	assert.NotEmpty(t, login.Self.Login.Token)
}

// Rotating the password is what an operator reaches for when a token has
// leaked, so it has to invalidate the tokens - otherwise it changes what an
// attacker cannot use anyway and leaves what they can.
func TestUpdatePasswordRevokesOutstandingSessions(t *testing.T) {
	t.Parallel()

	server, code := newAuthTestServer(t)
	token := loginAsAdmin(t, server, code)
	cookie := loginBrowserCookie(t, server, "admin", strongTestPassword)

	updated := query(t, server, token, `mutation { self { updatePassword(input: {
		currentPassword: "`+strongTestPassword+`", newPassword: "another-good-passphrase-2026"
	}) } }`)
	requireNoGqlErrors(t, updated)

	for name, credential := range map[string]func() gqlResponse{
		"the bearer token that changed it": func() gqlResponse {
			return query(t, server, token, `{ self { identity { user { username } } } }`)
		},
		"an outstanding browser cookie": func() gqlResponse {
			replayed, _, _ := queryWithOrigin(t, server, "", cookie, sameOrigin(t, server),
				`{ self { identity { user { username } } } }`)

			return replayed
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			response := credential()
			requireNoGqlErrors(t, response)
			assert.JSONEq(t, `{"self":{"identity":{"user":null}}}`, string(response.Data))
		})
	}
}

func TestUpdatePasswordRequiresTheCurrentPassword(t *testing.T) {
	t.Parallel()

	server, code := newAuthTestServer(t)
	token := loginAsAdmin(t, server, code)

	response := query(t, server, token, `mutation { self { updatePassword(input: {
		currentPassword: "not-the-current-password", newPassword: "another-good-passphrase-2026"
	}) } }`)
	requireGraphQLErrorCode(t, response, "PASSWORD_INCORRECT")

	stillWorks := query(t, server, token, `{ self { identity { user { username } } } }`)
	requireNoGqlErrors(t, stillWorks)
	assert.JSONEq(t, `{"self":{"identity":{"user":{"username":"admin"}}}}`, string(stillWorks.Data),
		"a refused password change must not revoke the session that attempted it")
}

func TestUpdatePasswordRefusesAWeakNewPassword(t *testing.T) {
	t.Parallel()

	server, code := newAuthTestServer(t)
	token := loginAsAdmin(t, server, code)

	response := query(t, server, token, `mutation { self { updatePassword(input: {
		currentPassword: "`+strongTestPassword+`", newPassword: "password"
	}) } }`)
	requireGraphQLErrorCode(t, response, "PASSWORD_INSUFFICIENT_ENTROPY")
}

// The mutation acts on the caller's own account, so there has to be one. An API
// key is a user credential but not an interactive session, and the same
// boundary that guards key management guards this.
func TestUpdatePasswordRequiresAUserSession(t *testing.T) {
	t.Parallel()

	server, _ := newAuthTestServer(t)

	response := query(t, server, "", `mutation { self { updatePassword(input: {
		currentPassword: "`+strongTestPassword+`", newPassword: "another-good-passphrase-2026"
	}) } }`)
	requireGraphQLErrorCode(t, response, "USER_SESSION_REQUIRED")
}

// Logging out when nobody is logged in still has to answer, and must not revoke
// anything: the cookie path is reachable anonymously by design.
func TestAnonymousLogoutRevokesNothing(t *testing.T) {
	t.Parallel()

	server, code := newAuthTestServer(t)
	token := loginAsAdmin(t, server, code)

	loggedOut, headers, _ := queryWithOrigin(
		t, server, "", nil, sameOrigin(t, server), `mutation { self { logoutBrowser } }`,
	)
	requireNoGqlErrors(t, loggedOut)

	cookies := (&http.Response{Header: headers}).Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, -1, cookies[0].MaxAge)

	stillWorks := query(t, server, token, `{ self { identity { user { username } } } }`)
	requireNoGqlErrors(t, stillWorks)
	assert.JSONEq(t, `{"self":{"identity":{"user":{"username":"admin"}}}}`, string(stillWorks.Data))
}
