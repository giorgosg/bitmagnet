package user_test

import (
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/user"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countInvitations reports how many invitations exist. Every test here asserts
// on it: the point of a retrieval path is that it reads, and a create-or-return
// passes every other assertion in this file while quietly minting a permanent
// administrator credential.
func countInvitations(t *testing.T, query *dao.Query) int64 {
	t.Helper()

	count, err := query.WithContext(t.Context()).Invitation.Count()
	require.NoError(t, err)

	return count
}

// The state an operator hits on an instance whose workers have never run, or
// whose invitation was deleted. Reporting it must not be the same thing as
// creating an administrator invitation on an instance deliberately left without
// one.
func TestGetInitialInvitationDoesNotMintOne(t *testing.T) {
	t.Parallel()

	svc, query := newUserService(t)

	result, err := svc.GetInitialInvitation(t.Context())
	require.NoError(t, err)

	assert.Equal(t, user.InitialInvitationNone, result.Status,
		"nothing is outstanding, and that has to be said rather than invented")
	assert.Empty(t, result.Code)
	assert.Zero(t, countInvitations(t, query),
		"a retrieval command must never create an administrator invitation")
}

// The case the command exists for: the code was logged once at startup and the
// operator did not capture it.
func TestGetInitialInvitationReturnsTheOutstandingCode(t *testing.T) {
	t.Parallel()

	svc, query := newUserService(t)

	created, err := svc.CreateInitialInvitation(t.Context())
	require.NoError(t, err)
	require.Equal(t, user.InitialInvitationCreated, created.Status)

	result, err := svc.GetInitialInvitation(t.Context())
	require.NoError(t, err)

	assert.Equal(t, user.InitialInvitationUnclaimed, result.Status)
	assert.Equal(t, created.Code, result.Code,
		"the command has to print the same code the creation log line carried")
	assert.EqualValues(t, 1, countInvitations(t, query))
}

// Running it twice is the normal case — an operator reruns a command whose
// output scrolled away. It must be idempotent in the strong sense: same answer,
// no new row.
func TestGetInitialInvitationIsRepeatable(t *testing.T) {
	t.Parallel()

	svc, query := newUserService(t)

	created, err := svc.CreateInitialInvitation(t.Context())
	require.NoError(t, err)

	first, err := svc.GetInitialInvitation(t.Context())
	require.NoError(t, err)

	second, err := svc.GetInitialInvitation(t.Context())
	require.NoError(t, err)

	assert.Equal(t, created.Code, first.Code)
	assert.Equal(t, first.Code, second.Code)
	assert.EqualValues(t, 1, countInvitations(t, query),
		"repeating a read must not accumulate invitations")
}

// Once the invitation has been claimed there is an administrator, and nothing
// is outstanding. This also pins the acceptance criterion that the code the
// command prints is the one that produces an administrator.
func TestGetInitialInvitationReportsAnExistingAdministrator(t *testing.T) {
	t.Parallel()

	svc, query := newUserService(t)

	created, err := svc.CreateInitialInvitation(t.Context())
	require.NoError(t, err)

	registered, err := svc.Register(t.Context(), user.RegisterRequest{
		Username:       "first-admin",
		Password:       testPassword,
		InvitationCode: created.Code,
	})
	require.NoError(t, err)
	require.Equal(t, rbac.RoleAdmin.String(), registered.RoleName,
		"the bootstrap code is only worth printing if it grants admin")

	result, err := svc.GetInitialInvitation(t.Context())
	require.NoError(t, err)

	assert.Equal(t, user.InitialInvitationNotRequired, result.Status)
	assert.Empty(t, result.Code,
		"a claimed code is spent; printing it would only mislead")
	assert.EqualValues(t, 1, countInvitations(t, query),
		"an instance that already has an administrator must not be given a second way in")
}

// An invitation somebody issued through the API is not the bootstrap one, even
// when it names the admin role. Returning it would hand out a credential whose
// custodian is a different person, and it can carry an expiry the bootstrap
// path never sets.
func TestGetInitialInvitationIgnoresAnOperatorIssuedAdminInvitation(t *testing.T) {
	t.Parallel()

	// Open registration, so the inviter can exist without consuming the very
	// invitation this test is about.
	svc, query := newUserServiceWithConfig(t, func(c *authconfig.Config) {
		c.InvitationRequired = false
	})

	inviter, err := svc.Register(t.Context(), user.RegisterRequest{
		Username: "someone",
		Password: testPassword,
	})
	require.NoError(t, err)

	_, err = svc.Invite(t.Context(), user.InviteRequest{
		Role:      rbac.RoleAdmin.String(),
		CreatedBy: inviter.ID,
	})
	require.NoError(t, err)

	result, err := svc.GetInitialInvitation(t.Context())
	require.NoError(t, err)

	assert.Equal(t, user.InitialInvitationNone, result.Status,
		"only the unattributed bootstrap invitation is the one this reports")
	assert.Empty(t, result.Code)
	assert.EqualValues(t, 1, countInvitations(t, query))
}
