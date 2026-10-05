package rbac_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	rbac_mocks "github.com/bitmagnet-io/bitmagnet/internal/auth/rbac/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testHarness struct {
	repository *rbac_mocks.Repository
	service    rbac.Service
}

func newTestHarness(t *testing.T) testHarness {
	t.Helper()

	return newTestHarnessWithTTL(t, time.Minute)
}

func newTestHarnessWithTTL(t *testing.T, ttl time.Duration) testHarness {
	t.Helper()

	return newTestHarnessWithAnonymousAccess(t, ttl, true)
}

func newTestHarnessWithAnonymousAccess(
	t *testing.T,
	ttl time.Duration,
	anonymousAccess bool,
) testHarness {
	t.Helper()

	repo := rbac_mocks.NewRepository(t)

	return testHarness{
		repository: repo,
		service: rbac.NewService(
			repo,
			func() []rbac.ObjectAction {
				return []rbac.ObjectAction{}
			},
			rbac.CorePermissions,
			rbac.CacheTTL(ttl),
			rbac.AnonymousAccess(anonymousAccess),
		),
	}
}

func TestService_no_persisted_permissions(t *testing.T) {
	t.Parallel()

	test := newTestHarness(t)

	test.repository.EXPECT().
		GetPermissions(t.Context()).
		Return([]rbac.Permission{}, nil).
		Once()

	// admins can do anything:
	allow, err := test.service.Enforce(
		t.Context(),
		rbac.SubjectRole{Role: rbac.RoleAdmin},
		rbac.NewObjectAction("foo", "bar", "baz"),
	)

	require.NoError(t, err)
	assert.True(t, allow)

	// unknown roles can do nothing:
	allow, err = test.service.Enforce(
		t.Context(),
		rbac.SubjectRole{Role: rbac.Role("unknown")},
		rbac.NewObjectAction("foo", "bar", "baz"),
	)

	require.NoError(t, err)
	assert.False(t, allow)

	// subject including both admin and unknown roles should be allowed with EnforceAny:
	allow, err = test.service.EnforceAny(
		t.Context(),
		[]rbac.Subject{
			rbac.SubjectRole{Role: rbac.Role("unknown")},
			rbac.SubjectRole{Role: rbac.RoleAdmin},
		},
		rbac.NewObjectAction("foo", "bar", "baz"),
	)

	require.NoError(t, err)
	assert.True(t, allow)

	// // subject including both admin and unknown roles should not be allowed with EnforceAll:
	// allow, err = test.service.EnforceAll(
	// 	context.Background(),
	// 	[]rbac.Subject{
	// 		rbac.SubjectRole{Role: rbac.Role("unknown")},
	// 		rbac.SubjectRole{Role: rbac.RoleAdmin},
	// 	},
	// 	rbac.NewObjectAction("foo", "bar", "baz"),
	// )

	// require.NoError(t, err)
	// assert.False(t, allow)

	// get all permissions should return core permissions:
	permissions, err := test.service.GetPermissions(t.Context())
	require.NoError(t, err)
	assert.NotEmpty(t, permissions)
	assert.Equal(t, rbac.CorePermissions(), permissions)
}

func TestService_persist_permissions(t *testing.T) {
	t.Parallel()

	test := newTestHarness(t)

	test.repository.EXPECT().
		GetPermissions(t.Context()).
		Return([]rbac.Permission{}, nil).
		Once()

	// unknown role can initially do nothing:
	allow, err := test.service.Enforce(
		t.Context(),
		rbac.SubjectRole{Role: rbac.Role("unknown")},
		rbac.NewObjectAction("foo", "bar", "baz"),
	)

	require.NoError(t, err)
	assert.False(t, allow)

	test.repository.EXPECT().
		PutRole(
			t.Context(),
			rbac.Role("unknown"),
			[]rbac.ObjectAction{
				rbac.NewObjectAction("foo", "bar", "baz"),
			},
		).
		Return(rbac.RoleInfo{}, nil).
		Once()

	test.repository.EXPECT().
		GetPermissions(t.Context()).
		Return([]rbac.Permission{
			rbac.NewPermission(
				rbac.SubjectRole{Role: rbac.Role("unknown")},
				rbac.NewObjectAction("foo", "bar", "baz"),
			),
		}, nil).
		Once()

	// persist a new permission:
	_, err = test.service.PutRole(
		t.Context(),
		rbac.Role("unknown"),
		[]rbac.ObjectAction{rbac.NewObjectAction("foo", "bar", "baz")},
	)
	require.NoError(t, err)

	// unknown role can now baz a foobar:
	allow, err = test.service.Enforce(
		t.Context(),
		rbac.SubjectRole{Role: rbac.Role("unknown")},
		rbac.NewObjectAction("foo", "bar", "baz"),
	)

	require.NoError(t, err)
	assert.True(t, allow)
}

