package enhancement

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

var (
	ErrBudget = errors.New("automatic spending budget unavailable")
	ErrPaused = errors.New("automatic enhancement paused; check configuration and resume explicitly")
	ErrLease  = errors.New("enhancement claim expired")
)

const OutcomeDeferred = "deferred"

type (
	automaticKey struct{}
	accounting   struct {
		db     *gorm.DB
		config Config
		claim  string
	}
)

func automaticContext(ctx context.Context, a *accounting) context.Context {
	return context.WithValue(ctx, automaticKey{}, a)
}

func automaticAccounting(ctx context.Context) *accounting {
	a, _ := ctx.Value(automaticKey{}).(*accounting)
	return a
}
func newToken() string { return fmt.Sprintf("%x", rand.Text()) }

// A reservation is charged immediately. Crashes and unknown usage never refund it.
// Only deleting the unique live reservation can reconcile it, once, to known usage.
func (a *accounting) reserve(ctx context.Context, cost float64) (string, error) {
	token := newToken()
	err := a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := schedulerLock(tx); err != nil {
			return err
		}

		var paused int64
		if err := tx.Table("key_values").Where("key = 'enhancement/paused'").Count(&paused).Error; err != nil {
			return err
		}

		if paused > 0 {
			return ErrPaused
		}

		lease := tx.Exec(
			"UPDATE enhancement_claims SET started = true WHERE token = ? AND expires_at > now()",
			a.claim,
		)
		if lease.Error != nil {
			return lease.Error
		}

		if lease.RowsAffected != 1 {
			return ErrLease
		}

		if err := tx.Exec(
			`INSERT INTO enhancement_daily_spending(day) VALUES ((now() AT TIME ZONE 'UTC' )::date) ON CONFLICT
DO NOTHING`,
		).Error; err != nil {
			return err
		}

		res := tx.Exec(
			`UPDATE enhancement_daily_spending SET charged = charged + ? WHERE day = (now() AT TIME ZONE 'UTC'
)::date AND charged + ? <= ?`,
			cost,
			cost,
			a.config.DailyBudget,
		)
		if res.Error != nil {
			return res.Error
		}

		if res.RowsAffected != 1 {
			return ErrBudget
		}

		return tx.Exec(
			`INSERT INTO enhancement_reservations(token, day, cost, expires_at)
 SELECT ?, (now() AT TIME ZONE 'UTC')::date, ?, expires_at + interval '5 seconds'
 FROM enhancement_claims WHERE token = ?`,
			token,
			cost,
			a.claim,
		).Error
	})

	return token, err
}

func (a *accounting) settle(parent context.Context, token string, actual *float64, status int) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()

	return a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := schedulerLock(tx); err != nil {
			return err
		}

		if status == 400 || status == 401 || status == 403 || status == 404 || status == 422 {
			if err := pauseAutomatic(tx); err != nil {
				return err
			}
		}

		if actual == nil {
			// The caller has terminated with uncertain usage. Keep the full daily
			// charge, but retire its token so later duplicate settlement cannot refund it.
			return tx.Exec("DELETE FROM enhancement_reservations WHERE token = ?", token).Error
		}

		return tx.Exec(
			`WITH reservation AS (DELETE FROM enhancement_reservations WHERE token = ? AND expires_at > now()
 RETURNING day,cost)
UPDATE enhancement_daily_spending d SET charged = greatest(0,charged + ? - r.cost) FROM reservation
r WHERE d.day=r.day`,
			token,
			*actual,
		).Error
	})
}

// Only unsettled requests retain tokens. A crash may orphan one after its lease
// disappears; expiry includes the same five-second settlement grace as settle.
func retireExpiredReservations(tx *gorm.DB) error {
	return tx.Exec(`DELETE FROM enhancement_reservations WHERE token IN (
 SELECT token FROM enhancement_reservations WHERE expires_at <= now()
 ORDER BY expires_at,token LIMIT 64)`).Error
}
