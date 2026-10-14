package fixtureserver

import (
	"context"
	"fmt"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/classifier"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/search"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/processor"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/bitmagnet-io/bitmagnet/internal/tmdb"
	"go.uber.org/zap"
)

// The opt-in browser fixture uses only harmless synthetic identifiers and metadata.
// It runs the real classifier and processor, with external APIs and local content
// matching disabled, so reprocessing cannot fetch third-party data.
func newModerationFixture(
	ctx context.Context,
	query *dao.Query,
	ledger *moderation.Service,
) (processor.Processor, error) {
	ld := lazy.New(func() (*dao.Query, error) { return query, nil })
	lm := lazy.New(func() (*moderation.Service, error) { return ledger, nil })
	sr := search.New(search.Params{Query: ld})
	cfg := classifier.NewDefaultConfig()
	cfg.Keywords = map[string][]string{"banned": {"synthetic review marker"}}
	cfg.Flags = map[string]any{"apis_enabled": false, "local_search_enabled": false}
	api := tmdb.New(tmdb.Params{Config: tmdb.Config{Enabled: false}, Logger: zap.NewNop().Sugar()})

	cr := classifier.New(classifier.Params{
		Config: cfg, ModerationConfig: moderation.NewDefaultConfig(), Moderation: lm,
		MetadataSearch: sr.MetadataSearch,
		TmdbClient:     api.Client,
	})

	pr := processor.New(processor.Params{
		Moderation: lm, ClassifierConfig: cfg, Search: sr.Search, Workflow: cr.Runner, Dao: ld,
	})

	p, err := pr.Processor.Get()
	if err != nil {
		return nil, fmt.Errorf("fixtureserver: moderation processor: %w", err)
	}

	now := time.Now().UTC()

	for i := range 68 {
		hash := protocol.ID{0xfa, 0xb7, byte(i)}

		name := fmt.Sprintf("Harmless synthetic review marker handbook %02d.pdf", i)
		if i >= 10 && i != 54 && i != 55 {
			name = fmt.Sprintf("Harmless pagination synthetic review marker handbook %02d.pdf", i)
		}

		if i == 0 {
			name = "Harmless catalogue handbook.pdf"
		} else if i >= 66 {
			name = fmt.Sprintf("Harmless catalogue handbook %02d.pdf", i)
		}

		filesStatus := model.FilesStatusMulti
		filesCount := model.NewNullUint(1)

		if i == 55 {
			filesStatus = model.FilesStatusNoInfo
			filesCount = model.NullUint{}
		}

		err = query.Torrent.WithContext(ctx).Create(&model.Torrent{
			InfoHash: hash, Name: name, Size: 1024, FilesStatus: filesStatus,
			FilesCount: filesCount, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return nil, err
		}

		if i != 55 {
			err = query.TorrentFile.WithContext(ctx).Create(&model.TorrentFile{
				InfoHash: hash, Index: 0, Path: fmt.Sprintf("full/private/handbook %02d.pdf", i),
				Size: 1024, Extension: model.NewNullString("pdf"),
			})
			if err != nil {
				return nil, err
			}
		}

		if err = p.Process(ctx, processor.MessageParams{InfoHashes: []protocol.ID{hash}}); err != nil {
			return nil, err
		}
	}

	// A digest-only record is valid; there are deliberately no names or raw hashes
	// for the review UI to reconstruct.
	id := moderation.IdentifierForInfoHash(protocol.ID{0xfa, 0xb7, 90})
	if err = ledger.Ban(ctx, id, "synthetic fixture moderator"); err != nil {
		return nil, err
	}

	return p, nil
}
