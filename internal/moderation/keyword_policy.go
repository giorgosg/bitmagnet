package moderation

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/bitmagnet-io/bitmagnet/internal/keywords"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
)

// KeywordPolicy is the harmful-keyword check shared by ingestion and classification.
// Its evidence uses observed paths before the catalogue's file storage limit applies.
type KeywordPolicy struct {
	regex  *regexp.Regexp
	status Status
}

func NewKeywordPolicy(words []string, config Config) (*KeywordPolicy, error) {
	regex, err := keywords.NewRegexFromKeywords(words...)
	if err != nil {
		return nil, err
	}

	status := Pending
	if config.AutomaticBan {
		status = Banned
	}

	return &KeywordPolicy{regex: regex, status: status}, nil
}

// Apply retains only harmful matches. The service serializes this assertion with
// final local allows and bans; allowed identifiers never acquire automatic evidence.
func (p *KeywordPolicy) Apply(ctx context.Context, service *Service, evidence Evidence) (bool, error) {
	if p == nil {
		return false, nil
	}

	name := model.Torrent{Name: evidence.Name, Extension: model.FileExtensionFromPath(evidence.Name)}
	parts := []string{name.BaseName()}

	for _, path := range evidence.Files {
		file := model.TorrentFile{Path: path, Extension: model.FileExtensionFromPath(path)}
		parts = append(parts, file.BasePath())
	}

	evidence.Matches = p.regex.FindAllString(strings.Join(parts, " "), -1)
	if len(evidence.Matches) == 0 {
		return false, nil
	}

	if service == nil {
		return true, fmt.Errorf("harmful keyword check requires moderation service")
	}

	if evidence.InfoHash == nil {
		return true, ErrInvalidEvidence
	}

	evidence.Rule = "keywords.banned: " + p.regex.String()

	return true, service.ApplyAutomatic(ctx, IdentifierForInfoHash(*evidence.InfoHash), p.status, evidence)
}
