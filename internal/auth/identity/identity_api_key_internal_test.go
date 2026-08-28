package identity

import (
	"context"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingEnforcer counts how many times an authorization decision reaches the
// rbac service. Each of those calls acquires the service's process-global
// semaphore, which is the cost this is here to measure.
type recordingEnforcer struct {
	calls  int
	groups [][]rbac.Subject
	allow  bool
}

func (r *recordingEnforcer) Enforce(
	_ context.Context, subject rbac.Subject, _ rbac.ObjectAction,
) (bool, error) {
	r.calls++
	r.groups = append(r.groups, []rbac.Subject{subject})

	return r.allow, nil
}

func (r *recordingEnforcer) EnforceAny(
	_ context.Context, subjects []rbac.Subject, _ rbac.ObjectAction,
) (bool, error) {
	r.calls++
	r.groups = append(r.groups, subjects)

	return r.allow, nil
}

func (r *recordingEnforcer) EnforceEvery(
	_ context.Context, groups [][]rbac.Subject, _ rbac.ObjectAction,
) (bool, error) {
	r.calls++
	r.groups = append(r.groups, groups...)

	return r.allow, nil
}

func newTestAPIKey(enforcer rbac.Enforcer) APIKey {
	return APIKey{
		APIKey: model.APIKey{
			User: model.User{RoleName: "editor"},
			Permissions: []model.APIKeyPermission{
				{Namespace: "torrent", Object: "torrent", Action: "query"},
			},
		},
		anon:     rbac.RoleInfo{Role: rbac.RoleAnon},
		enforcer: enforcer,
	}
}

// The @auth directive fires per field, and the rbac service serialises every
// decision through a one-slot semaphore. Asking two questions for one decision
// therefore doubles the globally-serialised acquisitions per field.
func TestAPIKeyEnforceMakesOneAuthorizationCall(t *testing.T) {
	t.Parallel()

	enforcer := &recordingEnforcer{allow: true}

	allow, err := newTestAPIKey(enforcer).
		Enforce(t.Context(), rbac.NewObjectAction("torrent", "torrent", "query"))

	require.NoError(t, err)
	assert.True(t, allow)
	assert.Equal(t, 1, enforcer.calls,
		"one decision must cost one acquisition of the rbac semaphore, not two")
}

// The decision itself is unchanged: the User's role must allow, and the key's own
// permissions or the anonymous role must also allow.
func TestAPIKeyEnforceAsksRoleAndScopeSeparately(t *testing.T) {
	t.Parallel()

	enforcer := &recordingEnforcer{allow: true}

	_, err := newTestAPIKey(enforcer).
		Enforce(t.Context(), rbac.NewObjectAction("torrent", "torrent", "query"))
	require.NoError(t, err)

	require.Len(t, enforcer.groups, 2, "a role gate and a scope gate")
	assert.Equal(t, []rbac.Subject{rbac.SubjectRole{Role: rbac.Role("editor")}}, enforcer.groups[0])
	assert.Equal(t, []rbac.Subject{
		rbac.SubjectPermission{ObjectAction: rbac.NewObjectAction("torrent", "torrent", "query")},
		rbac.SubjectRole{Role: rbac.RoleAnon},
	}, enforcer.groups[1])
}

func TestAPIKeyEnforceDeniesWhenTheEnforcerDenies(t *testing.T) {
	t.Parallel()

	enforcer := &recordingEnforcer{allow: false}

	allow, err := newTestAPIKey(enforcer).
		Enforce(t.Context(), rbac.NewObjectAction("torrent", "torrent", "query"))

	require.NoError(t, err)
	assert.False(t, allow)
}