func TestService_DeleteRole_propagates_repository_error(t *testing.T) {
	t.Parallel()

	test := newTestHarness(t)

	// Initialise the casbin cache, as any process that has served one request will have.
	test.repository.EXPECT().
		GetPermissions(t.Context()).
		Return([]rbac.Permission{}, nil).
		Once()

	_, err := test.service.Enforce(
		t.Context(),
		rbac.SubjectRole{Role: rbac.RoleAdmin},
		rbac.NewObjectAction("foo", "bar", "baz"),
	)
	require.NoError(t, err)

	errDelete := errors.New("delete failed")

	test.repository.EXPECT().
		DeleteRole(t.Context(), rbac.Role("unknown")).
		Return(errDelete).
		Once()

	// The permission reload that follows succeeds - the role is still there and nothing
	// is wrong with it - and must not mask the failed delete.
	test.repository.EXPECT().
		GetPermissions(t.Context()).
		Return([]rbac.Permission{}, nil).
		Maybe()

	require.ErrorIs(
		t,
		test.service.DeleteRole(t.Context(), rbac.Role("unknown")),
		errDelete,
	)
}

// The casbin matcher is globMatch(r.sub, p.sub), and the *stored* policy is the
// pattern rather than the request. A role named "*" would therefore match every
// subject, anon included, so its permissions would be granted to everyone. Names
// are rejected before they reach the repository, which is what the mock asserts:
// an unexpected PutRole call fails the test on its own.
func TestService_put_role_rejects_glob_patterns(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"*", "**", "adm*", "?dmin", "a[dm]in", "{admin,user}", "a\\*b"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			test := newTestHarness(t)

			_, err := test.service.PutRole(t.Context(), rbac.Role(name),
				[]rbac.ObjectAction{rbac.NewObjectAction("torrent", "", "delete")})

			require.ErrorIs(t, err, rbac.ErrRoleNameInvalid)
		})
	}
}

func TestService_put_role_rejects_names_outside_the_character_class(t *testing.T) {
	t.Parallel()

	for name, why := range map[string]string{
		"":                      "empty",
		".leading":              "leading punctuation",
		"trailing-":             "trailing punctuation",
		"has space":             "whitespace",
		"emoji😀":                "outside the class",
		strings.Repeat("a", 33): "too long",
	} {
		t.Run(why, func(t *testing.T) {
			t.Parallel()

			test := newTestHarness(t)

			_, err := test.service.PutRole(t.Context(), rbac.Role(name),
				[]rbac.ObjectAction{rbac.NewObjectAction("torrent", "", "delete")})

			require.ErrorIs(t, err, rbac.ErrRoleNameInvalid)
		})
	}
}

// Names that were always legitimate must still reach the repository - including
// the core roles, whose permissions an administrator may edit, and short names
// that a username's minimum length would have rejected.
func TestService_put_role_accepts_ordinary_names(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"admin", "editor", "user", "anon", "ops", "a", "read-only", "team.one", "a_b"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			test := newTestHarness(t)
			role := rbac.Role(name)
			actions := []rbac.ObjectAction{rbac.NewObjectAction("torrent", "", "delete")}

			test.repository.EXPECT().
				PutRole(t.Context(), role, actions).
				Return(rbac.RoleInfo{Role: role}, nil).
				Once()

			_, err := test.service.PutRole(t.Context(), role, actions)
			require.NoError(t, err)
		})
	}
}

// Every authentication resolves a role - anonymous, JWT and API-key alike - and
// the lookup went straight to the database each time, bypassing the TTL cache that
// already exists for the compiled policy. `Preload(dao.Role.Permissions)` makes it
// two statements, and \*arr clients poll continuously, so this is the steady-state
// cost of an idle instance.
func TestService_get_role_is_cached(t *testing.T) {
	t.Parallel()

	test := newTestHarness(t)

	test.repository.EXPECT().
		GetAllRoles(t.Context()).
		Return([]rbac.RoleInfo{{Role: rbac.Role("custom")}}, nil).
		Once()

	for range 5 {
		info, err := test.service.GetRole(t.Context(), rbac.RoleAdmin)
		require.NoError(t, err)
		assert.Equal(t, rbac.RoleAdmin, info.Role)
	}
}

