package enhancement

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/database/catalogue"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"gorm.io/gorm"
)

// Scheduler owns automatic admission. It does not subscribe to queue notifications.
// Only active claims get separate rows; the catalogue is never copied to a job queue.
type Scheduler struct {
	db       *gorm.DB
	config   Config
	app      *Application
	classify func(context.Context, protocol.ID) error
}
type Claim struct {
	InfoHash protocol.ID
	Token    string
}

func NewScheduler(
	db *gorm.DB,
	c Config,
	app *Application,
	classify func(context.Context, protocol.ID) error,
) *Scheduler {
	return &Scheduler{db: db, config: c, app: app, classify: classify}
}

func schedulerLock(tx *gorm.DB) error {
	return tx.Exec("SELECT pg_advisory_xact_lock(531209741)").Error
}

// Claim serializes the capacity split and shared concurrency across processes. The
// cursor is one bit: every admitted slot alternates groups, borrowing if empty.
func (s *Scheduler) Claim(ctx context.Context) (claim Claim, err error) {
	if !s.config.Automatic {
		return claim, nil
	}

	if err = s.config.Validate(); err != nil {
		return claim, err
	}

	var admissionErr error

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := schedulerLock(tx); err != nil {
			return err
		}

		if err := retireExpiredReservations(tx); err != nil {
			return err
		}

		var paused int64
		if err := tx.Table("key_values").Where("key = 'enhancement/paused'").Count(&paused).Error; err != nil {
			return err
		}

		if paused > 0 {
			// Commit bounded reservation maintenance without admitting work.
			admissionErr = ErrPaused
			return nil
		}

		if err := recoverClaims(tx); err != nil {
			return err
		}

		var active int64
		if err := tx.Table("enhancement_claims").Count(&active).Error; err != nil {
			return err
		}

		if active >= int64(s.config.Concurrency) {
			return nil
		}
		// Refresh the seven-day boundary and gradual reconsideration in bounded pages.
		if err := tx.Exec(`UPDATE torrents SET enhancement_recent=false WHERE info_hash IN
(SELECT info_hash FROM torrents WHERE enhancement_recent AND created_at < now()-interval '7 days'
ORDER BY created_at,info_hash LIMIT 64)`).Error; err != nil {
			return err
		}

		var boundaryPending bool
		if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM torrents
            WHERE enhancement_recent AND created_at < now()-interval '7 days')
        `).Scan(&boundaryPending).Error; err != nil {
			return err
		}
		// Never rank a partially refreshed older group. Maintenance stays bounded,
		// and a large crossing pauses admission until subsequent polls catch up.
		if boundaryPending {
			return nil
		}

		if err := tx.Exec(`WITH candidates AS MATERIALIZED
   (SELECT info_hash FROM torrents WHERE NOT enhancement_ready AND enhancement_checked_at < ?
    ORDER BY enhancement_checked_at,info_hash LIMIT 64)
UPDATE torrents t SET enhancement_ready=EXISTS(SELECT 1 FROM torrent_contents tc WHERE
tc.info_hash=t.info_hash),
    enhancement_checked_at=now() WHERE t.info_hash IN (SELECT info_hash FROM candidates)
        `, time.Now().Add(-s.config.ReconsiderInterval)).Error; err != nil {
			return err
		}

		var value string
		if err := tx.Raw("SELECT value FROM key_values WHERE key='enhancement/next-group'").
			Scan(&value).
			Error; err != nil {
			return err
		}

		recent := value != "older"
		for _, group := range []bool{recent, !recent} {
			// Ranking is maintained by a source trigger, including source deletion. The
			// partial index excludes completed checks; moderation is an indexed anti-join.
			var hashes []protocol.ID
			if err := tx.Raw(selectionQuery(group)).Scan(&hashes).Error; err != nil {
				return err
			}

			if len(hashes) == 0 {
				continue
			}

			claim = Claim{InfoHash: hashes[0], Token: newToken()}
			if err := tx.Exec(
				"INSERT INTO enhancement_claims(info_hash,token,expires_at) VALUES (?,?,?)",
				claim.InfoHash,
				claim.Token,
				time.Now().Add(3*s.config.Timeout),
			).Error; err != nil {
				return err
			}

			if err := tx.Exec(
				"UPDATE torrents SET enhancement_ready=false,enhancement_checked_at=now() WHERE info_hash=?",
				claim.InfoHash,
			).Error; err != nil {
				return err
			}

			next := "recent"
			if group {
				next = "older"
			}

			return tx.Exec(
				`INSERT INTO key_values(key,value,created_at,updated_at) VALUES
('enhancement/next-group',?,now(),now()) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
				next,
			).Error
		}

		return nil
	})
	if err != nil {
		return claim, err
	}

	return claim, admissionErr
}

