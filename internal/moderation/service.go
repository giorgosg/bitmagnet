package moderation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/database/cache"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Status string

const (
	Pending Status = "pending"
	Allowed Status = "allowed"
	Banned  Status = "banned"
)

type Origin string

const (
	Moderator Origin = "moderator"
	Automatic Origin = "automatic"
	Imported  Origin = "import"
)

var (
	ErrInvalidEvidence   = errors.New("moderation evidence infohash does not match identifier")
	ErrNotFound          = errors.New("moderation record not found")
	ErrInvalidTransition = errors.New("invalid moderation transition")
	ErrInvalidPage       = errors.New("invalid moderation page")
)

type Evidence = model.ModerationEvidenceData

// Record is a private moderation view; it must never hydrate ordinary catalogue reads.
type Record struct {
	Identifier     Identifier
	Status         Status
	Origin         Origin
	ImportedOrigin Origin
	Source         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Evidence       *Evidence
}

// Page uses keyset pagination. Limit zero defaults to 50; at most 100 items are returned.
type Page struct {
	Limit int
	After string
}
type HistoryEntry struct {
	ID             int64
	Action         string
	Status         Status
	Origin         Origin
	ImportedOrigin Origin
	Source         string
	Actor          string
	CreatedAt      time.Time
}
type HistoryPage struct {
	Items []HistoryEntry
	Next  string
}

const lockForUpdate = "UPDATE"

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

func (s *Service) database(ctx context.Context) *gorm.DB {
	return s.db.WithContext(context.WithValue(ctx, cache.ModeKey, cache.ModeNoCache))
}

func (s *Service) Flag(ctx context.Context, id Identifier, evidence Evidence, actor string) error {
	return s.change(
		ctx,
		id,
		decisionChange{status: Pending, origin: Moderator, action: "flag", actor: actor, evidence: &evidence},
	)
}

type decisionChange struct {
	status         Status
	origin         Origin
	importedOrigin Origin
	source         string
	action         string
	actor          string
	evidence       *Evidence
	overwrite      bool
}

// change locks the exact identifier before changing state, history or evidence.
// A conflict insert waits on concurrent creation before the row lock is acquired.
func (s *Service) change(ctx context.Context, id Identifier, c decisionChange) error {
	if c.evidence != nil && c.evidence.InfoHash != nil && IdentifierForInfoHash(*c.evidence.InfoHash) != id {
		return ErrInvalidEvidence
	}

	err := s.database(ctx).Transaction(func(tx *gorm.DB) error {
		record := model.ModerationDecision{
			Identifier:     id.String(),
			Status:         string(c.status),
			Origin:         string(c.origin),
			ImportedOrigin: string(c.importedOrigin),
			Source:         c.source,
		}
		created := false

		if c.action != "reopen" {
			inserted := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&record)
			if inserted.Error != nil {
				return inserted.Error
			}

			created = inserted.RowsAffected == 1
		}

		if err := tx.Clauses(clause.Locking{Strength: lockForUpdate}).
			Where("identifier = ?", id.String()).
			First(&record).
			Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}

			return err
		}

		if !created {
			if !c.overwrite || record.Status == string(c.status) && record.Origin == string(c.origin) {
				return nil
			}

			if c.origin != Moderator {
				if record.Status == string(Allowed) || record.Status == string(Banned) {
					return nil
				}
			}
		}

		if !created {
			if err := tx.Model(&record).
				Updates(map[string]any{
					"status":          string(c.status),
					"origin":          string(c.origin),
					"imported_origin": string(c.importedOrigin),
					"source":          c.source,
					"updated_at":      time.Now().UTC(),
				}).
				Error; err != nil {
				return err
			}
		}

		if c.evidence != nil {
			if _, err := retainEvidence(tx, id, *c.evidence); err != nil {
				return err
			}
		}

		return tx.Create(
			&model.ModerationHistory{
				Identifier:     id.String(),
				Action:         c.action,
				Status:         string(c.status),
				Origin:         string(c.origin),
				ImportedOrigin: string(c.importedOrigin),
				Source:         c.source,
				Actor:          c.actor,
			},
		).Error
	})
	if err != nil {
		return fmt.Errorf("moderation %s: %w", c.action, err)
	}

	return nil
}

// Get reads the current decision and private evidence from one consistent snapshot.
func (s *Service) Get(ctx context.Context, id Identifier) (record Record, err error) {
	err = s.database(ctx).Transaction(func(tx *gorm.DB) error {
		record, err = getRecord(tx, id)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})

	return record, err
}

func getRecord(db *gorm.DB, id Identifier) (Record, error) {
	var stored model.ModerationDecision

	err := db.Where("identifier = ?", id.String()).First(&stored).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Record{}, ErrNotFound
	}

	if err != nil {
		return Record{}, fmt.Errorf("get moderation decision: %w", err)
	}

	record := Record{
		Identifier:     id,
		Status:         Status(stored.Status),
		Origin:         Origin(stored.Origin),
		ImportedOrigin: Origin(stored.ImportedOrigin),
		Source:         stored.Source,
		CreatedAt:      stored.CreatedAt,
		UpdatedAt:      stored.UpdatedAt,
	}

	var private model.ModerationEvidence

	err = db.Where("identifier = ?", id.String()).First(&private).Error
	if err == nil {
		record.Evidence = &private.Data
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Record{}, fmt.Errorf("get moderation evidence: %w", err)
	}

	return record, nil
}

