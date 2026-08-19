package user_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/jwt"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/user"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// auth.password_hashing_cost is the operator's control over how expensive a
// stored password is to attack offline. Raising it is the entire remedy when
// hardware gets faster, and it is worth nothing if only some of the code paths
// that write a password hash consult it.
func TestPasswordHashingCostAppliesToEveryStoredHash(t *testing.T) {
	t.Parallel()

	// One above the default, so a hash written at the default is distinguishable
	// from one written at the configured cost.
	const configuredCost = bcrypt.DefaultCost + 1

	db := dbtest.New(t)
	values := authconfig.NewDefaultConfig().UserValues()
	values.PasswordHashingCost.Set(user.PasswordHashingCost(configuredCost))

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

	putInvitation(t, db.Query, "costcheck001", sql.NullTime{})

	registered, err := service.Register(context.Background(), user.RegisterRequest{
		InvitationCode: "costcheck001",
		Username:       "costuser",
		Password:       testPassword,
	})
	require.NoError(t, err)

	storedCost := func() int {
		t.Helper()

		stored, err := db.Query.WithContext(context.Background()).
			User.Where(db.Query.User.ID.Eq(registered.ID)).First()
		require.NoError(t, err)

		cost, err := bcrypt.Cost(stored.Password)
		require.NoError(t, err)

		return cost
	}

	assert.Equal(t, configuredCost, storedCost(), "Register must hash at the configured cost")

	const newPassword = "another-correct-horse-battery-77"

	require.NoError(t, service.UpdatePassword(
		context.Background(), registered.ID, testPassword, newPassword,
	))

	assert.Equal(t, configuredCost, storedCost(), "UpdatePassword must hash at the configured cost")
}
