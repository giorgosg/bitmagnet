package processor

import (
	"context"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/classifier"
	"github.com/bitmagnet-io/bitmagnet/internal/classifier/classification"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/database/search"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessPreservesRuleDerivedTypeWithoutStoredFiles(t *testing.T) {
	t.Parallel()

	for _, filesStatus := range []model.FilesStatus{
		model.FilesStatusNoInfo,
		model.FilesStatusOverThreshold,
	} {
		t.Run(filesStatus.String(), func(t *testing.T) {
			t.Parallel()

			db := dbtest.New(t)
			ctx := context.Background()
			now := time.Now().UTC()
			infoHash := protocol.ID{1}

			require.NoError(t, db.Query.Torrent.WithContext(ctx).Create(&model.Torrent{
				InfoHash:    infoHash,
				Name:        "rule-derived type",
				CreatedAt:   now,
				UpdatedAt:   now,
				FilesStatus: filesStatus,
			}))
			require.NoError(t, db.Query.TorrentContent.WithContext(ctx).Create(&model.TorrentContent{
				InfoHash:    infoHash,
				ContentType: model.NewNullContentType(model.ContentTypeXxx),
				CreatedAt:   now,
				UpdatedAt:   now,
			}))

			searchResult := search.New(search.Params{
				Query: lazy.New(func() (*dao.Query, error) { return db.Query, nil }),
			})
			searcher, err := searchResult.Search.Get()
			require.NoError(t, err)

			runner := &hintPreservingRunner{}
			processor := processor{
				defaultWorkflow: "default",
				search:          searcher,
				runner:          runner,
				dao:             db.Query,
				blockingManager: noOpBlockingManager{},
			}
			require.NoError(t, processor.Process(ctx, MessageParams{
				InfoHashes:   []protocol.ID{infoHash},
				ClassifyMode: ClassifyModeRematch,
			}))

			assert.Equal(t, model.ContentTypeXxx, runner.hint.ContentType)
			assert.False(t, runner.hint.ContentSource.Valid)

			contents, err := db.Query.TorrentContent.WithContext(ctx).
				Where(db.Query.TorrentContent.InfoHash.Eq(infoHash)).
				Find()
			require.NoError(t, err)
			require.Len(t, contents, 1)
			assert.Equal(t, model.ContentTypeXxx, contents[0].ContentType.ContentType)
			assert.False(t, contents[0].ContentSource.Valid)
		})
	}
}

type hintPreservingRunner struct {
	hint model.TorrentHint
}

func (r *hintPreservingRunner) Run(
	_ context.Context,
	_ string,
	_ classifier.Flags,
	torrent model.Torrent,
) (classification.Result, error) {
	r.hint = torrent.Hint

	return classification.Result{
		ContentAttributes: classification.ContentAttributes{
			ContentType: torrent.Hint.NullContentType(),
		},
	}, nil
}

type noOpBlockingManager struct{}

func (noOpBlockingManager) Filter(_ context.Context, hashes []protocol.ID) ([]protocol.ID, error) {
	return hashes, nil
}

func (noOpBlockingManager) Block(_ context.Context, _ []protocol.ID, _ bool) error {
	return nil
}

func (noOpBlockingManager) Flush(_ context.Context) error {
	return nil
}