func TestService_get_all_roles_is_cached(t *testing.T) {
	t.Parallel()

	test := newTestHarness(t)

	test.repository.EXPECT().
		GetAllRoles(t.Context()).
		Return([]rbac.RoleInfo{{Role: rbac.Role("custom")}}, nil).
		Once()

	for range 5 {
		roles, err := test.service.GetAllRoles(t.Context())
		require.NoError(t, err)
		assert.NotEmpty(t, roles)
	}
}

// The cache is bounded by the same RBACCacheTTL as the compiled policy, so a role
// changed by another process becomes visible on the same schedule.
func TestService_role_cache_expires(t *testing.T) {
	t.Parallel()

	test := newTestHarnessWithTTL(t, 0)

	test.repository.EXPECT().
		GetAllRoles(t.Context()).
		Return([]rbac.RoleInfo{{Role: rbac.Role("custom")}}, nil).
		Twice()

	for range 2 {
		_, err := test.service.GetAllRoles(t.Context())
		require.NoError(t, err)
	}
}

// An administrator who writes a role must see it immediately, rather than waiting
// out the TTL for a change this process made itself.
func TestService_put_role_invalidates_the_role_cache(t *testing.T) {
	t.Parallel()

	test := newTestHarness(t)
	role := rbac.Role("custom")

	test.repository.EXPECT().
		GetAllRoles(t.Context()).
		Return([]rbac.RoleInfo{{Role: role}}, nil).
		Twice()
	test.repository.EXPECT().
		PutRole(t.Context(), role, []rbac.ObjectAction{}).
		Return(rbac.RoleInfo{Role: role}, nil).
		Once()

	_, err := test.service.GetAllRoles(t.Context())
	require.NoError(t, err)

	_, err = test.service.PutRole(t.Context(), role, []rbac.ObjectAction{})
	require.NoError(t, err)

	_, err = test.service.GetAllRoles(t.Context())
	require.NoError(t, err)
}

func TestService_delete_role_invalidates_the_role_cache(t *testing.T) {
	t.Parallel()

	test := newTestHarness(t)
	role := rbac.Role("custom")

	test.repository.EXPECT().
		GetAllRoles(t.Context()).
		Return([]rbac.RoleInfo{{Role: role}}, nil).
		Twice()
	test.repository.EXPECT().
		DeleteRole(t.Context(), role).
		Return(nil).
		Once()

	_, err := test.service.GetAllRoles(t.Context())
	require.NoError(t, err)

	require.NoError(t, test.service.DeleteRole(t.Context(), role))

	_, err = test.service.GetAllRoles(t.Context())
	require.NoError(t, err)
}

// FilterAllowed answers "which of these object actions may this subject
// perform" through casbin, so a caller that has to report an identity's
// effective set never has to re-derive the glob semantics of the matcher.
// Reimplementing them is how a report comes to disagree with enforcement,
// which is the defect it exists to prevent.
func TestService_filter_allowed_agrees_with_enforce(t *testing.T) {
	t.Parallel()

	test := newTestHarness(t)

	editorAction := rbac.NewObjectAction("graphql", "torrent", "query")
	adminAction := rbac.NewObjectAction("graphql", "auth", "mutate")

	test.repository.EXPECT().
		GetPermissions(t.Context()).
		Return([]rbac.Permission{
			rbac.NewPermission(rbac.SubjectRole{Role: rbac.RoleEditor}, editorAction),
		}, nil).
		Once()

	candidates := []rbac.ObjectAction{editorAction, adminAction}

	allowed, err := test.service.FilterAllowed(
		t.Context(),
		[]rbac.Subject{rbac.SubjectRole{Role: rbac.RoleEditor}},
		candidates,
	)
	require.NoError(t, err)
	assert.Equal(t, []rbac.ObjectAction{editorAction}, allowed)

	// The control: whatever the filter returns, Enforce must agree one by one.
	for _, candidate := range candidates {
		expected, err := test.service.Enforce(
			t.Context(), rbac.SubjectRole{Role: rbac.RoleEditor}, candidate,
		)
		require.NoError(t, err)
		assert.Equal(t, expected, slices.Contains(allowed, candidate),
			"FilterAllowed and Enforce disagree about %v", candidate)
	}
}

