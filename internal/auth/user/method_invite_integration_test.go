package user_test

import (
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/user"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// InviteInput.role is optional in the schema, and omitting it works: model.
// Invitation tags role_name `default:user`, so gorm omits the zero value and
// lets the column default in migrations/00022 apply, then reads it back.
//
// Recorded because review finding 17 claimed the opposite — that "" is written
// verbatim and violates the foreign key. It does not, and this test is what
// says so. Invite now supplies the default explicitly, because a validated
// name has to be a real one before the insert rather than after it.
func TestInviteWithoutARoleUsesTheDefaultRole(t *testing.T) {
	t.Parallel()

	svc, _ := newUserService(t)

	invitation, err := svc.Invite(t.Context(), user.InviteRequest{})
	require.NoError(t, err, "role is optional; omitting it must not fail")

	assert.Equal(t, rbac.RoleUser.String(), invitation.RoleName,
		"an omitted role must fall back to the same default registration uses")
}

// A misspelled role reached the database as a foreign-key violation, which the
// GraphQL boundary presents as INTERNAL_SERVER_ERROR. It is a caller error and
// has to be classified as one.
func TestInviteRejectsAnUnknownRole(t *testing.T) {
	t.Parallel()

	svc, _ := newUserService(t)

	_, err := svc.Invite(t.Context(), user.InviteRequest{Role: "administrator"})

	require.Error(t, err)
	assert.ErrorIs(t, err, user.ErrRoleNotFound,
		"an unknown role is a caller error, not a database failure")
}

// A role name becomes a casbin policy subject wherever it is used, so the
// invitation path must refuse a glob too. Checking the name against the roles
// table covers it without a second rule: no role is named "*".
func TestInviteRejectsAGlobRole(t *testing.T) {
	t.Parallel()

	svc, _ := newUserService(t)

	_, err := svc.Invite(t.Context(), user.InviteRequest{Role: "*"})

	require.Error(t, err)
	assert.ErrorIs(t, err, user.ErrRoleNotFound)
}

// The control: a role that exists is still accepted and recorded verbatim.
func TestInviteAcceptsAKnownRole(t *testing.T) {
	t.Parallel()

	svc, _ := newUserService(t)

	invitation, err := svc.Invite(t.Context(), user.InviteRequest{Role: rbac.RoleAdmin.String()})
	require.NoError(t, err)

	assert.Equal(t, rbac.RoleAdmin.String(), invitation.RoleName)
}
