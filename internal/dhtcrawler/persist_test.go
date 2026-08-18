package dhtcrawler

import (
	"context"
	"encoding/json"
	"testing"

	torrentmetainfo "github.com/anacrolix/torrent/metainfo"
	"github.com/bitmagnet-io/bitmagnet/internal/concurrency"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/processor"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol/metainfo"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestCreateTorrentModelDoesNotCountPaddingFilesTowardThreshold(t *testing.T) {
	t.Parallel()

	torrent, err := createTorrentModel(protocol.ID{}, metainfo.Info{
		Name: "release",
		Files: []torrentmetainfo.FileInfo{
			{Path: []string{".pad", "1024"}, Length: 1024},
			{Path: []string{"movie.mkv"}, Length: 10_000},
			{Path: []string{"subs.srt"}, Length: 1_000},
		},
	}, false, 2)
	require.NoError(t, err)
	require.Equal(t, model.FilesStatusMulti, torrent.FilesStatus)
	require.Len(t, torrent.Files, 2)
	require.Equal(t, []uint{1, 2}, []uint{torrent.Files[0].Index, torrent.Files[1].Index})
}

func TestPersistTorrentsPausesClassificationUntilQueueDrains(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	ctx := context.Background()
	crawler := newPersistTestCrawler(db.Query, 1)
	backlog, err := model.NewQueueJob("backlog", map[string]string{"test": "backlog"})
	require.NoError(t, err)
	require.NoError(t, db.Query.QueueJob.WithContext(ctx).Create(&backlog))
	require.NoError(t, crawler.refreshQueueDepth(ctx))
	require.EqualValues(t, 1, crawler.queueDepth.Get())

	blockedHash := protocol.ID{1}
	crawler.persistTorrentBatch(ctx, []infoHashWithMetaInfo{{
		nodeHasPeersForHash: nodeHasPeersForHash{infoHash: blockedHash},
		metaInfo:            metainfo.Info{Name: "blocked"},
	}})

	_, err = db.Query.Torrent.WithContext(ctx).Where(db.Query.Torrent.InfoHash.Eq(blockedHash)).First()
	require.NoError(t, err, "backpressure must not drop discovered torrents")
	classificationJobs, err := db.Query.QueueJob.WithContext(ctx).
		Where(db.Query.QueueJob.Queue.Eq(processor.MessageName)).
		Find()
	require.NoError(t, err)
	require.Empty(t, classificationJobs)

	require.NoError(t, db.Gorm.WithContext(ctx).Model(&model.QueueJob{}).
		Where("queue = ?", "backlog").
		Update("status", model.QueueJobStatusProcessed).Error)
	require.NoError(t, crawler.refreshQueueDepth(ctx))
	require.Zero(t, crawler.queueDepth.Get())

	resumedHash := protocol.ID{2}
	crawler.persistTorrentBatch(ctx, []infoHashWithMetaInfo{{
		nodeHasPeersForHash: nodeHasPeersForHash{infoHash: resumedHash},
		metaInfo:            metainfo.Info{Name: "resumed"},
	}})

	classificationJobs, err = db.Query.QueueJob.WithContext(ctx).
		Where(db.Query.QueueJob.Queue.Eq(processor.MessageName)).
		Find()
	require.NoError(t, err)
	require.Len(t, classificationJobs, 1)

	var message processor.MessageParams

	require.NoError(t, json.Unmarshal([]byte(classificationJobs[0].Payload), &message))
	require.Equal(t, []protocol.ID{resumedHash}, message.InfoHashes)
}

func TestPersistTorrentsQueueDepthZeroDisablesBackpressure(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	ctx := context.Background()
	crawler := newPersistTestCrawler(db.Query, 0)
	backlog, err := model.NewQueueJob("backlog", map[string]string{"test": "backlog"})
	require.NoError(t, err)
	require.NoError(t, db.Query.QueueJob.WithContext(ctx).Create(&backlog))
	require.NoError(t, crawler.refreshQueueDepth(ctx))
	require.EqualValues(t, 1, crawler.queueDepth.Get())

	infoHash := protocol.ID{3}
	crawler.persistTorrentBatch(ctx, []infoHashWithMetaInfo{{
		nodeHasPeersForHash: nodeHasPeersForHash{infoHash: infoHash},
		metaInfo:            metainfo.Info{Name: "unlimited"},
	}})

	classificationJobs, err := db.Query.QueueJob.WithContext(ctx).
		Where(db.Query.QueueJob.Queue.Eq(processor.MessageName)).
		Find()
	require.NoError(t, err)
	require.Len(t, classificationJobs, 1)
}

func newPersistTestCrawler(query *dao.Query, maxQueueDepth uint) *crawler {
	return &crawler{
		dao:                query,
		logger:             zap.NewNop().Sugar(),
		maxQueueDepth:      maxQueueDepth,
		queueDepth:         &concurrency.AtomicValue[int64]{},
		saveFilesThreshold: 100,
		scrape:             concurrency.NewBufferedConcurrentChannel[nodeHasPeersForHash](10, 1),
		persistedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "dhtcrawler_persist_test_total",
			Help: "Test counter.",
		}, []string{"entity"}),
	}
}