func pageLimit(page Page) (int, error) {
	if page.Limit < 0 || page.Limit > 100 {
		return 0, ErrInvalidPage
	}

	if page.Limit == 0 {
		return 50, nil
	}

	return page.Limit, nil
}

func (s *Service) History(ctx context.Context, id Identifier, page Page) (HistoryPage, error) {
	limit, err := pageLimit(page)
	if err != nil {
		return HistoryPage{}, err
	}

	query := s.database(ctx).Where("identifier = ?", id.String())

	if page.After != "" {
		after, parseErr := strconv.ParseInt(page.After, 10, 64)
		if parseErr != nil || after < 1 {
			return HistoryPage{}, ErrInvalidPage
		}

		query = query.Where("id > ?", after)
	}

	var stored []model.ModerationHistory
	if err := query.Order("id ASC").Limit(limit + 1).Find(&stored).Error; err != nil {
		return HistoryPage{}, fmt.Errorf("get moderation history: %w", err)
	}

	result := HistoryPage{}

	if len(stored) > limit {
		stored = stored[:limit]
		result.Next = strconv.FormatInt(stored[limit-1].ID, 10)
	}

	result.Items = make([]HistoryEntry, 0, len(stored))
	for _, item := range stored {
		result.Items = append(
			result.Items,
			HistoryEntry{
				ID:             item.ID,
				Action:         item.Action,
				Status:         Status(item.Status),
				Origin:         Origin(item.Origin),
				ImportedOrigin: Origin(item.ImportedOrigin),
				Source:         item.Source,
				Actor:          item.Actor,
				CreatedAt:      item.CreatedAt,
			},
		)
	}

	return result, nil
}

func (s *Service) Allow(ctx context.Context, id Identifier, actor string) error {
	return s.change(
		ctx,
		id,
		decisionChange{status: Allowed, origin: Moderator, action: "allow", actor: actor, overwrite: true},
	)
}

// ApplyAutomatic asserts a keyword match; local allow and existing bans are immutable to automation.
// Repeated decisions are no-ops; callers can separately RetainEvidence when metadata improves.
func (s *Service) ApplyAutomatic(ctx context.Context, id Identifier, status Status, evidence Evidence) error {
	if status != Pending && status != Banned {
		return ErrInvalidTransition
	}

	return s.change(
		ctx,
		id,
		decisionChange{
			status:    status,
			origin:    Automatic,
			action:    "automatic",
			evidence:  &evidence,
			overwrite: true,
		},
	)
}

// ApplyImport enforces one trusted assertion, without relabelling local decisions.
func (s *Service) ApplyImport(ctx context.Context, id Identifier, original Origin, source string) error {
	if original != Moderator && original != Automatic {
		return ErrInvalidTransition
	}

	return s.change(
		ctx,
		id,
		decisionChange{
			status:         Banned,
			origin:         Imported,
			importedOrigin: original,
			source:         source,
			action:         "import",
			overwrite:      true,
		},
	)
}

func (s *Service) Ban(ctx context.Context, id Identifier, actor string) error {
	return s.change(
		ctx,
		id,
		decisionChange{status: Banned, origin: Moderator, action: "ban", actor: actor, overwrite: true},
	)
}

// Reopen explicitly withdraws a final local decision and returns it to pending.
func (s *Service) Reopen(ctx context.Context, id Identifier, actor string) error {
	return s.change(
		ctx,
		id,
		decisionChange{status: Pending, origin: Moderator, action: "reopen", actor: actor, overwrite: true},
	)
}

// PurgeEvidence clears retained review metadata and prior private history, keeping the decision.
func (s *Service) PurgeEvidence(ctx context.Context, id Identifier) error {
	err := s.database(ctx).Transaction(func(tx *gorm.DB) error {
		var record model.ModerationDecision
		if err := tx.Clauses(clause.Locking{Strength: lockForUpdate}).
			Where("identifier = ?", id.String()).
			First(&record).
			Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}

			return err
		}

		if err := tx.Where("identifier = ?", id.String()).
			Delete(&model.ModerationEvidence{}).
			Error; err != nil {
			return err
		}

		if err := tx.Where("identifier = ?", id.String()).Delete(&model.ModerationHistory{}).Error; err != nil {
			return err
		}

		return tx.Create(
			&model.ModerationHistory{
				Identifier: id.String(),
				Action:     "purge",
				Status:     record.Status,
				Origin:     record.Origin,
			},
		).Error
	})
	if err != nil {
		return fmt.Errorf("purge moderation evidence: %w", err)
	}

	return nil
}

