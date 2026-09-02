package identity_test

import (
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/api_key"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/identity"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/jwt"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/user"
	"github.com/bitmagnet-io/bitmagnet/internal/database"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// scopeTestObjectAction is the only object action the scope stack registers.
var scopeTestObjectAction = rbac.NewObjectAction("test", "test", "query")

// newScopeStack differs from newTestStack in one way that matters here: it
// includes VerbatimPermissions, as authfx does. Those are the policies that give
// an API key's own permission list any effect at all, so a stack without them
// denies every scoped key and cannot tell a working scope check from a broken
// one.
func newScopeStack(t *testing.T) scopeStack {
	t.Helper()

	db := dbtest.New(t)

	var provider database.DaoTransactionProvider = daoProvider{query: db.Query}

	values := authconfig.NewDefaultConfig().UserValues()
	// These tests exercise credential scope, not bcrypt's work factor.
	values.PasswordHashingCost.Set(user.PasswordHashingCost(bcrypt.MinCost))

	jwtService := jwt.NewService(jwt.Secret("test-secret"), jwt.Duration(time.Hour))
	userService := user.NewService(
		provider, jwtService,
		values.InvitationRequired, values.EmailRequired, values.EmailVerification,
		values.PasswordMinEntropy, values.PasswordHashingCost,
		values.LoginRequestsPerMinute, values.LoginRequestBurst,
	)
	objectActions := func() []rbac.ObjectAction {
		return []rbac.ObjectAction{scopeTestObjectAction}
	}
	apiKeyRepository := api_key.NewRepository(provider)
	apiKeyService := api_key.NewService(apiKeyRepository, objectActions)

	rbacService := rbac.NewService(
		rbac.NewRepository(provider), objectActions,
		rbac.PermissionProviders(rbac.CorePermissions, rbac.VerbatimPermissions(objectActions)),
		rbac.CacheTTL(time.Minute),
	)

	return scopeStack{
		testStack: testStack{
			authenticator: identity.NewAuthenticator(jwtService, userService, apiKeyService, rbacService),
			userService:   userService,
			apiKeyService: apiKeyService,
			query:         db.Query,
		},
		apiKeyRepository: apiKeyRepository,
	}
}

// scopeStack is a testStack that can also write a key the service would refuse.
// The repository handle lives here rather than on testStack because only these
// tests need it.
type scopeStack struct {
	testStack
	apiKeyRepository api_key.Repository
}

// putAPIKey writes a key straight through the repository, bypassing the
// service's validation of the requested object actions. It is how a permission
// the service would now refuse still gets into the table, which is what the
// enforcement-level guarantees have to be tested against.
func (s scopeStack) putAPIKey(
	t *testing.T,
	userID int,
	name string,
	permissions ...rbac.ObjectAction,
) string {
	t.Helper()

	secret, err := api_key.NewSecret()
	require.NoError(t, err)

	id, err := s.apiKeyRepository.Create(t.Context(), userID, name, secret.Hash, permissions, time.Time{})
	require.NoError(t, err)

	return api_key.KeyData{ID: id, Secret: secret.Secret}.Encode()
}

// A key naming the registered action exactly must work. This is the control for
// the wildcard test below: without it, a denial there proves nothing.
func TestAPIKeyPermissionExactMatchIsAllowed(t *testing.T) {
	t.Parallel()

	stack := newScopeStack(t)
	admin := stack.registerAdmin(t)

	created, err := stack.apiKeyService.Create(t.Context(), api_key.CreateRequest{
		UserID:      admin.ID,
		Name:        "exact",
		Permissions: []rbac.ObjectAction{scopeTestObjectAction},
	})
	require.NoError(t, err)

	id, matched, err := stack.authenticator.Authenticate(t.Context(), created.APIKey)
	require.NoError(t, err)
	require.True(t, matched)

	allow, err := id.Enforce(t.Context(), scopeTestObjectAction)
	require.NoError(t, err)
	assert.True(t, allow, "a key naming the registered action exactly must be allowed")
}

// self.createAPIKey now refuses an unregistered object action, so a wildcard
// cannot reach the database through the service at all. This writes one through
// the repository regardless, because the enforcement-level guarantee is the one
// that contains a row already stored — by an older build, or by hand.
//
// The permissions become a casbin request subject, and the matcher is globMatch
// on all three terms, so it matters a great deal which side of that match is
// treated as the pattern. If the request side were the pattern, a stored "*"
// would hand the key its owner's entire role.
func TestAPIKeyPermissionWildcardsDoNotWidenScope(t *testing.T) {
	t.Parallel()

	stack := newScopeStack(t)
	admin := stack.registerAdmin(t)

	for _, wildcard := range []rbac.ObjectAction{
		{Namespace: "*", Object: "*", Action: "*"},
		{Namespace: "**", Object: "**", Action: "**"},
		// The subject is joined with "::", so a pattern spanning the separators
		// is the other shape worth trying.
		{Namespace: "*::*::*", Object: "*", Action: "*"},
	} {
		key := stack.putAPIKey(t, admin.ID, "wildcard "+wildcard.Namespace, wildcard)

		id, matched, err := stack.authenticator.Authenticate(t.Context(), key)
		require.NoError(t, err)
		require.True(t, matched)
		require.NotNil(t, id.Self().APIKey)

		allow, err := id.Enforce(t.Context(), scopeTestObjectAction)
		require.NoError(t, err)

		assert.False(t, allow,
			"a key scoped to %q must not reach %v; wildcards belong to the policy, not the request",
			wildcard.Namespace, scopeTestObjectAction)
	}
}

// The service refuses the same patterns before they are ever stored. This is
// the outer half of the guarantee above; both matter, because only one of them
// protects a row that is already in the table.
func TestCreateAPIKeyRefusesWildcardsBeforeStoringThem(t *testing.T) {
	t.Parallel()

	stack := newScopeStack(t)
	admin := stack.registerAdmin(t)

	_, err := stack.apiKeyService.Create(t.Context(), api_key.CreateRequest{
		UserID:      admin.ID,
		Name:        "wildcard",
		Permissions: []rbac.ObjectAction{{Namespace: "*", Object: "*", Action: "*"}},
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, api_key.ErrPermissionInvalid)
}
