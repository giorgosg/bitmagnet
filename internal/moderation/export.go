package moderation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"

	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"gorm.io/gorm"
)

// BanFileVersion is the frozen JSON envelope version, independent of the digest format.
const BanFileVersion = 1

// BanFile contains only shareable membership assertions. It is not a private Record.
// Source is an optional operator-chosen label for the whole file, never an actor or evidence.
type BanFile struct {
	Version          int        `json:"version"`
	IdentifierFormat string     `json:"identifierFormat"`
	Source           string     `json:"source,omitempty"`
	Entries          []BanEntry `json:"entries"`
}

// BanEntry records local decision origin; imported assertions are never exported.
type BanEntry struct {
	Digest string `json:"digest"`
	Origin Origin `json:"origin"`
}

type ExportOptions struct{ IncludeAutomatic bool }

// Export writes one read-only repeatable-read snapshot in bounded keyset pages.
// It deliberately selects no private fields and never reads evidence or history.
// An error after writing begins leaves an incomplete file, which must be discarded.
func (s *Service) Export(ctx context.Context, w io.Writer, opts ExportOptions) error {
	err := s.database(ctx).Transaction(func(tx *gorm.DB) error {
		origins := []Origin{Moderator}
		if opts.IncludeAutomatic {
			origins = append(origins, Automatic)
		}

		after := ""
		started := false

		header, err := json.Marshal(
			BanFile{Version: BanFileVersion, IdentifierFormat: IdentifierFormatV1, Entries: []BanEntry{}},
		)
		if err != nil {
			return err
		}

		const batchSize = 100

		for {
			if err := ctx.Err(); err != nil {
				return err
			}

			var entries []BanEntry

			err := tx.Model(&model.ModerationDecision{}).
				Select("identifier AS digest, origin").
				Where("status = ? AND origin IN ? AND identifier > ?", Banned, origins, after).
				Order("identifier ASC").Limit(batchSize).Scan(&entries).Error
			if err != nil {
				return err
			}

			if !started {
				if err := writeExport(w, header[:len(header)-2]); err != nil {
					return err
				}

				started = true
			}

			for _, entry := range entries {
				if _, err := ParseIdentifier(IdentifierFormatV1, entry.Digest); err != nil {
					return err
				}

				if after != "" {
					if err := writeExport(w, []byte(",")); err != nil {
						return err
					}
				}

				data, err := json.Marshal(entry)
				if err != nil {
					return err
				}

				if err = writeExport(w, data); err != nil {
					return err
				}

				after = entry.Digest
			}

			if len(entries) < batchSize {
				return writeExport(w, []byte("]}\n"))
			}
		}
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return fmt.Errorf("export moderation bans: %w", err)
	}

	return nil
}

func writeExport(w io.Writer, data []byte) error {
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}

	return err
}