// The admin role's permission is stored as the pattern "**::**::**", so a
// caller intersecting concrete sets by equality would report that an admin
// holds nothing. casbin globs the stored value against the request; the filter
// has to inherit that, not approximate it.
func TestService_filter_allowed_honours_wildcard_policies(t *testing.T) {
	t.Parallel()

	test := newTestHarness(t)

	test.repository.EXPECT().
		GetPermissions(t.Context()).
		Return([]rbac.Permission{}, nil).
		Once()

	candidates := []rbac.ObjectAction{
		rbac.NewObjectAction("graphql", "torrent", "query"),
		rbac.NewObjectAction("http", "metrics", "query"),
	}

	allowed, err := test.service.FilterAllowed(
		t.Context(),
		[]rbac.Subject{rbac.SubjectRole{Role: rbac.RoleAdmin}},
		candidates,
	)
	require.NoError(t, err)
	assert.Equal(t, candidates, allowed, "admin's ** policy covers every candidate")
}

// Any one subject allowing the action is enough, matching EnforceAny — an API
// key's second gate is satisfied by its own selection or by the anon role.
func TestService_filter_allowed_accepts_any_subject(t *testing.T) {
	t.Parallel()

	test := newTestHarness(t)

	action := rbac.NewObjectAction("graphql", "torrent", "query")

	test.repository.EXPECT().
		GetPermissions(t.Context()).
		Return([]rbac.Permission{
			rbac.NewPermission(rbac.SubjectRole{Role: rbac.RoleEditor}, action),
		}, nil).
		Once()

	allowed, err := test.service.FilterAllowed(
		t.Context(),
		[]rbac.Subject{
			rbac.SubjectRole{Role: rbac.Role("unknown")},
			rbac.SubjectRole{Role: rbac.RoleEditor},
		},
		[]rbac.ObjectAction{action},
	)
	require.NoError(t, err)
	assert.Equal(t, []rbac.ObjectAction{action}, allowed)
}

func TestService_filter_allowed_short_circuits_on_empty_input(t *testing.T) {
	t.Parallel()

	test := newTestHarness(t)

	// No repository call is expected: with nothing to decide there is nothing
	// to ask casbin, so the semaphore is not taken either.
	allowed, err := test.service.FilterAllowed(
		t.Context(), []rbac.Subject{rbac.SubjectRole{Role: rbac.RoleAdmin}}, nil,
	)
	require.NoError(t, err)
	assert.Empty(t, allowed)

	allowed, err = test.service.FilterAllowed(
		t.Context(), nil, []rbac.ObjectAction{rbac.NewObjectAction("a", "b", "c")},
	)
	require.NoError(t, err)
	assert.Empty(t, allowed)
}

// The point of the redesign: `auth.anonymous_access: false` must withhold what
// the database grants anon, not merely what the config used to grant in memory.
// Before this, a stored anon permission survived the flag and the instance stayed
// open with nothing in the configuration or the logs saying so — docs/issues/0012.
func TestService_anonymous_access_off_denies_stored_anon_permissions(t *testing.T) {
	t.Parallel()

	test := newTestHarnessWithAnonymousAccess(t, time.Minute, false)

	objAct := rbac.NewObjectAction("foo", "bar", "baz")

	test.repository.EXPECT().
		GetPermissions(t.Context()).
		Return([]rbac.Permission{
			rbac.NewPermission(rbac.SubjectRole{Role: rbac.RoleAnon}, objAct),
		}, nil).
		Once()

	allow, err := test.service.Enforce(t.Context(), rbac.SubjectRole{Role: rbac.RoleAnon}, objAct)

	require.NoError(t, err)
	assert.False(t, allow, "a stored anon grant must not survive anonymous_access: false")
}

// A wildcard is the case that matters most: it is what an anonymous caller could
// write for itself through putRole while the old baseline granted auth:mutate,
// and it is the one a subject filter has to catch as surely as an exact grant.
func TestService_anonymous_access_off_denies_a_stored_anon_wildcard(t *testing.T) {
	t.Parallel()

	test := newTestHarnessWithAnonymousAccess(t, time.Minute, false)

	test.repository.EXPECT().
		GetPermissions(t.Context()).
		Return([]rbac.Permission{
			rbac.NewPermission(
				rbac.SubjectRole{Role: rbac.RoleAnon},
				rbac.NewObjectAction("**", "**", "**"),
			),
		}, nil).
		Once()

	allow, err := test.service.Enforce(
		t.Context(),
		rbac.SubjectRole{Role: rbac.RoleAnon},
		rbac.NewObjectAction("foo", "bar", "baz"),
	)

	require.NoError(t, err)
	assert.False(t, allow, "a stored anon wildcard must not survive anonymous_access: false")
}

