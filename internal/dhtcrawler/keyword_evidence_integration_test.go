package dhtcrawler

import (
	"testing"

	torrentmetainfo "github.com/anacrolix/torrent/metainfo"
	"github.com/bitmagnet-io/bitmagnet/internal/classifier"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/database/search"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/processor"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol/metainfo"
	"github.com/bitmagnet-io/bitmagnet/internal/tmdb"
	tmdb_mocks "github.com/bitmagnet-io/bitmagnet/internal/tmdb/mocks"
	"github.com/stretchr/testify/require"
)

func TestInitialKeywordDetectionPreservesObservedPaths(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	ledger := moderation.NewService(db.Gorm)
	hash := protocol.ID{121}
	id := moderation.IdentifierForInfoHash(hash)
	_, err := ledger.Get(t.Context(), id)
	require.ErrorIs(t, err, moderation.ErrNotFound)

	cfg := classifier.NewDefaultConfig()
	cfg.Keywords = map[string][]string{"banned": {"synthetic review marker"}}
	cfg.Flags = map[string]any{"apis_enabled": false, "local_search_enabled": false}
	sr := search.New(search.Params{Query: lazy.New(func() (*dao.Query, error) { return db.Query, nil })})
	ml := lazy.New(func() (*moderation.Service, error) { return ledger, nil })
	cr := classifier.New(classifier.Params{
		Config: cfg, ModerationConfig: moderation.NewDefaultConfig(), Moderation: ml,
		MetadataSearch: sr.MetadataSearch,
		TmdbClient:     lazy.New(func() (tmdb.Client, error) { return tmdb_mocks.NewClient(t), nil }),
	})
	c := newPersistTestCrawler(db.Query, 0)
	c.saveFilesThreshold = 1
	c.keywordPolicy, err = cr.KeywordPolicy.Get()
	require.NoError(t, err)
	_, ok := c.writeTorrentBatch(t.Context(), []infoHashWithMetaInfo{
		{
			nodeHasPeersForHash: nodeHasPeersForHash{infoHash: hash},
			metaInfo: metainfo.Info{
				Name: "Harmless synthetic review marker collection",
				Files: []torrentmetainfo.FileInfo{
					{Path: []string{"folder", "one.pdf"}, Length: 1024},
					{Path: []string{"folder", "two.pdf"}, Length: 1024},
					{Path: []string{"folder", "three.pdf"}, Length: 1024},
				},
			},
		},
	})
	require.True(t, ok)

	pr := processor.New(processor.Params{
		Moderation: ml, ClassifierConfig: cfg, Search: sr.Search, Workflow: cr.Runner,
		Dao: lazy.New(func() (*dao.Query, error) { return db.Query, nil }),
	})
	p, err := pr.Processor.Get()
	require.NoError(t, err)
	require.NoError(t, p.Process(t.Context(), processor.MessageParams{InfoHashes: []protocol.ID{hash}}))
	record, err := ledger.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, moderation.Pending, record.Status)
	require.Equal(t, []string{"folder/one.pdf", "folder/two.pdf", "folder/three.pdf"}, record.Evidence.Files)
}

func TestPendingSingleFileRetainsPath(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	ledger := moderation.NewService(db.Gorm)
	hash := protocol.ID{122}
	id := moderation.IdentifierForInfoHash(hash)
	require.NoError(t, ledger.Flag(t.Context(), id, moderation.Evidence{}, "synthetic"))

	c := newPersistTestCrawler(db.Query, 0)
	_, ok := c.writeTorrentBatch(t.Context(), []infoHashWithMetaInfo{{
		nodeHasPeersForHash: nodeHasPeersForHash{infoHash: hash},
		metaInfo:            metainfo.Info{Name: "Harmless handbook.pdf", Length: 1024},
	}})
	require.True(t, ok)
	record, err := ledger.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, []string{"Harmless handbook.pdf"}, record.Evidence.Files)
}

func TestKeywordIngestionChecksPathsBeyondCatalogueLimit(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		mode     moderation.Config
		allowed  bool
		ordinary bool
		status   moderation.Status
		written  bool
	}{
		{name: "pending", status: moderation.Pending, written: true},
		{name: "automatic ban", mode: moderation.Config{AutomaticBan: true}, status: moderation.Banned},
		{name: "local allow", allowed: true, status: moderation.Allowed, written: true},
		{name: "ordinary", ordinary: true, written: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := dbtest.New(t)
			hash := protocol.ID{124}
			ledger := moderation.NewService(db.Gorm)

			id := moderation.IdentifierForInfoHash(hash)
			if tc.allowed {
				require.NoError(t, ledger.Allow(t.Context(), id, "synthetic"))
			}

			cfg := classifier.NewDefaultConfig()
			cfg.Keywords = map[string][]string{"banned": {"synthetic review marker"}}
			cr := classifier.New(classifier.Params{Config: cfg, ModerationConfig: tc.mode})
			c := newPersistTestCrawler(db.Query, 0)

			var err error

			c.keywordPolicy, err = cr.KeywordPolicy.Get()
			require.NoError(t, err)

			c.saveFilesThreshold = 1

			lastPath := "synthetic review marker.pdf"
			if tc.ordinary {
				lastPath = "three.pdf"
			}

			written, ok := c.writeTorrentBatch(t.Context(), []infoHashWithMetaInfo{{
				nodeHasPeersForHash: nodeHasPeersForHash{infoHash: hash},
				metaInfo: metainfo.Info{Name: "Harmless collection", Files: []torrentmetainfo.FileInfo{
					{Path: []string{"folder", "one.pdf"}, Length: 1024},
					{Path: []string{"folder", "two.pdf"}, Length: 1024},
					{Path: []string{"folder", lastPath}, Length: 1024},
				}},
			}})
			require.True(t, ok)
			require.Equal(t, tc.written, len(written) == 1)

			record, err := ledger.Get(t.Context(), id)
			if tc.ordinary {
				require.ErrorIs(t, err, moderation.ErrNotFound)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.status, record.Status)

			if tc.allowed {
				require.Nil(t, record.Evidence)
				return
			}

			require.Equal(
				t,
				[]string{"folder/one.pdf", "folder/two.pdf", "folder/" + lastPath},
				record.Evidence.Files,
			)
			require.NotEmpty(t, record.Evidence.Rule)
			require.NotEmpty(t, record.Evidence.Matches)
		})
	}
}
