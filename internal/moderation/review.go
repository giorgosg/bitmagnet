package moderation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrConflict          = errors.New("moderation record changed; reload before reviewing")
	ErrInvalidIdentifier = errors.New("invalid moderation identifier")
)

const (
	reviewAllowAction  = "allow"
	reviewReopenAction = "reopen"
)

// FlagCatalogue retains server-loaded metadata, including quarantined catalogue rows.
// Reassessment of an existing decision must use Review, rather than a second flag.
func (s *Service) FlagCatalogue(ctx context.Context, hash protocol.ID, actor string) (Record, error) {
	id := IdentifierForInfoHash(hash)

	var result Record

	err := s.database(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockIdentifier(tx, id); err != nil {
			return err
		}

		var existing model.ModerationDecision

		err := tx.Where("identifier = ?", id.String()).First(&existing).Error
		if err == nil {
			return ErrInvalidTransition
		}

		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		var torrent model.Torrent
		if err = tx.Where("info_hash = ?", hash).First(&torrent).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}

			return err
		}

		evidence, err := withCatalogueEvidence(tx, id, Evidence{InfoHash: &hash})
		if err != nil {
			return err
		}

		if err = NewService(tx).Flag(ctx, id, evidence, actor); err != nil {
			return err
		}

		result, err = getRecord(tx, id)

		return err
	})

	return result, err
}

type ReviewResult struct {
	Record               Record
	ClassificationQueued bool
}

// Review compares the observed timestamp while holding the ingestion/decision lock.
// makeJob uses the normal processor payload without coupling the ledger to the classifier.
// State, history, and missing classification work commit or roll back together.
func (s *Service) Review(
	ctx context.Context,
	id Identifier,
	action string,
	expected time.Time,
	actor string,
	suppliedHash *protocol.ID,
	makeJob func(protocol.ID) (model.QueueJob, error),
) (ReviewResult, error) {
	var result ReviewResult
	if suppliedHash != nil && IdentifierForInfoHash(*suppliedHash) != id {
		return result, ErrInvalidEvidence
	}

	err := s.database(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockIdentifier(tx, id); err != nil {
			return err
		}

		var stored model.ModerationDecision
		if err := tx.Clauses(clause.Locking{Strength: lockForUpdate}).
			Where("identifier = ?", id.String()).
			First(&stored).
			Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}

			return err
		}

		if !stored.UpdatedAt.Equal(expected) {
			return ErrConflict
		}

		service := NewService(tx)

		var err error

		switch action {
		case reviewAllowAction:
			if stored.Status == string(Allowed) {
				return ErrInvalidTransition
			}

			err = service.Allow(ctx, id, actor)
		case "ban":
			if stored.Status == string(Banned) && stored.Origin == string(Moderator) {
				return ErrInvalidTransition
			}

			err = service.Ban(ctx, id, actor)
		case reviewReopenAction:
			if stored.Status == string(Pending) {
				return ErrInvalidTransition
			}

			err = service.Reopen(ctx, id, actor)
		case "purge":
			err = service.PurgeEvidence(ctx, id)
			if err == nil {
				err = tx.Model(&model.ModerationDecision{}).
					Where("identifier = ?", id.String()).
					Update("updated_at", time.Now().UTC()).
					Error
			}
		default:
			return ErrInvalidTransition
		}

		if err != nil {
			return err
		}

		result.Record, err = getRecord(tx, id)
		if err != nil {
			return err
		}

		if action != reviewAllowAction {
			return nil
		}

		hashPointer := suppliedHash
		if hashPointer == nil && result.Record.Evidence != nil {
			hashPointer = result.Record.Evidence.InfoHash
		}

		if hashPointer == nil {
			return nil
		}

		hash := *hashPointer

		var missing bool
		if err = tx.Raw(`SELECT EXISTS (SELECT 1 FROM torrents WHERE info_hash = ?)
   AND NOT EXISTS (SELECT 1 FROM torrent_contents WHERE info_hash = ?)`, hash, hash).Scan(&missing).Error; err != nil {
			return err
		}

		if !missing {
			return nil
		}

		if makeJob == nil {
			return fmt.Errorf("classification job constructor required")
		}

		job, err := makeJob(hash)
		if err != nil {
			return err
		}

		if err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&job).Error; err != nil {
			return err
		}

		result.ClassificationQueued = true

		return nil
	})

	return result, err
}
