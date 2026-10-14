package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"unicode/utf8"

	"gorm.io/gorm"
)

const catalogueIdentifierSQL = `encode(sha256(
 '\x6d61676e65733a6d6f6465726174696f6e3a76313a627469683a'::bytea || info_hash), 'hex')`

// ImportResult counts accepted distinct identifiers, including preserved bans and local allows.
type ImportResult struct {
	Entries int `json:"entries"`
}

// Import applies a whole operator-selected file in one transaction.
func (s *Service) Import(ctx context.Context, reader io.Reader) (ImportResult, error) {
	file, err := decodeBanFile(reader)
	if err != nil {
		return ImportResult{}, err
	}

	ids := make(map[Identifier]Origin, len(file.Entries))
	for _, entry := range file.Entries {
		id, err := ParseIdentifier(file.IdentifierFormat, entry.Digest)
		if err != nil {
			return ImportResult{}, err
		}

		if previous, exists := ids[id]; exists && previous != entry.Origin {
			return ImportResult{}, ErrInvalidBanFile
		}

		ids[id] = entry.Origin
	}

	ordered := make([]Identifier, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}

	sort.Slice(ordered, func(i, j int) bool { return ordered[i].String() < ordered[j].String() })

	err = s.database(ctx).Transaction(func(tx *gorm.DB) error {
		for _, id := range ordered {
			if err := NewService(tx).ApplyImport(ctx, id, ids[id], file.Source); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return ImportResult{}, err
	}

	return ImportResult{Entries: len(ids)}, nil
}

// Files are deliberately bounded before opening the write transaction.
const (
	MaxBanFileBytes   = 8 * 1024 * 1024
	MaxBanFileEntries = 10000
)

// MaxBanFileSourceBytes bounds provenance copied into every decision and history row.
const MaxBanFileSourceBytes = 256

var (
	ErrInvalidBanFile  = errors.New("invalid or unsupported moderation ban file")
	ErrBanFileTooLarge = errors.New("moderation ban file exceeds import limits")
)

func decodeBanFile(reader io.Reader) (BanFile, error) {
	data, err := io.ReadAll(io.LimitReader(reader, MaxBanFileBytes+1))
	if err != nil {
		return BanFile{}, fmt.Errorf("%w: %w", ErrInvalidBanFile, err)
	}

	if len(data) > MaxBanFileBytes {
		return BanFile{}, ErrBanFileTooLarge
	}

	if !utf8.Valid(data) {
		return BanFile{}, ErrInvalidBanFile
	}

	fields, err := banFields(data, []string{"version", "identifierFormat", "entries"}, "source")
	if err != nil {
		return BanFile{}, err
	}

	var file BanFile
	if err = json.Unmarshal(fields["version"], &file.Version); err != nil || file.Version != BanFileVersion {
		return file, ErrInvalidBanFile
	}

	if err = json.Unmarshal(
		fields["identifierFormat"],
		&file.IdentifierFormat,
	); err != nil ||
		file.IdentifierFormat != IdentifierFormatV1 {
		return file, ErrInvalidBanFile
	}

	if source, exists := fields["source"]; exists {
		if err = json.Unmarshal(source, &file.Source); err != nil {
			return file, ErrInvalidBanFile
		}

		if len(file.Source) > MaxBanFileSourceBytes {
			return file, ErrBanFileTooLarge
		}
	}

	var entries []json.RawMessage
	if err = json.Unmarshal(fields["entries"], &entries); err != nil {
		return file, ErrInvalidBanFile
	}

	if len(entries) > MaxBanFileEntries {
		return file, ErrBanFileTooLarge
	}

	file.Entries = make([]BanEntry, 0, len(entries))
	for _, raw := range entries {
		fields, err := banFields(raw, []string{"digest", "origin"}, "")
		if err != nil {
			return file, err
		}

		var entry BanEntry
		if err = json.Unmarshal(fields["digest"], &entry.Digest); err != nil {
			return file, ErrInvalidBanFile
		}

		if _, err = ParseIdentifier(file.IdentifierFormat, entry.Digest); err != nil {
			return file, ErrInvalidBanFile
		}

		if err = json.Unmarshal(
			fields["origin"],
			&entry.Origin,
		); err != nil ||
			(entry.Origin != Moderator && entry.Origin != Automatic) {
			return file, ErrInvalidBanFile
		}

		file.Entries = append(file.Entries, entry)
	}

	return file, nil
}

// Reject ambiguous duplicate properties, case variants, nulls and private fields.
// encoding/json's ordinary struct decoder alone accepts several of these.
func banFields(data []byte, required []string, optional string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))

	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, ErrInvalidBanFile
	}

	fields := make(map[string]json.RawMessage, len(required)+1)

	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, ErrInvalidBanFile
		}

		key, ok := keyToken.(string)

		permitted := optional != "" && key == optional
		for _, field := range required {
			permitted = permitted || key == field
		}

		if !ok || !permitted || fields[key] != nil {
			return nil, ErrInvalidBanFile
		}

		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil || bytes.Equal(value, []byte("null")) {
			return nil, ErrInvalidBanFile
		}

		fields[key] = value
	}

	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, ErrInvalidBanFile
	}

	if _, err = decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidBanFile
	}

	for _, field := range required {
		if fields[field] == nil {
			return nil, ErrInvalidBanFile
		}
	}

	return fields, nil
}