var selectionSQL = `WITH ranked AS MATERIALIZED (
 SELECT t.info_hash,t.enhancement_seeders,t.created_at FROM torrents t
 WHERE t.enhancement_ready AND t.enhancement_recent = %t
 AND t.created_at %s now()-interval '7 days'
 AND NOT EXISTS (SELECT 1 FROM enhancement_claims c WHERE c.info_hash=t.info_hash)
 ORDER BY coalesce(t.enhancement_seeders,-1) DESC,t.created_at,t.info_hash LIMIT 64
), visibility AS MATERIALIZED (
 SELECT t.*, ` + catalogue.VisibleInfoHash("t.info_hash") + ` AS visible FROM ranked t
), hidden AS (
 UPDATE torrents SET enhancement_ready=false,enhancement_checked_at=now()
 WHERE info_hash IN (SELECT info_hash FROM visibility WHERE NOT visible)
)
 SELECT info_hash FROM visibility WHERE visible
 ORDER BY coalesce(enhancement_seeders,-1) DESC,created_at,info_hash LIMIT 1`

func selectionQuery(recent bool) string {
	boundary := "<"
	if recent {
		boundary = ">="
	}

	return fmt.Sprintf(selectionSQL, recent, boundary)
}

// Process reclassifies selected legacy/stale evidence before admission, without
// fabricating an ordinary completion record from a pre-upgrade search row.
func (s *Scheduler) Process(ctx context.Context, c Claim) (Evaluation, error) {
	return s.ProcessRequested(ctx, c, false, false)
}

// ProcessRequested preserves admission, accounting and application safeguards for explicit requests.
func (s *Scheduler) ProcessRequested(ctx context.Context, c Claim, force, rematch bool) (Evaluation, error) {
	var expiry time.Time
	if err := s.db.WithContext(ctx).Raw(`SELECT expires_at FROM enhancement_claims
     WHERE token=? AND info_hash=? AND expires_at>now()`, c.Token, c.InfoHash).Row().Scan(&expiry); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Evaluation{}, ErrLease
		}

		return Evaluation{}, err
	}

	ctx, cancel := context.WithDeadline(ctx, expiry)
	defer cancel()

	ctx = automaticContext(ctx, &accounting{db: s.db, config: s.config, claim: c.Token})

	result, err := s.app.Enhance(ctx, c.InfoHash, force, rematch)
	if errors.Is(err, ErrNotClassified) && s.classify != nil {
		if err = s.classify(ctx, c.InfoHash); err == nil {
			result, err = s.app.Enhance(ctx, c.InfoHash, force, rematch)
		}
	}

	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()

	finishErr := s.db.WithContext(cleanup).Transaction(func(tx *gorm.DB) error {
		if err := schedulerLock(tx); err != nil {
			return err
		}

		if err != nil {
			if recoverErr := limitInterruptedClaims(tx, c.Token); recoverErr != nil {
				return recoverErr
			}
		}
		// Only the owner can release the lease. A late worker cannot release a successor.
		var admitted time.Time
		if releaseErr := tx.Raw(`DELETE FROM enhancement_claims WHERE token=? AND expires_at>now()
 RETURNING claimed_at`, c.Token).
			Row().
			Scan(&admitted); releaseErr != nil {
			if errors.Is(releaseErr, sql.ErrNoRows) {
				return ErrLease
			}

			return releaseErr
		}

		ready := result.Outcome == OutcomeDeferred ||
			(err != nil && !errors.Is(err, ErrEvaluated) && !errors.Is(err, ErrHidden))

		return tx.Exec(
			`UPDATE torrents t SET enhancement_ready=? OR EXISTS (
 SELECT 1 FROM classification_evaluations e WHERE e.info_hash=t.info_hash
 AND e.stage='ordinary' AND e.completed_at > ?), enhancement_checked_at=now() WHERE info_hash=?`,
			ready, admitted, c.InfoHash,
		).Error
	})

	return result, errors.Join(err, finishErr)
}

// Resume is explicit: restarting a process never clears an authentication pause.
func (s *Scheduler) Resume(ctx context.Context) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := schedulerLock(tx); err != nil {
			return err
		}

		if err := tx.Exec("DELETE FROM key_values WHERE key='enhancement/paused'").Error; err != nil {
			return err
		}

		return tx.Exec(`WITH failures AS (UPDATE classification_evaluations SET attempt_recipe=''
    WHERE stage='enhancement' AND attempt_outcome IN ('provider_failure','malformed') RETURNING info_hash)
    UPDATE torrents t SET enhancement_ready=true FROM failures f WHERE f.info_hash=t.info_hash`).Error
	})
}

// Completed accepted state survives interrupted attempts. Empty token recovers
// expired claims; failed persistence additionally limits the current paid claim.
func limitInterruptedClaims(tx *gorm.DB, token string) error {
	return tx.Exec(`UPDATE classification_evaluations e SET input_fingerprint=c.input_fingerprint,
 recipe_fingerprint=c.recipe_fingerprint,outcome='limited',attempt_outcome='limited',completed_at=now()
 FROM enhancement_claims c WHERE (c.expires_at<=now() OR c.token=?) AND c.started AND c.generation=e.generation
 AND c.info_hash=e.info_hash AND e.stage='enhancement' AND e.attempt_outcome='running'`, token).Error
}
