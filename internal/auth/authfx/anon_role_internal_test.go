package authfx

import (
	"context"
	"sync"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/database"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
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
		Dao:    provider,
		Config: cfg,
		Logger: zap.NewNop().Sugar(),
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
		Where(query.KeyValue.Key.Eq(anonRoleEmptyDefaultKey)).
		Count()
	require.NoError(t, err)

	return count > 0
}

func TestNewInstallationStartsWithAnEmptyAnonRole(t *testing.T) {
	t.Parallel()

	p, query := newTranslationParams(t, true)

	require.NoError(t, translateAnonRole(t.Context(), p))

	assert.Empty(t, anonPermissions(t, query),
		"a fresh installation must require credentials for search and Torznab")
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

// The fixture option grants only the prior read surface; production does not
// call this helper.
func TestFixtureReadSurfaceGrantsNoWritesAndNoExcludedObjects(t *testing.T) {
	t.Parallel()

	p, query := newTranslationParams(t, true)

	require.NoError(t, translateAnonRole(t.Context(), p))
	require.NoError(t, GrantAnonReadSurface(t.Context(), p.Dao, testObjectActions))

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
		Create(&model.KeyValue{Key: anonRoleEmptyDefaultKey, Value: "false"}))
	require.NoError(t, query.RolePermission.WithContext(t.Context()).Create(
		&model.RolePermission{
			RoleName:  string(rbac.RoleAnon),
			Namespace: "gql",
			Object:    "torrent",
			Action:    "query",
		}))

	require.NoError(t, translateAnonRole(t.Context(), p))

	assert.Contains(t, anonPermissions(t, query), "torrent:query",
		"grants made after the upgrade must survive a restart")
}

// Prior seed grants and administrator grants have identical rows. The
// one-time upgrade clears both, then leaves later edits alone.
func TestTranslationClearsPreExistingGrants(t *testing.T) {
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

	assert.Empty(t, granted)
}

// The seed overlapping an existing row must not fail the insert: ON CONFLICT DO
// NOTHING on the composite primary key, not a plain create.
func TestTranslationToleratesAnOverlappingGrant(t *testing.T) {
	t.Parallel()

	p, query := newTranslationParams(t, true)

	require.NoError(t, translateAnonRole(t.Context(), p))
	require.NoError(t, query.RolePermission.WithContext(t.Context()).
		Create(&model.RolePermission{
			RoleName:  string(rbac.RoleAnon),
			Namespace: "gql",
			Object:    "torrent",
			Action:    "query",
		}))

	require.NoError(t, GrantAnonReadSurface(t.Context(), p.Dao, testObjectActions))

	assert.Contains(t, anonPermissions(t, query), "torrent:query")
}

func TestUpgradeClearsExistingAnonymousGrantsOnce(t *testing.T) {
	t.Parallel()

	p, query := newTranslationParams(t, true)

	require.NoError(t, query.KeyValue.WithContext(t.Context()).Create(
		&model.KeyValue{Key: "auth.anon_role_translated", Value: "true"}))
	require.NoError(t, query.RolePermission.WithContext(t.Context()).Create(
		&model.RolePermission{
			RoleName:  string(rbac.RoleAnon),
			Namespace: "gql",
			Object:    "torrent",
			Action:    "query",
		}))

	require.NoError(t, translateAnonRole(t.Context(), p))

	assert.Empty(t, anonPermissions(t, query), "the upgrade must close existing installations")

	require.NoError(t, query.RolePermission.WithContext(t.Context()).Create(
		&model.RolePermission{
			RoleName:  string(rbac.RoleAnon),
			Namespace: "gql",
			Object:    "torrent",
			Action:    "query",
		}))
	require.NoError(t, translateAnonRole(t.Context(), p))

	assert.Contains(t, anonPermissions(t, query), "torrent:query",
		"a restart must preserve grants made after the upgrade")
}

func TestConcurrentUpgradeClearsAnonymousGrantsOnce(t *testing.T) {
	t.Parallel()

	p, query := newTranslationParams(t, true)

	require.NoError(t, query.RolePermission.WithContext(t.Context()).Create(
		&model.RolePermission{
			RoleName:  string(rbac.RoleAnon),
			Namespace: "gql",
			Object:    "torrent",
			Action:    "query",
		}))

	start := make(chan struct{})
	errors := make(chan error, 2)

	var workers sync.WaitGroup

	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()

			<-start

			errors <- translateAnonRole(t.Context(), p)
		}()
	}

	close(start)
	workers.Wait()
	close(errors)

	for err := range errors {
		require.NoError(t, err)
	}

	assert.Empty(t, anonPermissions(t, query))
	assert.True(t, markerIsSet(t, query))
}

func TestHTTPOnlyWorkerResetsAnonRoleBeforeListening(t *testing.T) {
	t.Parallel()

	p, query := newTranslationParams(t, true)
	require.NoError(t, query.RolePermission.WithContext(t.Context()).Create(
		&model.RolePermission{
			RoleName:  string(rbac.RoleAnon),
			Namespace: "gql",
			Object:    "torrent",
			Action:    "query",
		}))

	listened := false
	httpWorker := worker.NewWorker("http_server", fx.Hook{
		OnStart: func(ctx context.Context) error {
			count, err := query.RolePermission.WithContext(ctx).
				Where(query.RolePermission.RoleName.Eq(string(rbac.RoleAnon))).Count()
			require.NoError(t, err)
			assert.Zero(t, count, "the HTTP listener must not open with old grants")

			listened = true

			return nil
		},
	})
	result, err := worker.NewRegistry(worker.RegistryParams{
		Workers:    []worker.Worker{httpWorker},
		Decorators: []worker.Decorator{newAnonRoleHTTPDecorator(p)},
		Logger:     zap.NewNop().Sugar(),
	})
	require.NoError(t, err)
	require.NoError(t, result.Registry.Enable("http_server"))
	require.NoError(t, result.Registry.Start(t.Context()))
	assert.True(t, listened)
	assert.True(t, markerIsSet(t, query))
}
