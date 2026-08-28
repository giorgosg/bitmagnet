package rbac_test

import (
	"errors"
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

	repo := rbac_mocks.NewRepository(t)

	return testHarness{
		repository: repo,
		service: rbac.NewService(
			repo,
			func() []rbac.ObjectAction {
				return []rbac.ObjectAction{}
			},
			rbac.CorePermissions,
			rbac.CacheTTL(time.Minute),
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
