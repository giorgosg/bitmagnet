package rbac_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blockingRepository is a Repository whose PutRole blocks until the test
// releases it, standing in for a slow database write.
type blockingRepository struct {
	putRoleEntered chan struct{}
	releasePutRole chan struct{}
}

func newBlockingRepository() *blockingRepository {
	return &blockingRepository{
		putRoleEntered: make(chan struct{}),
		releasePutRole: make(chan struct{}),
	}
}

func (r *blockingRepository) PutRole(
	context.Context,
	rbac.Role,
	[]rbac.ObjectAction,
) (rbac.RoleInfo, error) {
	close(r.putRoleEntered)
	<-r.releasePutRole

	return rbac.RoleInfo{Role: "slow"}, nil
}

func (*blockingRepository) GetPermissions(context.Context) ([]rbac.Permission, error) {
	return nil, nil
}

func (*blockingRepository) GetAllRoles(context.Context) ([]rbac.RoleInfo, error) {
	return nil, nil
}

func (*blockingRepository) GetRole(context.Context, rbac.Role) (rbac.RoleInfo, error) {
	return rbac.RoleInfo{}, nil
}

func (*blockingRepository) GetRoles(context.Context, []rbac.Role) ([]rbac.RoleInfo, error) {
	return nil, nil
}

func (*blockingRepository) DeleteRole(context.Context, rbac.Role) error { return nil }

func newBlockingHarness(t *testing.T) (rbac.Service, *blockingRepository) {
	t.Helper()

	repo := newBlockingRepository()

	return rbac.NewService(
		repo,
		func() []rbac.ObjectAction { return nil },
		rbac.CorePermissions,
		rbac.CacheTTL(time.Minute),
	), repo
}

// Authorization is on the path of every GraphQL field carrying an @auth
// directive, every Torznab request and every guarded endpoint. It must not
// queue behind a role write, which holds the database for as long as the
// database takes.
func TestEnforceDoesNotQueueBehindARoleWrite(t *testing.T) {
	t.Parallel()

	service, repo := newBlockingHarness(t)

	// Compile the policy first, so the decision below needs no reload and is
	// waiting for nothing but the lock.
	_, err := service.Enforce(
		t.Context(),
		rbac.SubjectRole{Role: rbac.RoleAdmin},
		rbac.NewObjectAction("graphql", "version", "query"),
	)
	require.NoError(t, err)

	writeDone := make(chan error, 1)

	go func() {
		_, putErr := service.PutRole(t.Context(), "slow", nil)
		writeDone <- putErr
	}()

	<-repo.putRoleEntered

	decided := make(chan error, 1)

	go func() {
		_, enforceErr := service.Enforce(
			t.Context(),
			rbac.SubjectRole{Role: rbac.RoleAdmin},
			rbac.NewObjectAction("graphql", "version", "query"),
		)
		decided <- enforceErr
	}()

	select {
	case enforceErr := <-decided:
		require.NoError(t, enforceErr)
	case <-time.After(2 * time.Second):
		t.Fatal("an authorization decision blocked on an in-flight role write")
	}

	close(repo.releasePutRole)
	require.NoError(t, <-writeDone)
}

// The decisions themselves have to be unchanged, and unchanged under load: the
// race detector over concurrent enforcement is the point of this one.
func TestConcurrentEnforcementAgreesWithSerialEnforcement(t *testing.T) {
	t.Parallel()

	service, _ := newBlockingHarness(t)

	allowed := rbac.NewObjectAction("graphql", "version", "query")

	expected, err := service.Enforce(t.Context(), rbac.SubjectRole{Role: rbac.RoleAdmin}, allowed)
	require.NoError(t, err)
	require.True(t, expected, "an administrator holds the core wildcard permission")

	anonymous, err := service.Enforce(t.Context(), rbac.SubjectRole{Role: rbac.RoleAnon}, allowed)
	require.NoError(t, err)

	var wg sync.WaitGroup

	for range 50 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			admin, enforceErr := service.Enforce(
				t.Context(), rbac.SubjectRole{Role: rbac.RoleAdmin}, allowed)
			assert.NoError(t, enforceErr)
			assert.Equal(t, expected, admin)

			anon, enforceErr := service.Enforce(
				t.Context(), rbac.SubjectRole{Role: rbac.RoleAnon}, allowed)
			assert.NoError(t, enforceErr)
			assert.Equal(t, anonymous, anon)
		}()
	}

	wg.Wait()
}
