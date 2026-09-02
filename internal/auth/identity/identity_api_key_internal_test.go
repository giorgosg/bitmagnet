package identity

import (
	"context"
	"slices"
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

func (*recordingEnforcer) FilterAllowed(
	_ context.Context, _ []rbac.Subject, _ []rbac.ObjectAction,
) ([]rbac.ObjectAction, error) {
	return nil, nil
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

// filteringEnforcer answers FilterAllowed from a fixed allow-list per subject,
// standing in for casbin. The real agreement between FilterAllowed and Enforce
// is covered in package rbac; what matters here is which subjects the identity
// asks about.
type filteringEnforcer struct {
	recordingEnforcer
	allowedBySubject map[string][]rbac.ObjectAction
	filterSubjects   [][]rbac.Subject
}

func (f *filteringEnforcer) FilterAllowed(
	_ context.Context, subjects []rbac.Subject, objectActions []rbac.ObjectAction,
) ([]rbac.ObjectAction, error) {
	f.filterSubjects = append(f.filterSubjects, subjects)

	var allowed []rbac.ObjectAction

	for _, objectAction := range objectActions {
		for _, subject := range subjects {
			if slices.Contains(f.allowedBySubject[subject.SubjectName()], objectAction) {
				allowed = append(allowed, objectAction)
				break
			}
		}
	}

	return allowed, nil
}

var (
	selectedAction = rbac.NewObjectAction("graphql", "torrent", "delete")
	anonAction     = rbac.NewObjectAction("graphql", "version", "query")
)

func newFilteringAPIKey(enforcer rbac.Enforcer) APIKey {
	return APIKey{
		APIKey: model.APIKey{
			User: model.User{RoleName: "editor"},
			Permissions: []model.APIKeyPermission{
				{
					Namespace: selectedAction.Namespace,
					Object:    selectedAction.Object,
					Action:    selectedAction.Action,
				},
			},
		},
		anon: rbac.RoleInfo{
			Role: rbac.RoleAnon,
			Permissions: []rbac.Permission{
				rbac.NewPermission(rbac.SubjectRole{Role: rbac.RoleAnon}, anonAction),
			},
		},
		userRole: rbac.RoleInfo{Role: rbac.Role("editor")},
		enforcer: enforcer,
	}
}

// Enforce requires the owning User's Role and (the key's selection or anon).
// The reported set was the selection concatenated with anon's, never
// intersected with the Role, so it claimed authority the enforcer refuses.
func TestAPIKeyEffectivePermissionsAreIntersectedWithTheOwningRole(t *testing.T) {
	t.Parallel()

	enforcer := &filteringEnforcer{
		allowedBySubject: map[string][]rbac.ObjectAction{
			// The role allows only what anon also offers, and not the key's own
			// selection - so the selection must not be reported.
			"editor": {anonAction},
		},
	}

	permissions, err := newFilteringAPIKey(enforcer).EffectivePermissions(t.Context())
	require.NoError(t, err)

	assert.Equal(t, []rbac.ObjectAction{anonAction}, permissions)
	assert.NotContains(t, permissions, selectedAction,
		"an action the owning Role denies must not be reported as held")
}

// The candidate set is the key's own selection plus the anon role's, because
// that is exactly what the second gate accepts. The filter is asked about the
// owning Role, which is the first gate.
func TestAPIKeyEffectivePermissionsAskAboutTheOwningRole(t *testing.T) {
	t.Parallel()

	enforcer := &filteringEnforcer{
		allowedBySubject: map[string][]rbac.ObjectAction{
			"editor": {selectedAction, anonAction},
		},
	}

	permissions, err := newFilteringAPIKey(enforcer).EffectivePermissions(t.Context())
	require.NoError(t, err)

	assert.ElementsMatch(t, []rbac.ObjectAction{selectedAction, anonAction}, permissions)

	require.Len(t, enforcer.filterSubjects, 1, "one question, one acquisition")
	assert.Equal(t,
		[]rbac.Subject{rbac.SubjectRole{Role: rbac.Role("editor")}},
		enforcer.filterSubjects[0],
		"the gate being filtered on is the owning User's Role")
}
