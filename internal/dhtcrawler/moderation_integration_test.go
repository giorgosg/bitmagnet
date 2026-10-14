package dhtcrawler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	torrentmetainfo "github.com/anacrolix/torrent/metainfo"
	"github.com/bitmagnet-io/bitmagnet/internal/bloom"
	"github.com/bitmagnet-io/bitmagnet/internal/concurrency"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol/metainfo"
	migrationssql "github.com/bitmagnet-io/bitmagnet/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	boom "github.com/tylertreat/BoomFilters"
	"gorm.io/gorm"
)

func TestLegacyBlockDoesNotSuppressDiscovery(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	hash := protocol.ID{82}
	seedLegacyBlock(t, db, hash)
	c := newPersistTestCrawler(db.Query, 0)
	c.infoHashTriage = concurrency.NewBatchingChannel[nodeHasPeersForHash](1, 1, time.Millisecond)
	c.getPeers = concurrency.NewBufferedConcurrentChannel[nodeHasPeersForHash](2, 1)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	observations := make(chan nodeHasPeersForHash, 1)

	go func() { _ = c.getPeers.Run(ctx, func(observed nodeHasPeersForHash) { observations <- observed }) }()

	done := make(chan struct{})
	go func() { defer close(done); c.runInfoHashTriage(ctx) }()

	c.infoHashTriage.In() <- nodeHasPeersForHash{infoHash: hash}

	select {
	case observed := <-observations:
		require.Equal(t, hash, observed.infoHash)
	case <-time.After(time.Second):
		t.Error("legacy blocks must not suppress discovery")
	}

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("triage did not stop")
	}
}

func TestCrawlerCannotResurrectDigestOnlyBan(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	hash := protocol.ID{83}
	ledger := moderation.NewService(db.Gorm)
	data, err := json.Marshal(
		moderation.BanFile{
			Version:          1,
			IdentifierFormat: moderation.IdentifierFormatV1,
			Entries: []moderation.BanEntry{
				{Digest: moderation.IdentifierForInfoHash(hash).String(), Origin: moderation.Moderator},
			},
		},
	)
	require.NoError(t, err)
	_, err = ledger.Import(t.Context(), bytes.NewReader(data))
	require.NoError(t, err)

	c := newPersistTestCrawler(db.Query, 0)
	written, ok := c.writeTorrentBatch(
		t.Context(),
		[]infoHashWithMetaInfo{
			{
				nodeHasPeersForHash: nodeHasPeersForHash{infoHash: hash},
				metaInfo:            metainfo.Info{Name: "Synthetic handbook"},
			},
		},
	)
	require.True(t, ok)
	require.Empty(t, written, "banned crawler metadata must not be written or scraped")
}

func seedLegacyBlock(t *testing.T, db *dbtest.DB, hash protocol.ID) uint32 {
	t.Helper()
	tx, err := db.Pool.Begin(t.Context())

	require.NoError(t, err)
	defer func() { _ = tx.Rollback(t.Context()) }()

	lobs := tx.LargeObjects()
	oid, err := lobs.Create(t.Context(), 0)
	require.NoError(t, err)
	object, err := lobs.Open(t.Context(), oid, pgx.LargeObjectModeWrite)
	require.NoError(t, err)

	filter := boom.NewStableBloomFilter(100_000_000, 2, 0.001)
	filter.Add(hash[:])
	_, err = filter.WriteTo(object)
	require.NoError(t, err)
	require.NoError(t, object.Close())
	_, err = tx.Exec(
		t.Context(),
		"INSERT INTO bloom_filters (key, oid, created_at, updated_at) VALUES ('blocked_torrents', $1, now(), now())",
		oid,
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(t.Context()))

	return oid
}

func TestLegacyUpgradeDiscardsBlocksWithoutDecisions(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	sqlDB, err := sql.Open("pgx", db.DSN)
	require.NoError(t, err)

	defer func() { _ = sqlDB.Close() }()

	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrationssql.FS)
	require.NoError(t, err)
	_, err = provider.DownTo(t.Context(), 25)
	require.NoError(t, err)
	oid := seedLegacyBlock(t, db, protocol.ID{84})
	_, err = provider.Up(t.Context())
	require.NoError(t, err)

	var count int
	require.NoError(
		t,
		sqlDB.QueryRowContext(t.Context(), "SELECT count(*) FROM bloom_filters WHERE key = 'blocked_torrents'").
			Scan(&count),
	)
	require.Zero(t, count, "legacy blocks must be discarded on upgrade")
	require.NoError(
		t,
		sqlDB.QueryRowContext(t.Context(), "SELECT count(*) FROM pg_largeobject_metadata WHERE oid = $1", oid).
			Scan(&count),
	)
	require.Zero(t, count)
	require.NoError(t, sqlDB.QueryRowContext(
		t.Context(), "SELECT count(*) FROM moderation_decisions",
	).Scan(&count))
	require.Zero(t, count, "discarded Bloom entries must never become decisions")
}

