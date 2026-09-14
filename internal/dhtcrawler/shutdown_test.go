package dhtcrawler

import (
	"context"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/bloom"
	"github.com/bitmagnet-io/bitmagnet/internal/concurrency"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol/dht/ktable"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol/metainfo"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	boom "github.com/tylertreat/BoomFilters"
	"go.uber.org/zap"
)

// Every pipeline stage has to return when the crawler's context is cancelled,
// because the stop hook now waits for all of them. These two poll the routing
// table and then sleep, and an empty table skips the only cancellation check
// they had - so on an idle instance they never noticed the stop at all.
func TestNodeFeedsStopWhenCancelled(t *testing.T) {
	t.Parallel()

	for name, run := range map[string]func(*crawler, context.Context){
		"getNodesForFindNode": func(c *crawler, ctx context.Context) {
			c.getNodesForFindNode(ctx)
		},
		"getNodesForSampleInfoHashes": func(c *crawler, ctx context.Context) {
			c.getNodesForSampleInfoHashes(ctx)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// An empty routing table is the case that matters: with no nodes to
			// hand on, the loop body never reaches a select on the context.
			table := ktable.New(ktable.Params{NodeID: protocol.RandomNodeID()}).Table
			c := &crawler{
				kTable:                   table,
				nodesForFindNode:         concurrency.NewBufferedConcurrentChannel[ktable.Node](1, 1),
				nodesForSampleInfoHashes: concurrency.NewBufferedConcurrentChannel[ktable.Node](1, 1),
			}

			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})

			go func() {
				defer close(done)

				run(c, ctx)
			}()

			cancel()

			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("the node feed did not return after its context was cancelled")
			}
		})
	}
}

// newShutdownTestCrawler builds a crawler that is complete enough to start and
// stop, and idle enough not to need the network. The dependencies left nil are
// only reached once an item flows through a stage, and nothing flows here: the
// routing table is empty, so no node is ever pinged, sampled or asked for peers.
func newShutdownTestCrawler(query *dao.Query) *crawler {
	table := ktable.New(ktable.Params{NodeID: protocol.RandomNodeID()}).Table

	c := &crawler{
		kTable:                       table,
		reseedBootstrapNodesInterval: time.Hour,
		reseedRequests:               make(chan struct{}, 1),
		getOldestNodesInterval:       time.Hour,
		oldPeerThreshold:             15 * time.Minute,
		discoveredNodes:              concurrency.NewBatchingChannel[ktable.Node](10, 10, time.Hour),
		nodesForPing:                 concurrency.NewBufferedConcurrentChannel[ktable.Node](10, 10),
		nodesForFindNode:             concurrency.NewBufferedConcurrentChannel[ktable.Node](10, 10),
		nodesForSampleInfoHashes:     concurrency.NewBufferedConcurrentChannel[ktable.Node](10, 10),
		infoHashTriage:               concurrency.NewBatchingChannel[nodeHasPeersForHash](10, 10, time.Hour),
		getPeers:                     concurrency.NewBufferedConcurrentChannel[nodeHasPeersForHash](10, 10),
		scrape:                       concurrency.NewBufferedConcurrentChannel[nodeHasPeersForHash](10, 10),
		requestMetaInfo:              concurrency.NewBufferedConcurrentChannel[infoHashWithPeers](10, 10),
		// An hour between flushes is the point: anything handed over stays
		// buffered, so what reaches the database came out of the shutdown.
		persistTorrents:    concurrency.NewBatchingChannel[infoHashWithMetaInfo](1000, 1000, time.Hour),
		persistSources:     concurrency.NewBatchingChannel[infoHashWithScrape](1000, 1000, time.Hour),
		saveFilesThreshold: 100,
		dao:                query,
		ignoreHashes: &ignoreHashes{
			bloom: boom.NewStableBloomFilter(10_000, 2, 0.001),
		},
		soughtNodeID: &concurrency.AtomicValue[protocol.ID]{},
		persistedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "dhtcrawler_shutdown_test_total",
			Help: "Test counter.",
		}, []string{labelEntity}),
		logger:     zap.NewNop().Sugar(),
		queueDepth: &concurrency.AtomicValue[int64]{},
	}
	c.soughtNodeID.Set(protocol.RandomNodeID())

	return c
}

// The whole point of the stop path: start the crawler, hand it a torrent, stop
// it, and find the torrent in the database. Batches were previously discarded
// with the pipeline - up to a thousand of them, or a minute's crawling.
func TestStopWritesTheBufferedBatch(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	c := newShutdownTestCrawler(db.Query)

	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)

		c.start(ctx)
	}()

	hash := protocol.ID{1}
	c.persistTorrents.In() <- infoHashWithMetaInfo{
		nodeHasPeersForHash: nodeHasPeersForHash{infoHash: hash},
		metaInfo:            metainfo.Info{Name: "buffered when the crawler stopped"},
	}

	cancel()

	select {
	case <-stopped:
	case <-time.After(30 * time.Second):
		t.Fatal("the crawler did not stop after its context was cancelled")
	}

	torrent, err := db.Query.Torrent.WithContext(t.Context()).
		Where(db.Query.Torrent.InfoHash.Eq(hash)).First()
	require.NoError(t, err, "a torrent buffered when the crawler stopped must still be written")
	require.Equal(t, "buffered when the crawler stopped", torrent.Name)
}

// The scraped seeder and leecher counts are buffered the same way, on their own
// channel, and are lost the same way.
func TestStopWritesTheBufferedSources(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	c := newShutdownTestCrawler(db.Query)

	hash := protocol.ID{2}
	torrent := model.Torrent{
		InfoHash:    hash,
		Name:        "already indexed",
		Size:        1,
		FilesStatus: model.FilesStatusSingle,
	}
	require.NoError(t, db.Query.Torrent.WithContext(t.Context()).Create(&torrent))

	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)

		c.start(ctx)
	}()

	c.persistSources.In() <- infoHashWithScrape{
		nodeHasPeersForHash: nodeHasPeersForHash{infoHash: hash},
		bfsd:                bloom.Filter{},
		bfpe:                bloom.Filter{},
	}

	cancel()

	select {
	case <-stopped:
	case <-time.After(30 * time.Second):
		t.Fatal("the crawler did not stop after its context was cancelled")
	}

	source, err := db.Query.TorrentsTorrentSource.WithContext(t.Context()).
		Where(db.Query.TorrentsTorrentSource.InfoHash.Eq(hash)).First()
	require.NoError(t, err, "a scrape buffered when the crawler stopped must still be written")
	require.Equal(t, "dht", source.Source)
}