// The override is scoped to anon. Switching it off must not disturb any other
// role, or it would be an outage rather than a setting.
func TestService_anonymous_access_off_leaves_other_roles_alone(t *testing.T) {
	t.Parallel()

	test := newTestHarnessWithAnonymousAccess(t, time.Minute, false)

	objAct := rbac.NewObjectAction("foo", "bar", "baz")

	test.repository.EXPECT().
		GetPermissions(t.Context()).
		Return([]rbac.Permission{
			rbac.NewPermission(rbac.SubjectRole{Role: rbac.RoleAnon}, objAct),
			rbac.NewPermission(rbac.SubjectRole{Role: rbac.Role("ops")}, objAct),
		}, nil).
		Once()

	allow, err := test.service.Enforce(t.Context(), rbac.SubjectRole{Role: rbac.Role("ops")}, objAct)

	require.NoError(t, err)
	assert.True(t, allow, "only the anon subject is filtered")
}

// Allow carries no permission set of its own any more: it defers to the role, so
// anon may exactly what the database says and nothing besides.
func TestService_anonymous_access_on_grants_exactly_the_stored_set(t *testing.T) {
	t.Parallel()

	test := newTestHarnessWithAnonymousAccess(t, time.Minute, true)

	granted := rbac.NewObjectAction("foo", "bar", "query")
	withheld := rbac.NewObjectAction("foo", "bar", "mutate")

	test.repository.EXPECT().
		GetPermissions(t.Context()).
		Return([]rbac.Permission{
			rbac.NewPermission(rbac.SubjectRole{Role: rbac.RoleAnon}, granted),
		}, nil).
		Once()

	allow, err := test.service.Enforce(t.Context(), rbac.SubjectRole{Role: rbac.RoleAnon}, granted)
	require.NoError(t, err)
	assert.True(t, allow)

	allow, err = test.service.Enforce(t.Context(), rbac.SubjectRole{Role: rbac.RoleAnon}, withheld)
	require.NoError(t, err)
	assert.False(t, allow, "allow grants the stored set, not a surface of its own")
}

// The override has to survive a reload, not just the first compile: PutRole
// recompiles the policy, and the filter lives in both paths.
func TestService_anonymous_access_off_survives_a_reload(t *testing.T) {
	t.Parallel()

	test := newTestHarnessWithAnonymousAccess(t, time.Minute, false)

	objAct := rbac.NewObjectAction("foo", "bar", "baz")
	anonPerms := []rbac.Permission{
		rbac.NewPermission(rbac.SubjectRole{Role: rbac.RoleAnon}, objAct),
	}

	test.repository.EXPECT().GetPermissions(t.Context()).Return(anonPerms, nil)
	test.repository.EXPECT().
		PutRole(t.Context(), rbac.Role("ops"), []rbac.ObjectAction{objAct}).
		Return(rbac.RoleInfo{Role: rbac.Role("ops")}, nil).
		Once()

	// Compile once, then force a reload through a role write.
	_, err := test.service.Enforce(t.Context(), rbac.SubjectRole{Role: rbac.RoleAnon}, objAct)
	require.NoError(t, err)

	_, err = test.service.PutRole(t.Context(), rbac.Role("ops"), []rbac.ObjectAction{objAct})
	require.NoError(t, err)

	allow, err := test.service.Enforce(t.Context(), rbac.SubjectRole{Role: rbac.RoleAnon}, objAct)

	require.NoError(t, err)
	assert.False(t, allow, "the reloaded policy must filter anon too")
}

// Filtering by "subject is the anon role" is sufficient only because no other
// stored subject can glob-match it: casbin's matcher is globMatch(r.sub, p.sub),
// so the stored policy is the pattern. Role.Validate is what holds that, and this
// test is here so that relaxing it fails loudly rather than silently reopening
// anonymous access.
func TestService_no_other_subject_can_glob_match_anon(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"*", "**", "an*", "anon*", "?non", "[a]non"} {
		require.Error(t, rbac.Role(name).Validate(),
			"a role name that could glob-match anon must be rejected: %q", name)
	}

	test := newTestHarnessWithAnonymousAccess(t, time.Minute, false)

	// Even if such a subject reached the policy, it must not carry anon.
	test.repository.EXPECT().
		GetPermissions(t.Context()).
		Return([]rbac.Permission{
			rbac.NewPermission(
				rbac.SubjectRole{Role: rbac.Role("*")},
				rbac.NewObjectAction("foo", "bar", "baz"),
			),
		}, nil).
		Once()

	allow, err := test.service.Enforce(
		t.Context(),
		rbac.SubjectRole{Role: rbac.RoleAnon},
		rbac.NewObjectAction("foo", "bar", "baz"),
	)

	require.NoError(t, err)
	assert.False(t, allow, "a wildcard subject must not hand anon a permission")
}