type SearchRequest struct {
	Status Status
	Query  string
	Page   Page
}
type RecordSummary struct {
	Identifier     Identifier
	Status         Status
	Origin         Origin
	ImportedOrigin Origin
	Source         string
	Name           string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
type SearchPage struct {
	Items []RecordSummary
	Next  string
}

// Search is a bounded private review list; Query searches literal identifier/name/full-path text.
// It returns summaries only, leaving complete private evidence to Get.
func (s *Service) Search(ctx context.Context, req SearchRequest) (SearchPage, error) {
	limit, err := pageLimit(req.Page)
	if err != nil {
		return SearchPage{}, err
	}

	query := s.database(ctx).Table("moderation_decisions AS d").
		Select("d.*, COALESCE(e.data->>'name', '') AS name").
		Joins("LEFT JOIN moderation_evidence AS e ON e.identifier = d.identifier")

	if req.Status != "" {
		if req.Status != Pending && req.Status != Allowed && req.Status != Banned {
			return SearchPage{}, ErrInvalidTransition
		}

		query = query.Where("d.status = ?", req.Status)
	}

	if req.Page.After != "" {
		after, err := ParseIdentifier(IdentifierFormatV1, req.Page.After)
		if err != nil {
			return SearchPage{}, ErrInvalidPage
		}

		query = query.Where("d.identifier > ?", after.String())
	}

	if req.Query != "" {
		escaped := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(req.Query)
		pattern := "%" + escaped + "%"
		query = query.Where(`d.identifier ILIKE ? OR e.data->>'name' ILIKE ? OR EXISTS (
   SELECT 1 FROM jsonb_array_elements_text(COALESCE(NULLIF(e.data->'files', 'null'::jsonb), '[]'::jsonb)) AS file(path)
   WHERE file.path ILIKE ?)`, pattern, pattern, pattern)
	}

	var stored []struct {
		model.ModerationDecision
		Name string
	}
	if err := query.Order("d.identifier ASC").Limit(limit + 1).Find(&stored).Error; err != nil {
		return SearchPage{}, fmt.Errorf("search moderation records: %w", err)
	}

	result := SearchPage{}

	if len(stored) > limit {
		stored = stored[:limit]
		result.Next = stored[limit-1].Identifier
	}

	result.Items = make([]RecordSummary, 0, len(stored))
	for _, item := range stored {
		id, err := ParseIdentifier(IdentifierFormatV1, item.Identifier)
		if err != nil {
			return SearchPage{}, err
		}

		result.Items = append(
			result.Items,
			RecordSummary{
				Identifier:     id,
				Status:         Status(item.Status),
				Origin:         Origin(item.Origin),
				ImportedOrigin: Origin(item.ImportedOrigin),
				Source:         item.Source,
				Name:           item.Name,
				CreatedAt:      item.CreatedAt,
				UpdatedAt:      item.UpdatedAt,
			},
		)
	}

	return result, nil
}

// RetainEvidence enriches pending/banned metadata without changing the enforcement decision.
// Missing fields preserve retained metadata. Local allows are exempt from automatic enrichment.
func (s *Service) RetainEvidence(ctx context.Context, id Identifier, evidence Evidence) error {
	if evidence.InfoHash != nil && IdentifierForInfoHash(*evidence.InfoHash) != id {
		return ErrInvalidEvidence
	}

	err := s.database(ctx).Transaction(func(tx *gorm.DB) error {
		var record model.ModerationDecision
		if err := tx.Clauses(clause.Locking{Strength: lockForUpdate}).
			Where("identifier = ?", id.String()).
			First(&record).
			Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}

			return err
		}

		if record.Status == string(Allowed) {
			return nil
		}

		changed, err := retainEvidence(tx, id, evidence)
		if err != nil || !changed {
			return err
		}

		return tx.Create(
			&model.ModerationHistory{
				Identifier:     id.String(),
				Action:         "evidence",
				Status:         record.Status,
				Origin:         record.Origin,
				ImportedOrigin: record.ImportedOrigin,
				Source:         record.Source,
			},
		).Error
	})
	if err != nil {
		return fmt.Errorf("retain moderation evidence: %w", err)
	}

	return nil
}

func retainEvidence(tx *gorm.DB, id Identifier, evidence Evidence) (bool, error) {
	var prior model.ModerationEvidence

	err := tx.Where("identifier = ?", id.String()).First(&prior).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}

	if err == nil {
		if evidence.InfoHash == nil {
			evidence.InfoHash = prior.Data.InfoHash
		}

		if evidence.Name == "" {
			evidence.Name = prior.Data.Name
		}

		if evidence.Files == nil {
			evidence.Files = prior.Data.Files
		}

		if evidence.Rule == "" {
			evidence.Rule = prior.Data.Rule
		}

		if evidence.Matches == nil {
			evidence.Matches = prior.Data.Matches
		}

		if reflect.DeepEqual(prior.Data, evidence) {
			return false, nil
		}
	}

	private := model.ModerationEvidence{Identifier: id.String(), Data: evidence}
	err = tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&private).Error

	return err == nil, err
}
