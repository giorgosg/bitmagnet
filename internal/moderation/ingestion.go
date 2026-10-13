package moderation

import (
	"context"
	"encoding/binary"
	"errors"
	"sort"

	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"gorm.io/gorm"
)

// lockIdentifier also serializes identifiers that have no decision row yet.
// Hash collisions only serialize unrelated work; exact decisions still use the full digest.
func lockIdentifier(tx *gorm.DB, id Identifier) error {
	return tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(binary.BigEndian.Uint64(id[:8]))).Error
}

// Ingest holds decision locks until catalogue writes commit. Bans retain observed
// private metadata but cannot insert catalogue rows. Pending metadata remains available
// for a later allow, with an independent evidence copy including uncapped file paths.
func (s *Service) Ingest(ctx context.Context, evidence []Evidence,
	write func(*gorm.DB, map[protocol.ID]bool) error,
) error {
	ids := make(map[Identifier]protocol.ID, len(evidence))

	observed := make(map[Identifier][]Evidence, len(evidence))
	for _, item := range evidence {
		if item.InfoHash == nil {
			return ErrInvalidEvidence
		}

		id := IdentifierForInfoHash(*item.InfoHash)
		ids[id] = *item.InfoHash
		observed[id] = append(observed[id], item)
	}

	ordered := make([]Identifier, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}

	sort.Slice(ordered, func(i, j int) bool { return ordered[i].String() < ordered[j].String() })

	return s.database(ctx).Transaction(func(tx *gorm.DB) error {
		permitted := make(map[protocol.ID]bool, len(ids))

		for _, id := range ordered {
			if err := lockIdentifier(tx, id); err != nil {
				return err
			}

			var record model.ModerationDecision

			err := tx.Where("identifier = ?", id.String()).First(&record).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}

			permitted[ids[id]] = record.Status != string(Banned)
			if record.Status != string(Pending) && record.Status != string(Banned) {
				continue
			}

			for _, item := range observed[id] {
				item, err = withCatalogueEvidence(tx, id, item)
				if err != nil {
					return err
				}

				changed, err := retainEvidence(tx, id, item)
				if err != nil {
					return err
				}

				if changed {
					if err := tx.Create(&model.ModerationHistory{
						Identifier:     id.String(),
						Action:         "evidence",
						Status:         record.Status,
						Origin:         record.Origin,
						ImportedOrigin: record.ImportedOrigin,
						Source:         record.Source,
					}).Error; err != nil {
						return err
					}
				}
			}
		}

		return write(tx, permitted)
	})
}

// Discoverable filters only exact bans. Pending identifiers can acquire missing evidence.
func (s *Service) Discoverable(ctx context.Context, hashes []protocol.ID) ([]protocol.ID, error) {
	ids := make([]string, 0, len(hashes))
	for _, hash := range hashes {
		ids = append(ids, IdentifierForInfoHash(hash).String())
	}

	var banned []model.ModerationDecision
	if len(ids) > 0 {
		if err := s.database(ctx).
			Where("identifier IN ? AND status = ?", ids, Banned).
			Find(&banned).
			Error; err != nil {
			return nil, err
		}
	}

	excluded := make(map[string]bool, len(banned))
	for _, record := range banned {
		excluded[record.Identifier] = true
	}

	result := make([]protocol.ID, 0, len(hashes))
	for _, hash := range hashes {
		if !excluded[IdentifierForInfoHash(hash).String()] {
			result = append(result, hash)
		}
	}

	return result, nil
}

// withCatalogueEvidence fills only metadata absent from both the observation and
// private evidence. Lookup uses the known raw infohash, never a catalogue digest scan.
// Previously retained uncapped paths must not be replaced by capped catalogue rows.
func withCatalogueEvidence(tx *gorm.DB, id Identifier, observed Evidence) (Evidence, error) {
	if observed.Name != "" && observed.Files != nil {
		return observed, nil
	}

	var private model.ModerationEvidence

	err := tx.Where("identifier = ?", id.String()).First(&private).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return observed, err
	}

	needName := observed.Name == "" && private.Data.Name == ""

	needFiles := observed.Files == nil && private.Data.Files == nil
	if !needName && !needFiles {
		return observed, nil
	}

	var torrent model.Torrent

	err = tx.Where("info_hash = ?", *observed.InfoHash).First(&torrent).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return observed, nil
	}

	if err != nil {
		return observed, err
	}

	if needName {
		observed.Name = torrent.Name
	}

	if needFiles && torrent.FilesStatus != model.FilesStatusNoInfo {
		var files []model.TorrentFile
		if err := tx.Where("info_hash = ?", *observed.InfoHash).
			Order("index ASC, path ASC").
			Find(&files).
			Error; err != nil {
			return observed, err
		}

		observed.Files = make([]string, 0, len(files))
		for _, file := range files {
			observed.Files = append(observed.Files, file.Path)
		}
	}

	return observed, nil
}