func TestPendingCrawlerRetainsUncappedPrivatePaths(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	ledger := moderation.NewService(db.Gorm)
	hash := protocol.ID{88}
	id := moderation.IdentifierForInfoHash(hash)
	require.NoError(t, ledger.Flag(t.Context(), id, moderation.Evidence{}, "synthetic"))

	c := newPersistTestCrawler(db.Query, 0)
	c.saveFilesThreshold = 1
	_, ok := c.writeTorrentBatch(
		t.Context(),
		[]infoHashWithMetaInfo{
			{
				nodeHasPeersForHash: nodeHasPeersForHash{infoHash: hash},
				metaInfo: metainfo.Info{Name: "Synthetic collection", Files: []torrentmetainfo.FileInfo{
					{Path: []string{"folder", "one.pdf"}, Length: 1024},
					{Path: []string{"folder", "two.pdf"}, Length: 1024},
					{Path: []string{"folder", "three.pdf"}, Length: 1024},
				}},
			},
		},
	)
	require.True(t, ok)
	record, err := ledger.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, []string{"folder/one.pdf", "folder/two.pdf", "folder/three.pdf"}, record.Evidence.Files)
	_, err = db.Query.Torrent.WithContext(t.Context()).Where(db.Query.Torrent.InfoHash.Eq(hash)).Delete()
	require.NoError(t, err)
	record, err = ledger.Get(t.Context(), id)
	require.NoError(t, err)
	require.Len(t, record.Evidence.Files, 3, "catalogue deletion must not destroy review evidence")
	require.NoError(t, ledger.Allow(t.Context(), id, "synthetic"))
	written, ok := c.writeTorrentBatch(
		t.Context(),
		[]infoHashWithMetaInfo{
			{
				nodeHasPeersForHash: nodeHasPeersForHash{infoHash: hash},
				metaInfo:            metainfo.Info{Name: "Allowed handbook"},
			},
		},
	)
	require.True(t, ok)
	require.Contains(t, written, hash)
}

