package identity_test

import (
	"context"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/api_key"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/identity"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/jwt"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/user"
	"github.com/bitmagnet-io/bitmagnet/internal/database"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type daoProvider struct{ query *dao.Query }

func (p daoProvider) Dao() (*dao.Query, error) { return p.query, nil }

func (p daoProvider) DaoTransaction(fn func(tx *dao.Query) error) error {
	return p.query.Transaction(fn)
}

type testStack struct {
	authenticator identity.Authenticator
	userService   user.Service
	apiKeyService api_key.Service
	query         *dao.Query
}

func newTestStack(t *testing.T) testStack {
	t.Helper()

	db := dbtest.New(t)

	var provider database.DaoTransactionProvider = daoProvider{query: db.Query}

	values := authconfig.NewDefaultConfig().UserValues()
	jwtService := jwt.NewService(jwt.Secret("test-secret"), jwt.Duration(time.Hour))
	userService := user.NewService(
		provider,
		jwtService,
		values.InvitationRequired,
		values.EmailRequired,
		values.EmailVerification,
		values.PasswordMinEntropy,
		values.PasswordHashingCost,
		values.LoginRequestsPerMinute,
		values.LoginRequestBurst,
	)
	apiKeyService := api_key.NewService(api_key.NewRepository(provider))

	objectActions := func() []rbac.ObjectAction {
		return []rbac.ObjectAction{rbac.NewObjectAction("test", "test", "query")}
	}
	rbacService := rbac.NewService(
		rbac.NewRepository(provider),
		objectActions,
		rbac.CorePermissions,
		rbac.CacheTTL(time.Minute),
	)

	return testStack{
		authenticator: identity.NewAuthenticator(jwtService, userService, apiKeyService, rbacService),
		userService:   userService,
		apiKeyService: apiKeyService,
		query:         db.Query,
	}
}

const testPassword = "correct-horse-battery-staple-99"

func (s testStack) registerAdmin(t *testing.T) model.User {
	t.Helper()

	invitation, err := s.userService.CreateInitialInvitation(context.Background())
	require.NoError(t, err)

	registered, err := s.userService.Register(context.Background(), user.RegisterRequest{
		InvitationCode: invitation.Code,
		Username:       "admin",
		Password:       testPassword,
	})
	require.NoError(t, err)

	return registered
}

// Disabling an account must revoke the sessions it already has. Blocking only
// new logins leaves a disabled user with full access until the token expires,
// which defeats the point of disabling them.
func TestDisabledUserTokenIsRejected(t *testing.T) {
	t.Parallel()

	stack := newTestStack(t)
	admin := stack.registerAdmin(t)

	login, err := stack.userService.Login(context.Background(), "admin", testPassword)
	require.NoError(t, err)
	require.NotEmpty(t, login.Token)

	// The token works while the account is enabled.
	_, matched, err := stack.authenticator.Authenticate(context.Background(), login.Token)
	require.NoError(t, err)
	require.True(t, matched)

	_, err = stack.userService.SetEnabled(context.Background(), admin.ID, false)
	require.NoError(t, err)

	_, _, err = stack.authenticator.Authenticate(context.Background(), login.Token)
	require.Error(t, err, "a disabled account's existing token must be rejected")
	assert.ErrorIs(t, err, user.ErrDisabled)
}

// The same for machine credentials, which matters more: API keys have no
// expiry by default, so without this they outlive the account indefinitely.
func TestDisabledUserAPIKeyIsRejected(t *testing.T) {
	t.Parallel()

	stack := newTestStack(t)
	admin := stack.registerAdmin(t)

	created, err := stack.apiKeyService.Create(context.Background(), api_key.CreateRequest{
		UserID:      admin.ID,
		Name:        "test",
		Permissions: []rbac.ObjectAction{rbac.NewObjectAction("test", "test", "query")},
	})
	require.NoError(t, err)

	_, err = stack.apiKeyService.Auth(context.Background(), created.APIKey)
	require.NoError(t, err, "the key works while the account is enabled")

	_, err = stack.userService.SetEnabled(context.Background(), admin.ID, false)
	require.NoError(t, err)

	_, err = stack.apiKeyService.Auth(context.Background(), created.APIKey)
	require.Error(t, err, "a disabled account's API key must be rejected")
}

// Re-enabling restores access, so this is a reversible control rather than a
// one-way door.
func TestReEnabledUserIsAcceptedAgain(t *testing.T) {
	t.Parallel()

	stack := newTestStack(t)
	admin := stack.registerAdmin(t)

	login, err := stack.userService.Login(context.Background(), "admin", testPassword)
	require.NoError(t, err)

	_, err = stack.userService.SetEnabled(context.Background(), admin.ID, false)
	require.NoError(t, err)

	_, _, err = stack.authenticator.Authenticate(context.Background(), login.Token)
	require.Error(t, err)

	_, err = stack.userService.SetEnabled(context.Background(), admin.ID, true)
	require.NoError(t, err)

	_, matched, err := stack.authenticator.Authenticate(context.Background(), login.Token)
	require.NoError(t, err)
	assert.True(t, matched)
}

// An expired session must degrade to anonymous, not lock the caller out.
//
// jwt/v5 reports an expired token as ErrTokenInvalidClaims. Treating that as a
// match aborted the authenticator chain, so no identity reached the request and
// every field was refused — including self.login, the one call needed to
// recover. The only escape was clearing browser storage by hand.
func TestExpiredTokenFallsBackToAnonymous(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)

	var provider database.DaoTransactionProvider = daoProvider{query: db.Query}

	values := authconfig.NewDefaultConfig().UserValues()
	// A lifetime already in the past, so the token is born expired.
	jwtService := jwt.NewService(jwt.Secret("test-secret"), jwt.Duration(-time.Hour))
	userService := user.NewService(
		provider, jwtService,
		values.InvitationRequired, values.EmailRequired, values.EmailVerification,
		values.PasswordMinEntropy, values.PasswordHashingCost,
		values.LoginRequestsPerMinute, values.LoginRequestBurst,
	)
	apiKeyService := api_key.NewService(api_key.NewRepository(provider))

	objectActions := func() []rbac.ObjectAction {
		return []rbac.ObjectAction{rbac.NewObjectAction("test", "test", "query")}
	}
	rbacService := rbac.NewService(
		rbac.NewRepository(provider), objectActions,
		rbac.PermissionProviders(rbac.CorePermissions, rbac.VerbatimPermissions(objectActions)),
		rbac.CacheTTL(time.Minute),
	)
	authenticator := identity.NewAuthenticator(jwtService, userService, apiKeyService, rbacService)

	invitation, err := userService.CreateInitialInvitation(context.Background())
	require.NoError(t, err)

	_, err = userService.Register(context.Background(), user.RegisterRequest{
		InvitationCode: invitation.Code,
		Username:       "admin",
		Password:       testPassword,
	})
	require.NoError(t, err)

	login, err := userService.Login(context.Background(), "admin", testPassword)
	require.NoError(t, err)

	resolved, matched, err := authenticator.Authenticate(context.Background(), login.Token)
	require.NoError(t, err, "an expired token must not abort the chain")
	require.True(t, matched, "the anonymous authenticator must still match")
	require.NotNil(t, resolved)
	assert.Nil(t, resolved.Self().User, "an expired token resolves to nobody, not to its former user")
}

// A syntactically invalid credential must behave the same way.
func TestGarbageTokenFallsBackToAnonymous(t *testing.T) {
	t.Parallel()

	stack := newTestStack(t)

	resolved, matched, err := stack.authenticator.Authenticate(context.Background(), "not-a-token")
	require.NoError(t, err)
	require.True(t, matched)
	assert.Nil(t, resolved.Self().User)
}
