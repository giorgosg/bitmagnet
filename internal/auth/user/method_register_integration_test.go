package user_test

import (
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/jwt"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/user"
	"github.com/bitmagnet-io/bitmagnet/internal/database"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type daoProvider struct{ query *dao.Query }

func (p daoProvider) Dao() (*dao.Query, error) { return p.query, nil }

func (p daoProvider) DaoTransaction(fn func(tx *dao.Query) error) error {
	return p.query.Transaction(fn)
}

func newUserService(t *testing.T) (user.Service, *dao.Query) {
	t.Helper()

	db := dbtest.New(t)

	var provider database.DaoTransactionProvider = daoProvider{query: db.Query}

	values := authconfig.NewDefaultConfig().UserValues()

	return user.NewService(
		provider,
		jwt.NewService(jwt.Secret("test-secret"), jwt.Duration(time.Hour)),
		values.InvitationRequired,
		values.EmailRequired,
		values.EmailVerification,
		values.PasswordMinEntropy,
		values.PasswordHashingCost,
		values.LoginRequestsPerMinute,
		values.LoginRequestBurst,
	), db.Query
}

// putInvitation writes an invitation directly, so the expiry can be placed in
// the past — Invite only ever sets it forward from now.
func putInvitation(t *testing.T, query *dao.Query, code string, expiresAt sql.NullTime) {
	t.Helper()

	require.NoError(t, query.WithContext(t.Context()).Invitation.Create(&model.Invitation{
		Code:      code,
		RoleName:  "user",
		ExpiresAt: expiresAt,
	}))
}

const testPassword = "correct-horse-battery-staple-99"

// An invitation whose expiry has passed must be refused.
func TestRegisterRejectsExpiredInvitation(t *testing.T) {
	t.Parallel()

	service, query := newUserService(t)

	putInvitation(t, query, "expiredcode00", sql.NullTime{
		Time:  time.Now().Add(-time.Hour),
		Valid: true,
	})

	_, err := service.Register(t.Context(), user.RegisterRequest{
		InvitationCode: "expiredcode00",
		Username:       "someone",
		Password:       testPassword,
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, user.ErrInvitationExpired)
}

// And one that has not yet expired must be accepted — the mirror case, because
// an inverted comparison passes one of these while failing the other.
func TestRegisterAcceptsUnexpiredInvitation(t *testing.T) {
	t.Parallel()

	service, query := newUserService(t)

	putInvitation(t, query, "validcode0001", sql.NullTime{
		Time:  time.Now().Add(time.Hour),
		Valid: true,
	})

	registered, err := service.Register(t.Context(), user.RegisterRequest{
		InvitationCode: "validcode0001",
		Username:       "someone",
		Password:       testPassword,
	})

	require.NoError(t, err)
	assert.Equal(t, "someone", registered.Username)
}

// An invitation with no expiry never expires; this is what the bootstrap
// administrator invitation relies on.
func TestRegisterAcceptsInvitationWithoutExpiry(t *testing.T) {
	t.Parallel()

	service, query := newUserService(t)

	putInvitation(t, query, "noexpiry00001", sql.NullTime{})

	registered, err := service.Register(t.Context(), user.RegisterRequest{
		InvitationCode: "noexpiry00001",
		Username:       "someone",
		Password:       testPassword,
	})

	require.NoError(t, err)
	assert.Equal(t, "someone", registered.Username)
}

// SetRole must change exactly one user. The update was written without a
// predicate, so gorm refused it as a global update — meaning the method always
// failed, and would have reassigned every user's role had that guard not
// existed. Both halves are asserted: the target changes, the bystander does not.
func TestSetRoleAffectsOnlyTheTargetUser(t *testing.T) {
	t.Parallel()

	service, query := newUserService(t)

	putInvitation(t, query, "roletarget001", sql.NullTime{})
	putInvitation(t, query, "rolebystand01", sql.NullTime{})

	target, err := service.Register(t.Context(), user.RegisterRequest{
		InvitationCode: "roletarget001",
		Username:       "target",
		Password:       testPassword,
	})
	require.NoError(t, err)

	bystander, err := service.Register(t.Context(), user.RegisterRequest{
		InvitationCode: "rolebystand01",
		Username:       "bystander",
		Password:       testPassword,
	})
	require.NoError(t, err)
	require.Equal(t, "user", bystander.RoleName)

	updated, err := service.SetRole(t.Context(), target.ID, "admin")
	require.NoError(t, err)
	assert.Equal(t, "admin", updated.RoleName)

	unchanged, err := service.Get(t.Context(), bystander.ID)
	require.NoError(t, err)
	assert.Equal(t, "user", unchanged.RoleName, "SetRole must not touch other users")
}

// An invitation is single use, including when several registrations race for it.
//
// This is not a hypothetical: before the claim was made atomic, every one of
// the eight racers succeeded under load, because each read the invitation as
// unclaimed and the update that marked it claimed carried no predicate. For the
// bootstrap invitation that means several administrators from one code.
func TestInvitationIsSingleUseUnderConcurrency(t *testing.T) {
	t.Parallel()

	service, query := newUserService(t)
	putInvitation(t, query, "oneusecode01", sql.NullTime{})

	const callers = 8

	var (
		wg        sync.WaitGroup
		successes atomic.Int32
	)

	start := make(chan struct{})

	for i := range callers {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			<-start

			if _, err := service.Register(t.Context(), user.RegisterRequest{
				InvitationCode: "oneusecode01",
				Username:       fmt.Sprintf("racer%02d", i),
				Password:       testPassword,
			}); err == nil {
				successes.Add(1)
			}
		}(i)
	}

	close(start)
	wg.Wait()

	claimed := successes.Load()
	assert.Equal(t, int32(1), claimed, "exactly one registration may claim an invitation")

	// The database must agree with what the callers were told.
	registered, err := query.WithContext(t.Context()).User.Count()
	require.NoError(t, err)
	assert.Equal(t, int64(claimed), registered, "a user exists for each successful claim, and no more")

	invitation, err := query.WithContext(t.Context()).
		Invitation.Where(query.Invitation.Code.Eq("oneusecode01")).First()
	require.NoError(t, err)

	if claimed == 1 {
		assert.True(t, invitation.ClaimedBy.Valid, "a claimed invitation records its claimant")
	}
}

// Login must not distinguish a missing account from a wrong password, in either
// the message or the time taken. Both let a caller enumerate usernames.
func TestLoginDoesNotEnumerateAccounts(t *testing.T) {
	t.Parallel()

	service, query := newUserService(t)
	putInvitation(t, query, "enumtest00001", sql.NullTime{})

	_, err := service.Register(t.Context(), user.RegisterRequest{
		InvitationCode: "enumtest00001",
		Username:       "known",
		Password:       testPassword,
	})
	require.NoError(t, err)

	_, errUnknown := service.Login(t.Context(), "nosuchuser", testPassword)
	require.Error(t, errUnknown)

	_, errWrongPassword := service.Login(t.Context(), "known", "wrong-password-entirely")
	require.Error(t, errWrongPassword)

	assert.Equal(t, errUnknown.Error(), errWrongPassword.Error(),
		"an unknown account and a wrong password must be indistinguishable")
	require.ErrorIs(t, errUnknown, user.ErrCredentialsInvalid)
	require.ErrorIs(t, errWrongPassword, user.ErrCredentialsInvalid)
}

// The timing must not give it away either: before, a missing account returned
// without ever reaching bcrypt, which is a difference of two orders of magnitude.
func TestLoginTimingDoesNotRevealAccountExistence(t *testing.T) {
	t.Parallel()

	service, query := newUserService(t)
	putInvitation(t, query, "timingtest001", sql.NullTime{})

	_, err := service.Register(t.Context(), user.RegisterRequest{
		InvitationCode: "timingtest001",
		Username:       "known",
		Password:       testPassword,
	})
	require.NoError(t, err)

	measure := func(username string) time.Duration {
		start := time.Now()
		_, _ = service.Login(t.Context(), username, "wrong-password-entirely")

		return time.Since(start)
	}

	// Warm the decoy hash so its one-off generation is not counted.
	measure("nosuchuser")

	known := measure("known")
	unknown := measure("nosuchuser")

	ratio := float64(unknown) / float64(known)
	assert.Greater(t, ratio, 0.25, "an unknown account must not be dramatically faster (%v vs %v)", unknown, known)
	assert.Less(t, ratio, 4.0, "an unknown account must not be dramatically slower (%v vs %v)", unknown, known)
}
