package authfx

import (
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/database"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type testDaoProvider struct {
	query *dao.Query
}

func (p testDaoProvider) Dao() (*dao.Query, error) { return p.query, nil }

func (p testDaoProvider) DaoTransaction(fn func(tx *dao.Query) error) error {
	return p.query.Transaction(fn)
}

// testObjectActions spans what the translation has to discriminate between: two
// read actions it should grant, a mutate and a delete it must not, and the three
// excluded objects whose read action it must also withhold.
func testObjectActions() []rbac.ObjectAction {
	return []rbac.ObjectAction{
		rbac.NewObjectAction("gql", "torrent", "query"),
		rbac.NewObjectAction("gql", "torrent", "mutate"),
		rbac.NewObjectAction("gql", "torrent", "delete"),
		rbac.NewObjectAction("gql", "queue", "query"),
		rbac.NewObjectAction("gql", "queue", "mutate"),
		rbac.NewObjectAction("gql", "auth", "query"),
		rbac.NewObjectAction("gql", "auth", "mutate"),
		rbac.NewObjectAction("http", "pprof", "query"),
		rbac.NewObjectAction("http", "metrics", "query"),
	}
}

func newTranslationParams(t *testing.T, anonymousAccess bool) (anonRoleParams, *dao.Query) {
	t.Helper()

	db := dbtest.New(t)

	var provider database.DaoTransactionProvider = testDaoProvider{query: db.Query}

	cfg := authconfig.NewDefaultConfig()
	cfg.AnonymousAccess = anonymousAccess

	return anonRoleParams{
		Dao:           provider,
		Config:        cfg,
		ObjectActions: testObjectActions,
		Logger:        zap.NewNop().Sugar(),
	}, db.Query
}

// anonPermissions reads the stored grants for the anon role as "object:action",
// which is what the assertions below are about — the namespace is not what the
// read-surface rule discriminates on.
func anonPermissions(t *testing.T, query *dao.Query) map[string]struct{} {
	t.Helper()

	perms, err := query.RolePermission.WithContext(t.Context()).
		Where(query.RolePermission.RoleName.Eq(string(rbac.RoleAnon))).
		Find()
	require.NoError(t, err)

	granted := make(map[string]struct{}, len(perms))
	for _, perm := range perms {
		granted[perm.Object+":"+perm.Action] = struct{}{}
	}

	return granted
}

func markerIsSet(t *testing.T, query *dao.Query) bool {
	t.Helper()

	count, err := query.KeyValue.WithContext(t.Context()).
		Where(query.KeyValue.Key.Eq(anonRoleTranslationKey)).
		Count()
	require.NoError(t, err)

	return count > 0
}

func TestTranslationWritesTheReadSurfaceWhenAccessIsOpen(t *testing.T) {
	t.Parallel()

	p, query := newTranslationParams(t, true)

	require.NoError(t, translateAnonRole(t.Context(), p))

	granted := anonPermissions(t, query)

	assert.Contains(t, granted, "torrent:query")
	assert.Contains(t, granted, "queue:query")
	assert.Len(t, granted, 2, "only the two non-excluded read actions should be granted")
	assert.True(t, markerIsSet(t, query), "the marker must be set")
}

func TestTranslationWritesNothingWhenAccessIsClosed(t *testing.T) {
	t.Parallel()

	p, query := newTranslationParams(t, false)

	require.NoError(t, translateAnonRole(t.Context(), p))

	assert.Empty(t, anonPermissions(t, query))
	assert.True(t, markerIsSet(t, query),
		"the marker must be set even when nothing was granted, so a later start does not re-seed")
}

// The written set is the whole point of the exclusions: a mutate or delete
// reaching the anon role is the defect #81 fixed, and auth is the one that makes
// the instance unrecoverable.
func TestTranslationGrantsNoWritesAndNoExcludedObjects(t *testing.T) {
	t.Parallel()

	p, query := newTranslationParams(t, true)

	require.NoError(t, translateAnonRole(t.Context(), p))

	for objectAction := range anonPermissions(t, query) {
		assert.NotContains(t, objectAction, ":mutate")
		assert.NotContains(t, objectAction, ":delete")
	}

	granted := anonPermissions(t, query)

	assert.NotContains(t, granted, "auth:query")
	assert.NotContains(t, granted, "pprof:query")
	assert.NotContains(t, granted, "metrics:query")
}

func TestTranslationIsIdempotent(t *testing.T) {
	t.Parallel()

	p, query := newTranslationParams(t, true)

	require.NoError(t, translateAnonRole(t.Context(), p))

	first := anonPermissions(t, query)

	require.NoError(t, translateAnonRole(t.Context(), p))

	assert.Equal(t, first, anonPermissions(t, query),
		"a second run must not change the stored grants")
}

// Once the marker is set the flag is never consulted again: an administrator who
// revoked anonymous access through the role must not have it handed back on the
// next restart.
func TestTranslationDoesNothingWhenTheMarkerIsAlreadySet(t *testing.T) {
	t.Parallel()

	p, query := newTranslationParams(t, true)

	require.NoError(t, query.KeyValue.WithContext(t.Context()).
		Create(&model.KeyValue{Key: anonRoleTranslationKey, Value: "false"}))

	require.NoError(t, translateAnonRole(t.Context(), p))

	assert.Empty(t, anonPermissions(t, query),
		"nothing should be written when the marker is already present")
}

// An administrator who granted anon something before upgrading keeps it. The
// translation adds the read surface beside it rather than replacing the set.
func TestTranslationPreservesPreExistingGrants(t *testing.T) {
	t.Parallel()

	p, query := newTranslationParams(t, true)

	require.NoError(t, query.RolePermission.WithContext(t.Context()).
		Create(&model.RolePermission{
			RoleName:  string(rbac.RoleAnon),
			Namespace: "gql",
			Object:    "torrent",
			Action:    "mutate",
		}))

	require.NoError(t, translateAnonRole(t.Context(), p))

	granted := anonPermissions(t, query)

	assert.Contains(t, granted, "torrent:mutate", "a pre-existing grant must survive")
	assert.Contains(t, granted, "torrent:query")
	assert.Contains(t, granted, "queue:query")
}

// The seed overlapping an existing row must not fail the insert: ON CONFLICT DO
// NOTHING on the composite primary key, not a plain create.
func TestTranslationToleratesAnOverlappingGrant(t *testing.T) {
	t.Parallel()

	p, query := newTranslationParams(t, true)

	require.NoError(t, query.RolePermission.WithContext(t.Context()).
		Create(&model.RolePermission{
			RoleName:  string(rbac.RoleAnon),
			Namespace: "gql",
			Object:    "torrent",
			Action:    "query",
		}))

	require.NoError(t, translateAnonRole(t.Context(), p),
		"a row the seed also wants must not fail the translation")

	assert.Contains(t, anonPermissions(t, query), "torrent:query")
}
