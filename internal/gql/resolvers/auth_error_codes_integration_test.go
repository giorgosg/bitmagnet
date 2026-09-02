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

	t.Run("missing email", func(t *testing.T) {
		t.Parallel()

		cfg := authconfig.NewDefaultConfig()
		cfg.InvitationRequired = false
		cfg.EmailRequired = true
		server, _ := newAuthTestServerWithConfig(t, cfg)
		response := query(t, server, "", `mutation { self { register(input: {
			username: "noemail", password: "`+strongTestPassword+`"
		}) { user { id } } } }`)

		requireGraphQLErrorCode(t, response, "EMAIL_REQUIRED")
	})

	t.Run("invalid email", func(t *testing.T) {
		t.Parallel()

		cfg := authconfig.NewDefaultConfig()
		cfg.InvitationRequired = false
		server, _ := newAuthTestServerWithConfig(t, cfg)
		response := query(t, server, "", `mutation { self { register(input: {
			username: "bademail", email: "not-an-email",
			password: "`+strongTestPassword+`"
		}) { user { id } } } }`)

		requireGraphQLErrorCode(t, response, "EMAIL_INVALID")
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

func (f failingIdentity) EffectivePermissions(context.Context) ([]rbac.ObjectAction, error) {
	return nil, f.err
}

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

// A role name the caller made up reaches the database as a foreign-key
// violation on invitations.role_name. Unclassified, that is presented as
// INTERNAL_SERVER_ERROR: a message naming a constraint, for what is a typo in a
// field. setUserRole reports the same condition, so both are covered here.
func TestGraphQLUnknownRoleErrorCodes(t *testing.T) {
	t.Parallel()

	t.Run("invite", func(t *testing.T) {
		t.Parallel()

		server, code := newAuthTestServer(t)
		adminToken := loginAsAdmin(t, server, code)

		response := query(t, server, adminToken,
			`mutation { auth { invite(input: {role: "administrator"}) { code } } }`)

		requireGraphQLErrorCode(t, response, "ROLE_NOT_FOUND")
	})

	t.Run("setUserRole", func(t *testing.T) {
		t.Parallel()

		server, code := newAuthTestServer(t)
		adminToken := loginAsAdmin(t, server, code)

		response := query(t, server, adminToken,
			`mutation { auth { setUserRole(userId: 1, roleName: "administrator") { id } } }`)

		requireGraphQLErrorCode(t, response, "ROLE_NOT_FOUND")
	})
}

// createAPIKey stored whatever object actions it was handed: api_key_permissions
// has no foreign key to a registry, because there is no registry table. A typo
// produced a key that grants nothing and reported success.
func TestGraphQLCreateAPIKeyInvalidPermissionErrorCode(t *testing.T) {
	t.Parallel()

	server, code := newAuthTestServer(t)
	adminToken := loginAsAdmin(t, server, code)

	response := query(t, server, adminToken, `mutation { self { createAPIKey(input: {
		name: "typo",
		permissions: [{namespace: "graphql", object: "torrent", action: "quury"}]
	}) { id } } }`)

	requireGraphQLErrorCode(t, response, "PERMISSION_INVALID")
}

// self.permissions is what a client presents to the operator, so it has to
// agree with what Enforce actually grants.
//
// APIKey.Enforce requires both the owning User's Role and the key's own
// selection (or anon). The reported set was the selection concatenated with
// anon's and never intersected with the Role, so a key selected for
// torrent:delete whose owner is demoted kept listing torrent:delete while the
// enforcer refused it.
func TestSelfPermissionsForAnAPIKeyAgreeWithEnforcement(t *testing.T) {
	t.Parallel()

	cfg := authconfig.NewDefaultConfig()
	// With the open baseline anon holds nearly everything, which would mask the
	// intersection under a set the Role happens to allow anyway.
	cfg.AnonymousAccess = false

	server, code := newAuthTestServerWithConfig(t, cfg)
	adminToken := loginAsAdmin(t, server, code)

	// A second administrator, so demoting one does not leave the instance
	// without any.
	deputyToken := registerAndLoginAsRole(t, server, adminToken, "admin", "deputy")
	key := apiKeyFrom(t, createAPIKeyAs(
		t, server, deputyToken, "wide", "graphql", "auth", "query",
	))

	// A top-level field, so reaching it needs exactly the one action the key
	// names and no parent gate as well - and unlike version:query, the user role
	// does not hold it, so demotion actually withdraws it.
	adminQuery := `{ auth { listUsers { totalCount } } }`
	permissionsQuery := `{ self { identity {
		permissions { namespace object action }
		apiKey { name permissions { namespace object action } }
	} } }`

	// While the owner is an admin, the selection is honoured and reported.
	requireNoGqlErrors(t, query(t, server, key, adminQuery))
	assert.Contains(t,
		objectActionStrings(selfIdentity(t, query(t, server, key, permissionsQuery)).Permissions),
		"graphql::auth::query", "an allowed action must be reported")

	// Demote the owner. The key is unchanged; what it can reach is not.
	requireNoGqlErrors(t, query(t, server, adminToken, `mutation { auth { setUserRole(
		userId: 2, roleName: "user"
	) { role } } }`))

	denied := query(t, server, key, adminQuery)
	require.NotEmpty(t, denied.Errors, "the enforcer must refuse what the Role no longer allows")
	assert.Equal(t, "unauthorized", denied.Errors[0].Message)

	reported := selfIdentity(t, query(t, server, key, permissionsQuery))
	assert.NotContains(t, objectActionStrings(reported.Permissions), "graphql::auth::query",
		"a refused action must not be reported as held")

	// The selection itself is unchanged, and is still visible - a client shows
	// what the key was selected for alongside what it can currently reach.
	require.NotNil(t, reported.APIKey)
	assert.Equal(t, []string{"graphql::auth::query"}, objectActionStrings(reported.APIKey.Permissions),
		"the selected set is a property of the key and does not move with the Role")
}

// selfIdentity unmarshals a self.identity response. The two permission sets it
// carries are the point of the test above: what the identity may currently do,
// and - for an API key - what it was selected for.
func selfIdentity(t *testing.T, response gqlResponse) identityBody {
	t.Helper()
	requireNoGqlErrors(t, response)

	var body identityResponse

	require.NoError(t, json.Unmarshal(response.Data, &body))

	return body.Self.Identity
}

func objectActionStrings(actions []objectActionBody) []string {
	strs := make([]string, 0, len(actions))
	for _, action := range actions {
		strs = append(strs, action.Namespace+"::"+action.Object+"::"+action.Action)
	}

	return strs
}
