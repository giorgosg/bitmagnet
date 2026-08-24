package authfx_test

import (
	"errors"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/authfx"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/http_auth"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/identity"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/user"
	"github.com/bitmagnet-io/bitmagnet/internal/config"
	"github.com/bitmagnet-io/bitmagnet/internal/database"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
)

// errStubDao is returned rather than a nil query, so a test that accidentally
// reaches the database fails loudly instead of panicking.
var errStubDao = errors.New("stub dao provider")

type stubDaoProvider struct{}

func (stubDaoProvider) Dao() (*dao.Query, error) { return nil, errStubDao }

func (stubDaoProvider) DaoTransaction(func(tx *dao.Query) error) error { return errStubDao }

// testApp supplies the pieces the auth module expects from the wider graph:
// a database provider and an already-resolved config node.
func testApp(cfg authconfig.Config, invokes ...any) fx.Option {
	opts := make([]fx.Option, 0, 4+len(invokes))
	opts = append(opts,
		authfx.New(),
		fx.Provide(func() database.DaoTransactionProvider { return stubDaoProvider{} }),
		fx.Supply(config.ResolvedConfig{
			NodeMap: map[string]config.ResolvedNode{"auth": {Value: cfg}},
		}),
		fx.NopLogger,
	)

	for _, i := range invokes {
		opts = append(opts, fx.Invoke(i))
	}

	return fx.Options(opts...)
}

func TestModuleProvidesTheAuthSurface(t *testing.T) {
	t.Parallel()

	var (
		middleware    http_auth.Middleware
		authenticator identity.Authenticator
		userService   user.Service
		enforcer      rbac.Enforcer
	)

	app := fx.New(testApp(
		authconfig.NewDefaultConfig(),
		func(
			m http_auth.Middleware,
			a identity.Authenticator,
			u user.Service,
			e rbac.Enforcer,
		) {
			middleware, authenticator, userService, enforcer = m, a, u, e
		},
	))

	require.NoError(t, app.Err())

	assert.NotNil(t, middleware)
	assert.NotNil(t, authenticator)
	assert.NotNil(t, userService)
	assert.NotNil(t, enforcer)
}
