package user

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/atomic"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/time/rate"
	"gorm.io/gorm"
)

type LoginResult struct {
	Token string
	User  model.User
}

func (s *service) Login(ctx context.Context, username, password string) (LoginResult, error) {
	if err := s.loginLimiter.Wait(ctx); err != nil {
		return LoginResult{}, fmt.Errorf("%w: %w", Err, ErrLoginRequestLimiter)
	}

	dao, err := s.Dao()
	if err != nil {
		return LoginResult{}, fmt.Errorf("%w: %w: %w", Err, ErrLogin, err)
	}

	user, err := dao.WithContext(ctx).
		User.
		Where(dao.User.Username.Eq(username)).
		First()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return LoginResult{}, fmt.Errorf("%w: %w: %w", Err, ErrLogin, err)
	}

	// A missing account, an account with no password set, and a wrong password
	// are one outcome to the caller. Reporting them apart lets anyone enumerate
	// usernames, and returning early for the first two skipped bcrypt entirely,
	// so the response time told them the same thing. Comparing against a decoy
	// hash keeps the cost of a miss close to the cost of a hit.
	var storedHash []byte
	if user != nil {
		storedHash = user.Password
	}

	found := user != nil && len(storedHash) > 0
	if !found {
		storedHash = s.decoyPasswordHash()
	}

	if bcrypt.CompareHashAndPassword(storedHash, []byte(password)) != nil || !found {
		return LoginResult{}, fmt.Errorf("%w: %w: %w", Err, ErrLogin, ErrCredentialsInvalid)
	}

	if !user.Enabled {
		return LoginResult{}, fmt.Errorf("%w: %w: %w", Err, ErrLogin, ErrDisabled)
	}

	token, err := s.jwtService.Generate(user.ID, user.Username)
	if err != nil {
		return LoginResult{}, fmt.Errorf("%w: %w: %w: %w", Err, ErrLogin, ErrGenerateToken, err)
	}

	// Update last login
	_, _ = dao.WithContext(ctx).
		User.
		Where(dao.User.ID.Eq(user.ID)).
		UpdateSimple(
			dao.User.LastLoginAt.Value(sql.NullTime{Time: time.Now(), Valid: true}),
		)

	// Clear password from response
	user.Password = nil

	return LoginResult{
		Token: token,
		User:  *user,
	}, nil
}

func (rpm LoginRequestsPerMinute) limit() rate.Limit {
	return rate.Every(time.Minute / time.Duration(rpm))
}

func newLoginLimiter(
	rpm *atomic.Value[LoginRequestsPerMinute],
	burst *atomic.Value[LoginRequestBurst],
) *rate.Limiter {
	currentRpm, currentBurst := rpm.Get(), burst.Get()
	limiter := rate.NewLimiter(
		currentRpm.limit(),
		int(currentBurst),
	)

	rpm.Subscribe(func(rpm LoginRequestsPerMinute) {
		if rpm != currentRpm {
			currentRpm = rpm
			limiter.SetLimit(currentRpm.limit())
		}
	})

	burst.Subscribe(func(burst LoginRequestBurst) {
		if burst != currentBurst {
			currentBurst = burst
			limiter.SetBurst(int(currentBurst))
		}
	})

	return limiter
}

// decoyPasswordHash is a hash of a value no caller can supply, compared against
// when there is no stored hash so that the work done for an unknown account
// matches the work done for a known one. Generated once, at the configured cost,
// so it tracks whatever bcrypt work factor the deployment uses.
func (s *service) decoyPasswordHash() []byte {
	s.decoyOnce.Do(func() {
		secret := make([]byte, 32)
		_, _ = rand.Read(secret)

		s.decoyHash, _ = bcrypt.GenerateFromPassword(secret, int(s.passwordHashingCost.Get()))
	})

	return s.decoyHash
}
