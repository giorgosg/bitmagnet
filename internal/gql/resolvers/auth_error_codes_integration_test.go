package resolvers_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/identity"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const strongTestPassword = "correct-horse-battery-staple-99"

func requireGraphQLErrorCode(t *testing.T, response gqlResponse, code string) gqlError {
	t.Helper()

	require.Len(t, response.Errors, 1)

	gqlErr := response.Errors[0]
	assert.Equal(t, code, gqlErr.Extensions["code"])
	assert.NotEmpty(t, gqlErr.Path, "classified errors must retain their GraphQL path")
	assert.NotEmpty(t, gqlErr.Locations, "classified errors must retain their source location")

	return gqlErr
}

func TestGraphQLLoginErrorCodes(t *testing.T) {
	t.Parallel()

	t.Run("invalid credentials", func(t *testing.T) {
		t.Parallel()

		server, _ := newAuthTestServer(t)
		response := query(t, server, "", `mutation { self { login(
			username: "missing", password: "incorrect"
		) { token } } }`)

		requireGraphQLErrorCode(t, response, "INVALID_CREDENTIALS")
	})

	t.Run("disabled User", func(t *testing.T) {
		t.Parallel()

		server, code := newAuthTestServer(t)
		adminToken := loginAsAdmin(t, server, code)
		requireNoGqlErrors(t, query(t, server, adminToken,
			`mutation { auth { setUserEnabled(userId: 1, enabled: false) { id } } }`))

		response := query(t, server, "", `mutation { self { login(
			username: "admin", password: "`+strongTestPassword+`"
		) { token } } }`)

		requireGraphQLErrorCode(t, response, "USER_DISABLED")
	})

	t.Run("throttled", func(t *testing.T) {
		t.Parallel()

		cfg := authconfig.NewDefaultConfig()
		cfg.LoginRequestBurst = 1
		server, _ := newAuthTestServerWithConfig(t, cfg)
		login := `mutation { self { login(username: "missing", password: "incorrect") { token } } }`

		requireGraphQLErrorCode(t, query(t, server, "", login), "INVALID_CREDENTIALS")
		requireGraphQLErrorCode(t, query(t, server, "", login), "LOGIN_THROTTLED")
	})
}

func TestGraphQLRegistrationErrorCodes(t *testing.T) {
	t.Parallel()

	t.Run("duplicate User", func(t *testing.T) {
		t.Parallel()

		cfg := authconfig.NewDefaultConfig()
		cfg.InvitationRequired = false
		server, _ := newAuthTestServerWithConfig(t, cfg)
		register := `mutation { self { register(input: {
			username: "duplicate", password: "` + strongTestPassword + `"
		}) { user { id } } } }`

		requireNoGqlErrors(t, query(t, server, "", register))
		requireGraphQLErrorCode(t, query(t, server, "", register), "USER_ALREADY_EXISTS")
	})

	t.Run("invalid username", func(t *testing.T) {
		t.Parallel()

		server, _ := newAuthTestServer(t)
		response := query(t, server, "", `mutation { self { register(input: {
			username: "not valid", password: "`+strongTestPassword+`"
		}) { user { id } } } }`)

		requireGraphQLErrorCode(t, response, "USERNAME_INVALID")
	})

	t.Run("missing Invitation", func(t *testing.T) {
		t.Parallel()

		server, _ := newAuthTestServer(t)
		response := query(t, server, "", `mutation { self { register(input: {
			username: "missinginvite", password: "`+strongTestPassword+`"
		}) { user { id } } } }`)

		requireGraphQLErrorCode(t, response, "INVITATION_REQUIRED")
	})

	t.Run("invalid Invitation", func(t *testing.T) {
		t.Parallel()

		server, _ := newAuthTestServer(t)
		response := query(t, server, "", `mutation { self { register(input: {
			invitationCode: "not-an-invitation", username: "badinvite",
			password: "`+strongTestPassword+`"
		}) { user { id } } } }`)

		requireGraphQLErrorCode(t, response, "INVITATION_INVALID")
	})

	t.Run("expired Invitation", func(t *testing.T) {
		t.Parallel()

		server, code := newAuthTestServer(t)
		adminToken := loginAsAdmin(t, server, code)
		invited := query(t, server, adminToken,
			`mutation { auth { invite(input: {expiry: "PT0.001S"}) { code } } }`)
		requireNoGqlErrors(t, invited)

		var invitation inviteResponse
		require.NoError(t, json.Unmarshal(invited.Data, &invitation))
		require.NotEmpty(t, invitation.Auth.Invite.Code)
		time.Sleep(10 * time.Millisecond)

		response := query(t, server, "", `mutation { self { register(input: {
			invitationCode: "`+invitation.Auth.Invite.Code+`", username: "expiredinvite",
			password: "`+strongTestPassword+`"
		}) { user { id } } } }`)

		requireGraphQLErrorCode(t, response, "INVITATION_EXPIRED")
	})

	t.Run("claimed Invitation", func(t *testing.T) {
		t.Parallel()

		server, code := newAuthTestServer(t)
		requireNoGqlErrors(t, query(t, server, "", `mutation { self { register(input: {
			invitationCode: "`+code+`", username: "firstclaim",
			password: "`+strongTestPassword+`"
		}) { user { id } } } }`))

		response := query(t, server, "", `mutation { self { register(input: {
			invitationCode: "`+code+`", username: "secondclaim",
			password: "`+strongTestPassword+`"
		}) { user { id } } } }`)

		requireGraphQLErrorCode(t, response, "INVITATION_CLAIMED")
	})

	t.Run("insufficient password entropy", func(t *testing.T) {
		t.Parallel()

		cfg := authconfig.NewDefaultConfig()
		cfg.InvitationRequired = false
		server, _ := newAuthTestServerWithConfig(t, cfg)
		response := query(t, server, "", `mutation { self { register(input: {
			username: "weakpassword", password: "password"
		}) { user { id } } } }`)

		requireGraphQLErrorCode(t, response, "PASSWORD_INSUFFICIENT_ENTROPY")
	})
}

