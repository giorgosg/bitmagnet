package user_test

import (
	"context"
	"database/sql"
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

	require.NoError(t, query.WithContext(context.Background()).Invitation.Create(&model.Invitation{
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

	_, err := service.Register(context.Background(), user.RegisterRequest{
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

	registered, err := service.Register(context.Background(), user.RegisterRequest{
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

	registered, err := service.Register(context.Background(), user.RegisterRequest{
		InvitationCode: "noexpiry00001",
		Username:       "someone",
		Password:       testPassword,
	})

	require.NoError(t, err)
	assert.Equal(t, "someone", registered.Username)
}
