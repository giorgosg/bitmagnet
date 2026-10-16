package enhancement

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"gorm.io/gorm"
)

var ErrCapacity = errors.New("enhancement shared capacity unavailable; retry after active claims finish or expire")

// Request admits one explicit hash even while automatic selection is disabled.
// It shares the same lock, active claims, pause and request accountant as automatic work.
func (s *Scheduler) Request(ctx context.Context, hash protocol.ID) (claim Claim, err error) {
	cfg := s.config

	cfg.Automatic = true // Validate the same positive spending requirements, without enabling a worker.
	if err = cfg.Validate(); err != nil {
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

		if active >= int64(cfg.Concurrency) {
			admissionErr = ErrCapacity
			return nil
		}

		if err := LockTorrent(tx, hash); err != nil {
			return err
		}

		var owned int64
		if err := tx.Table("enhancement_claims").Where("info_hash=?", hash).Count(&owned).Error; err != nil {
			return err
		}

		if owned > 0 {
			admissionErr = ErrCapacity
			return nil
		}

		claim = Claim{InfoHash: hash, Token: newToken()}

		return tx.Exec(
			"INSERT INTO enhancement_claims(info_hash,token,expires_at) VALUES (?,?,?)",
			hash,
			claim.Token,
			time.Now().Add(3*cfg.Timeout),
		).Error
	})
	if err != nil {
		return Claim{}, err
	}

	return claim, admissionErr
}

func recoverClaims(tx *gorm.DB) error {
	// A crash after any request consumes this attempt's caps. Recover as limited,
	// preserving accepted output and the pessimistic spending already reserved.
	if err := limitInterruptedClaims(tx, ""); err != nil {
		return err
	}

	if err := tx.Exec(`UPDATE torrents t SET enhancement_ready = NOT c.started OR EXISTS (
 SELECT 1 FROM classification_evaluations e WHERE e.info_hash=t.info_hash
 AND e.stage='ordinary' AND e.completed_at > c.claimed_at), enhancement_checked_at=now()
   FROM enhancement_claims c WHERE c.expires_at<=now() AND c.info_hash=t.info_hash`).Error; err != nil {
		return err
	}

	return tx.Exec("DELETE FROM enhancement_claims WHERE expires_at<=now()").Error
}

// PreviewEligibility describes unchanged-completion suppression without writes or
// inference. Admission still rechecks visibility, authority, freshness and spending.
func PreviewEligibility(e Evidence, states []model.ClassificationEvaluation, cfg Config) string {
	var ordinary, latest model.ClassificationEvaluation

	for _, state := range states {
		if state.Stage == "ordinary" {
			ordinary = state
		}

		if state.Stage == "enhancement" {
			latest = state
		}
	}

	if !ordinary.CompletedAt.Valid {
		return "ordinary_required"
	}

	observation := e
	observation.Classification = json.RawMessage("[]")

	raw, err := json.Marshal(observation)
	if err != nil || fingerprint(raw) != ordinary.InputFingerprint {
		return "ordinary_required"
	}

	raw, err = json.Marshal(e)
	if err != nil {
		return "invalid_evidence"
	}

	input := fingerprint(raw)

	recipe, _, err := Recipe(cfg, ordinary.RecipeFingerprint)
	if err != nil {
		return "invalid_recipe"
	}

	if latest.InputFingerprint == input && latest.RecipeFingerprint == recipe &&
		(latest.Outcome == OutcomeAccepted || latest.Outcome == OutcomeUnresolved || latest.Outcome == OutcomeLimited) {
		return "unchanged_completed"
	}

	if latest.AttemptInput == input && latest.AttemptRecipe == recipe &&
		(latest.AttemptOutcome == OutcomeMalformed || latest.AttemptOutcome == OutcomeProviderFailure) {
		return "exhausted_failure"
	}

	return "eligible"
}
