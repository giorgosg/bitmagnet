package rbac_test

import (
	"context"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/database"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testDaoProvider struct {
	query *dao.Query
}

func (p testDaoProvider) Dao() (*dao.Query, error) {
	return p.query, nil
}

func (p testDaoProvider) DaoTransaction(fn func(tx *dao.Query) error) error {
	return p.query.Transaction(fn)
}

func newTestRepository(t *testing.T) rbac.Repository {
	t.Helper()

	db := dbtest.New(t)

	var provider database.DaoTransactionProvider = testDaoProvider{query: db.Query}

	return rbac.NewRepository(provider)
}

// PutRole is the only way to change a role's permissions — there is no
// DeleteRolePermissions. Passing an empty set should therefore revoke them all.
func TestPutRoleRevokesAllPermissions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newTestRepository(t)

	role := rbac.Role("test_role")
	objAct := rbac.NewObjectAction("foo", "bar", "baz")

	granted, err := repo.PutRole(ctx, role, []rbac.ObjectAction{objAct})
	require.NoError(t, err)
	require.Len(t, granted.Permissions, 1)

	revoked, err := repo.PutRole(ctx, role, nil)
	require.NoError(t, err)

	assert.Empty(t, revoked.Permissions, "empty permission set should revoke everything")

	reread, err := repo.GetRole(ctx, role)
	require.NoError(t, err)
	assert.Empty(t, reread.Permissions, "revocation should be persisted")
}

// Independently of the revocation question, PutRole must not report success
// while returning a zero-valued RoleInfo — a caller cannot tell the two apart.
func TestPutRoleWithNoPermissionsReturnsTheRole(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newTestRepository(t)

	role := rbac.Role("test_role")

	info, err := repo.PutRole(ctx, role, nil)
	require.NoError(t, err)

	assert.Equal(t, role, info.Role, "PutRole returned a zero-valued RoleInfo with a nil error")
}
