package user_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/jwt"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/user"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type bootstrapBarrierProvider struct {
	query   *dao.Query
	ready   chan struct{}
	release <-chan struct{}
}

func (p bootstrapBarrierProvider) Dao() (*dao.Query, error) { return p.query, nil }

func (p bootstrapBarrierProvider) DaoTransaction(fn func(tx *dao.Query) error) error {
	return p.query.Transaction(func(tx *dao.Query) error {
		p.ready <- struct{}{}
		<-p.release

		return fn(tx)
	})
}

// Startup is allowed to run in more than one process against the same
// database. Concurrent bootstraps must converge on one administrator
// invitation; every extra code is another permanent administrator credential.
func TestConcurrentBootstrapCreatesOneInitialInvitation(t *testing.T) {
	t.Parallel()

	const callers = 16

	db := dbtest.New(t)

	// Hold inserts briefly after every transaction has observed the empty
	// bootstrap state. Without a database uniqueness constraint, they can then
	// all commit different invitation codes.
	require.NoError(t, db.Gorm.Exec(`
		CREATE FUNCTION security_review_slow_invitation_insert()
		RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_sleep(0.2);
			RETURN NEW;
		END;
		$$;
		CREATE TRIGGER security_review_slow_invitation_insert
		BEFORE INSERT ON invitations
		FOR EACH ROW EXECUTE FUNCTION security_review_slow_invitation_insert();
	`).Error)

	launch := make(chan struct{})
	release := make(chan struct{})
	ready := make(chan struct{}, callers)
	results := make(chan user.InitialInvitation, callers)
	errs := make(chan error, callers)

	values := authconfig.NewDefaultConfig().UserValues()
	service := user.NewService(
		bootstrapBarrierProvider{query: db.Query, ready: ready, release: release},
		jwt.NewService(jwt.Secret("test-secret"), jwt.Duration(time.Hour)),
		values.InvitationRequired,
		values.EmailRequired,
		values.EmailVerification,
		values.PasswordMinEntropy,
		values.PasswordHashingCost,
		values.LoginRequestsPerMinute,
		values.LoginRequestBurst,
	)

	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)

		go func() {
			defer wg.Done()
			<-launch

			result, err := service.CreateInitialInvitation(context.Background())
			results <- result
			errs <- err
		}()
	}

	close(launch)

	for range callers {
		<-ready
	}

	close(release)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}

	codes := make(map[string]struct{})
	for result := range results {
		codes[result.Code] = struct{}{}
	}

	count, err := db.Query.WithContext(context.Background()).Invitation.Count()
	require.NoError(t, err)

	assert.Equal(t, int64(1), count, "bootstrap must persist exactly one administrator invitation")
	assert.Len(t, codes, 1, "all replicas must report the same administrator invitation")
}

// One caller must not consume the login budget for every account in the
// process. A global limiter lets an anonymous flood prevent all legitimate
// users from logging in, even when the attempted usernames are unrelated.
func TestLoginLimiterIsNotGlobalAcrossAccounts(t *testing.T) {
	t.Parallel()

	service, query := newUserService(t)
	putInvitation(t, query, "limittest0001", sql.NullTime{})

	_, err := service.Register(context.Background(), user.RegisterRequest{
		InvitationCode: "limittest0001",
		Username:       "legitimate",
		Password:       testPassword,
	})
	require.NoError(t, err)

	// Consume the default burst with attempts against unrelated accounts.
	for i := range 5 {
		_, err = service.Login(context.Background(), "attacker"+string(rune('a'+i)), "wrong-password")
		require.Error(t, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err = service.Login(ctx, "legitimate", testPassword)
	assert.NotErrorIs(t, err, user.ErrLoginRequestLimiter,
		"unrelated anonymous attempts must not lock every account out")
}

// Reject a nonexistent invitation before invoking the deliberately expensive
// password hasher. Otherwise an unauthenticated caller can submit arbitrary
// nonempty codes in parallel and consume one bcrypt operation per request.
func TestUnknownInvitationIsRejectedBeforePasswordHashing(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	values := authconfig.NewDefaultConfig().UserValues()

	// An impossible cost is a deterministic tripwire: reaching bcrypt returns
	// an InvalidCostError immediately instead of the invitation error.
	values.PasswordHashingCost.Set(user.PasswordHashingCost(32))

	service := user.NewService(
		daoProvider{query: db.Query},
		jwt.NewService(jwt.Secret("test-secret"), jwt.Duration(time.Hour)),
		values.InvitationRequired,
		values.EmailRequired,
		values.EmailVerification,
		values.PasswordMinEntropy,
		values.PasswordHashingCost,
		values.LoginRequestsPerMinute,
		values.LoginRequestBurst,
	)

	_, err := service.Register(context.Background(), user.RegisterRequest{
		InvitationCode: "does-not-exist",
		Username:       "attacker",
		Password:       testPassword,
	})

	assert.ErrorIs(t, err, user.ErrInvitationNotFound,
		"unknown invitation codes must be rejected before bcrypt")
}