func TestCrawlerWaitsForRacingBan(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	ledger := moderation.NewService(db.Gorm)
	hash := protocol.ID{91}
	decisionReady, release := make(chan struct{}), make(chan struct{})

	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()

	require.NoError(
		t,
		db.Gorm.Callback().Create().Before("gorm:create").Register(
			"pause_synthetic_ban", func(tx *gorm.DB) {
				if h, ok := tx.Statement.Dest.(*model.ModerationHistory); ok && h.Action == "ban" {
					close(decisionReady)
					<-release
				}
			}),
	)

	banned := make(chan error, 1)
	go func() { banned <- ledger.Ban(t.Context(), moderation.IdentifierForInfoHash(hash), "synthetic") }()

	select {
	case <-decisionReady:
	case <-time.After(2 * time.Second):
		t.Fatal("ban did not begin")
	}

	c := newPersistTestCrawler(db.Query, 0)
	written := make(chan map[protocol.ID]infoHashWithMetaInfo, 1)

	go func() {
		result, _ := c.writeTorrentBatch(
			t.Context(),
			[]infoHashWithMetaInfo{
				{
					nodeHasPeersForHash: nodeHasPeersForHash{infoHash: hash},
					metaInfo:            metainfo.Info{Name: "Racing handbook"},
				},
			},
		)
		written <- result
	}()

	early := false

	select {
	case <-written:
		early = true

		t.Error("crawler persisted while ban was uncommitted")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	require.NoError(t, <-banned)

	if !early {
		select {
		case result := <-written:
			require.Empty(t, result)
		case <-time.After(2 * time.Second):
			t.Fatal("crawler write did not finish")
		}
	}

	count, err := db.Query.Torrent.WithContext(t.Context()).Where(db.Query.Torrent.InfoHash.Eq(hash)).Count()
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestDiscoveryExcludesExactBansAndPermitsPendingAndAllowed(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	ledger := moderation.NewService(db.Gorm)
	banned, pending, allowed := protocol.ID{92}, protocol.ID{93}, protocol.ID{94}
	require.NoError(
		t,
		ledger.ApplyImport(
			t.Context(),
			moderation.IdentifierForInfoHash(banned),
			moderation.Moderator,
			"synthetic",
		),
	)
	require.NoError(
		t,
		ledger.Flag(t.Context(), moderation.IdentifierForInfoHash(pending), moderation.Evidence{}, "synthetic"),
	)
	require.NoError(t, ledger.Allow(t.Context(), moderation.IdentifierForInfoHash(allowed), "synthetic"))

	c := newPersistTestCrawler(db.Query, 0)
	c.infoHashTriage = concurrency.NewBatchingChannel[nodeHasPeersForHash](3, 3, time.Millisecond)
	c.getPeers = concurrency.NewBufferedConcurrentChannel[nodeHasPeersForHash](3, 1)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	observations := make(chan protocol.ID, 3)

	go func() {
		_ = c.getPeers.Run(ctx, func(observed nodeHasPeersForHash) { observations <- observed.infoHash })
	}()

	done := make(chan struct{})
	go func() { defer close(done); c.runInfoHashTriage(ctx) }()

	for _, hash := range []protocol.ID{banned, pending, allowed} {
		c.infoHashTriage.In() <- nodeHasPeersForHash{infoHash: hash}
	}

	actual := make([]protocol.ID, 0, 2)

	for range 2 {
		select {
		case hash := <-observations:
			actual = append(actual, hash)
		case <-time.After(time.Second):
			t.Fatal("eligible discovery was lost")
		}
	}

	require.ElementsMatch(t, []protocol.ID{pending, allowed}, actual)

	select {
	case hash := <-observations:
		t.Errorf("unexpected discovery: %v", hash)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("triage did not stop")
	}
}

func TestLegacyUpgradePreservesSharedObjectAndOtherFilter(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	sqlDB, err := sql.Open("pgx", db.DSN)
	require.NoError(t, err)

	defer func() { _ = sqlDB.Close() }()

	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrationssql.FS)
	require.NoError(t, err)
	_, err = provider.DownTo(t.Context(), 25)
	require.NoError(t, err)
	oid := seedLegacyBlock(t, db, protocol.ID{95})
	_, err = sqlDB.ExecContext(
		t.Context(),
		"INSERT INTO bloom_filters (key, oid, created_at, updated_at) VALUES ('unrelated', $1, now(), now())",
		oid,
	)
	require.NoError(t, err)
	_, err = provider.Up(t.Context())
	require.NoError(t, err)

	var count int
	require.NoError(
		t,
		sqlDB.QueryRowContext(t.Context(), "SELECT count(*) FROM bloom_filters WHERE key = 'blocked_torrents'").
			Scan(&count),
	)
	require.Zero(t, count)
	require.NoError(
		t,
		sqlDB.QueryRowContext(t.Context(), "SELECT count(*) FROM bloom_filters WHERE key = 'unrelated'").
			Scan(&count),
	)
	require.Equal(t, 1, count)
	require.NoError(
		t,
		sqlDB.QueryRowContext(t.Context(), "SELECT count(*) FROM pg_largeobject_metadata WHERE oid = $1", oid).
			Scan(&count),
	)
	require.Equal(t, 1, count, "unrelated shared object must survive the discard")
}

func TestBannedCrawlerScrapeCannotUpdateCatalogueSource(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	hash := protocol.ID{96}
	require.NoError(t, db.Query.Torrent.WithContext(t.Context()).Create(&model.Torrent{
		InfoHash: hash, Name: "Synthetic handbook", FilesStatus: model.FilesStatusSingle,
	}))
	require.NoError(t, db.Query.TorrentsTorrentSource.WithContext(t.Context()).Create(&model.TorrentsTorrentSource{
		InfoHash: hash, Source: "dht", Seeders: model.NewNullUint(3), Leechers: model.NewNullUint(4),
	}))
	ledger := moderation.NewService(db.Gorm)
	require.NoError(t, ledger.Ban(t.Context(), moderation.IdentifierForInfoHash(hash), "synthetic"))

	filter := *bloom.NewWithEstimates(100, 0.01)
	c := newPersistTestCrawler(db.Query, 0)
	c.persistSourceBatch(t.Context(), []infoHashWithScrape{{
		nodeHasPeersForHash: nodeHasPeersForHash{infoHash: hash}, bfsd: filter, bfpe: filter,
	}})
	source, err := db.Query.TorrentsTorrentSource.WithContext(t.Context()).
		Where(db.Query.TorrentsTorrentSource.InfoHash.Eq(hash)).First()
	require.NoError(t, err)
	require.Equal(t, model.NewNullUint(3), source.Seeders, "an in-flight scrape must not write a banned source")
	require.Equal(t, model.NewNullUint(4), source.Leechers)
}