func TestGraphQLAuthorizationErrorIncludesObjectAction(t *testing.T) {
	t.Parallel()

	cfg := authconfig.NewDefaultConfig()
	cfg.AnonymousAccess = false
	server, _ := newAuthTestServerWithConfig(t, cfg)

	gqlErr := requireGraphQLErrorCode(
		t,
		query(t, server, "", `{ auth { listUsers { totalCount } } }`),
		"UNAUTHORIZED",
	)
	assert.Equal(t, "graphql", gqlErr.Extensions["namespace"])
	assert.Equal(t, "auth", gqlErr.Extensions["object"])
	assert.Equal(t, "query", gqlErr.Extensions["action"])
}

type failingIdentity struct{ err error }

func (failingIdentity) Self() identity.Self { return identity.Self{} }

func (f failingIdentity) Enforce(context.Context, rbac.ObjectAction) (bool, error) {
	return false, f.err
}

type staticAuthenticator struct{ identity identity.Identity }

func (a staticAuthenticator) Authenticate(context.Context, string) (identity.Identity, bool, error) {
	return a.identity, true, nil
}

func TestGraphQLUnknownInternalErrorIsRedacted(t *testing.T) {
	t.Parallel()

	backendErr := errors.New("casbin storage internals must stay private")
	server, _ := newAuthTestServerWithConfigAndAuthenticator(
		t,
		authconfig.NewDefaultConfig(),
		staticAuthenticator{identity: failingIdentity{err: backendErr}},
	)

	response := query(t, server, "credential", `{ auth { listUsers { totalCount } } }`)
	gqlErr := requireGraphQLErrorCode(t, response, "INTERNAL_SERVER_ERROR")
	assert.Equal(t, "internal server error", gqlErr.Message)
	assert.NotContains(t, gqlErr.Message, backendErr.Error())
}

func TestGraphQLErrorPresenterKeepsProtocolErrorsUseful(t *testing.T) {
	t.Parallel()

	server, _ := newAuthTestServer(t)
	response := query(t, server, "", `{ fieldThatDoesNotExist }`)

	require.Len(t, response.Errors, 1)
	assert.Contains(t, response.Errors[0].Message, "Cannot query field")
	assert.NotEmpty(t, response.Errors[0].Locations)
	assert.Equal(t, "GRAPHQL_VALIDATION_FAILED", response.Errors[0].Extensions["code"])
}
