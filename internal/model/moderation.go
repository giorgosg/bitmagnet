package model

import "github.com/bitmagnet-io/bitmagnet/internal/protocol"

// ModerationEvidenceData is private review metadata, independent of catalogue retention.
// Nil InfoHash/Files mean metadata was not available, rather than a complete empty list.
type ModerationEvidenceData struct {
	InfoHash *protocol.ID `json:"infoHash,omitempty"`
	Name     string       `json:"name"`
	Files    []string     `json:"files"`
	Rule     string       `json:"rule"`
	Matches  []string     `json:"matches"`
}
