package enhancement

import (
	"context"
	"errors"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/tmdb"
	"gorm.io/gorm"
)

func catalogueFailure(err error) string {
	switch {
	case errors.Is(err, tmdb.ErrUnauthorized):
		return "authentication"
	case errors.Is(err, tmdb.ErrConfiguration):
		return "configuration"
	default:
		return ""
	}
}

func pauseAutomatic(tx *gorm.DB) error {
	return tx.Exec(`INSERT INTO key_values(key,value,created_at,updated_at)
 VALUES ('enhancement/paused','provider authentication or configuration failure',now(),now())
 ON CONFLICT (key) DO NOTHING`).Error
}

func (a *accounting) pause(parent context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()

	return a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := schedulerLock(tx); err != nil {
			return err
		}

		return pauseAutomatic(tx)
	})
}
