package queue

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/processor"
	"github.com/bitmagnet-io/bitmagnet/internal/processor/batch"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/stretchr/testify/require"
)

func TestBatchReprocessNullContentTypeQueuesOnlyUnclassifiedTorrents(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	ctx := t.Context()
	now := time.Now().UTC()
	unclassified := protocol.ID{1}
	classified := protocol.ID{2}

	for _, torrent := range []model.Torrent{
		{
			InfoHash:    unclassified,
			Name:        "unclassified",
			CreatedAt:   now,
			UpdatedAt:   now,
			FilesStatus: model.FilesStatusNoInfo,
		},
		{
			InfoHash:    classified,
			Name:        "classified",
			CreatedAt:   now,
			UpdatedAt:   now,
			FilesStatus: model.FilesStatusNoInfo,
		},
	} {
		require.NoError(t, db.Query.Torrent.WithContext(ctx).Create(&torrent))
	}

	require.NoError(t, db.Query.TorrentContent.WithContext(ctx).Create(
		&model.TorrentContent{
			InfoHash:  unclassified,
			CreatedAt: now,
			UpdatedAt: now,
		},
		&model.TorrentContent{
			InfoHash:    classified,
			ContentType: model.NewNullContentType(model.ContentTypeMovie),
			CreatedAt:   now,
			UpdatedAt:   now,
		},
	))

	result := New(Params{
		Dao: lazy.New(func() (*dao.Query, error) { return db.Query, nil }),
	})
	handler, err := result.Handler.Get()
	require.NoError(t, err)

	batchJob, err := batch.NewQueueJob(batch.MessageParams{
		UpdatedBefore: now.Add(time.Hour),
		BatchSize:     100,
		ChunkSize:     100,
		ContentTypes:  []model.NullContentType{{}},
	})
	require.NoError(t, err)
	require.NoError(t, handler.Handle(ctx, batchJob))

	jobs, err := db.Query.QueueJob.WithContext(ctx).
		Where(db.Query.QueueJob.Queue.Eq(processor.MessageName)).
		Find()
	require.NoError(t, err)
	require.Len(t, jobs, 1)

	var message processor.MessageParams

	require.NoError(t, json.Unmarshal([]byte(jobs[0].Payload), &message))
	require.Equal(t, []protocol.ID{unclassified}, message.InfoHashes)
}
