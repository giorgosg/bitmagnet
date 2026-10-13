package classifier

import (
	"errors"

	"github.com/bitmagnet-io/bitmagnet/internal/classifier/classification"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
)

const moderateKeywordsName = "moderate_keywords"

type moderateKeywordsAction struct{}

func (moderateKeywordsAction) name() string { return moderateKeywordsName }

var moderateKeywordsPayload = payloadLiteral[string]{
	literal:     moderateKeywordsName,
	description: "Quarantine harmful keywords or automatically ban when configured; local allows skip the check",
}

func (moderateKeywordsAction) compileAction(ctx compilerContext) (action, error) {
	if _, err := moderateKeywordsPayload.Unmarshal(ctx); err != nil {
		return action{}, ctx.error(err)
	}

	policy, err := moderation.NewKeywordPolicy(ctx.keywords["banned"], ctx.moderationConfig)
	if err != nil {
		return action{}, ctx.fatal(err)
	}

	path := ctx.path

	return action{run: func(ctx executionContext) (classification.Result, error) {
		id := moderation.IdentifierForInfoHash(ctx.torrent.InfoHash)
		if ctx.moderation != nil {
			record, getErr := ctx.moderation.Get(ctx.Context, id)
			if getErr != nil && !errors.Is(getErr, moderation.ErrNotFound) {
				return ctx.result, getErr
			}

			if getErr == nil && record.Status == moderation.Allowed {
				return ctx.result, nil
			}
		}

		// CEL may synthesize a file from a name when metainfo is absent. Evidence
		// records only supplied paths, or the implicit known single-file name.
		var files []string
		if ctx.torrent.FilesStatus.HasStoredFileList() || len(ctx.torrent.Files) > 0 {
			files = make([]string, 0, len(ctx.torrent.Files))
			for _, file := range ctx.torrent.Files {
				files = append(files, file.Path)
			}

			if ctx.torrent.FilesStatus == model.FilesStatusSingle && len(files) == 0 {
				files = append(files, ctx.torrent.Name)
			}
		}

		evidence := moderation.Evidence{
			InfoHash: &ctx.torrent.InfoHash, Name: ctx.torrent.Name, Files: files,
		}

		matched, err := policy.Apply(ctx.Context, ctx.moderation, evidence)
		if err != nil {
			return ctx.result, err
		}

		if !matched {
			return ctx.result, nil
		}
		// A moderator may have allowed the torrent while the keywords were evaluated.
		record, err := ctx.moderation.Get(ctx.Context, id)
		if err != nil {
			return ctx.result, err
		}

		if record.Status == moderation.Allowed {
			return ctx.result, nil
		}

		return ctx.result, classification.RuntimeError{Path: path, Cause: classification.ErrQuarantinedTorrent}
	}}, nil
}

func (moderateKeywordsAction) JSONSchema() JSONSchema { return moderateKeywordsPayload.JSONSchema() }
