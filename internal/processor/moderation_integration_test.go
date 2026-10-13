package processor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/blocking"
	"github.com/bitmagnet-io/bitmagnet/internal/classifier"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/database/query"
	"github.com/bitmagnet-io/bitmagnet/internal/database/search"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/bitmagnet-io/bitmagnet/internal/tmdb"
	tmdb_mocks "github.com/bitmagnet-io/bitmagnet/internal/tmdb/mocks"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestKeywordMatchQuarantinesWithPrivateEvidence(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	p, ledger := moderationProcessor(t, db, classifier.NewDefaultConfig(), moderation.NewDefaultConfig())
	matched := protocol.ID{41}
	ordinary := protocol.ID{42}

	seedModerationTorrent(
		t,
		db,
		matched,
		"Harmless synthetic review marker collection",
		"folder/full synthetic review marker filename.pdf",
	)
	seedModerationTorrent(t, db, ordinary, "Ordinary handbook", "guide.pdf")
	require.NoError(t, p.Process(t.Context(), MessageParams{InfoHashes: []protocol.ID{matched, ordinary}}))
	record, err := ledger.Get(t.Context(), moderation.IdentifierForInfoHash(matched))
	require.NoError(t, err)
	require.Equal(t, moderation.Pending, record.Status)
	require.Equal(t, moderation.Automatic, record.Origin)
	require.NotNil(t, record.Evidence)
	require.Equal(t, "Harmless synthetic review marker collection", record.Evidence.Name)
	require.Equal(t, []string{"folder/full synthetic review marker filename.pdf"}, record.Evidence.Files)
	require.NotEmpty(t, record.Evidence.Rule)
	require.NotEmpty(t, record.Evidence.Matches)
	require.Equal(t, matched, *record.Evidence.InfoHash)
	visible, err := p.search.Torrents(t.Context(), query.Where(search.TorrentInfoHashCriteria(matched, ordinary)))
	require.NoError(t, err)
	require.Len(t, visible.Items, 1)
	require.Equal(t, ordinary, visible.Items[0].InfoHash)
	require.NoError(t, p.Process(t.Context(), MessageParams{InfoHashes: []protocol.ID{matched}}))
	history, err := ledger.History(t.Context(), record.Identifier, moderation.Page{})
	require.NoError(t, err)
	require.Len(t, history.Items, 1)
	require.NoError(t, ledger.Allow(t.Context(), record.Identifier, "synthetic moderator"))
	require.NoError(t, p.Process(t.Context(), MessageParams{InfoHashes: []protocol.ID{matched}}))
	contents, err := p.search.TorrentContent(
		t.Context(),
		query.Where(search.TorrentContentInfoHashCriteria(matched)),
	)
	require.NoError(t, err)
	require.Len(t, contents.Items, 1)
	require.Equal(t, model.ContentTypeEbook, contents.Items[0].ContentType.ContentType)
}

func moderationProcessor(
	t *testing.T,
	db *dbtest.DB,
	cfg classifier.Config,
	mode moderation.Config,
) (processor, *moderation.Service) {
	t.Helper()

	if cfg.Keywords == nil {
		cfg.Keywords = map[string][]string{"banned": {"synthetic review marker"}}
	}

	if cfg.Flags == nil {
		cfg.Flags = map[string]any{"apis_enabled": false, "local_search_enabled": false}
	}

	sr := search.New(search.Params{Query: lazy.New(func() (*dao.Query, error) { return db.Query, nil })})
	s, err := sr.Search.Get()
	require.NoError(t, err)

	ledger := moderation.NewService(db.Gorm)
	cr := classifier.New(classifier.Params{
		Config:           cfg,
		ModerationConfig: mode,
		Moderation:       lazy.New(func() (*moderation.Service, error) { return ledger, nil }),
		MetadataSearch:   sr.MetadataSearch,
		TmdbClient:       lazy.New(func() (tmdb.Client, error) { return tmdb_mocks.NewClient(t), nil }),
	})
	r, err := cr.Runner.Get()
	require.NoError(t, err)

	br := blocking.New(blocking.Params{
		Pool:        lazy.New(func() (*pgxpool.Pool, error) { return db.Pool, nil }),
		PgxPoolWait: &sync.WaitGroup{},
	})
	manager, err := br.Manager.Get()
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		require.NoError(t, br.AppHook.OnStop(ctx))
	})

	return processor{
		moderation:      ledger,
		defaultWorkflow: "default",
		search:          s,
		runner:          r,
		dao:             db.Query,
		blockingManager: manager,
	}, ledger
}

func seedModerationTorrent(t *testing.T, db *dbtest.DB, hash protocol.ID, name, path string) {
	t.Helper()

	now := time.Now().UTC()
	require.NoError(t, db.Query.Torrent.WithContext(t.Context()).Create(&model.Torrent{
		InfoHash: hash, Name: name, Size: 1024, FilesStatus: model.FilesStatusMulti,
		FilesCount: model.NewNullUint(1), CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, db.Query.TorrentFile.WithContext(t.Context()).Create(&model.TorrentFile{
		InfoHash: hash, Index: 0, Path: path, Size: 1024, Extension: model.NewNullString("pdf"),
	}))
}

func TestKeywordAutomaticBanRemainsDistinctFromReview(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	p, ledger := moderationProcessor(t, db, classifier.NewDefaultConfig(), moderation.Config{AutomaticBan: true})
	hash := protocol.ID{43}
	seedModerationTorrent(t, db, hash, "Harmless automatic synthetic review marker", "folder/book.pdf")
	require.NoError(t, p.Process(t.Context(), MessageParams{InfoHashes: []protocol.ID{hash}}))
	record, err := ledger.Get(t.Context(), moderation.IdentifierForInfoHash(hash))
	require.NoError(t, err)
	require.Equal(t, moderation.Banned, record.Status)
	require.Equal(t, moderation.Automatic, record.Origin)
	require.NotNil(t, record.Evidence)
	require.NoError(t, p.Process(t.Context(), MessageParams{InfoHashes: []protocol.ID{hash}}))
	history, err := ledger.History(t.Context(), record.Identifier, moderation.Page{})
	require.NoError(t, err)
	require.Len(t, history.Items, 1)
}

func TestAllowedTorrentIgnoresChangedKeywordsAndClassifies(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	cfg := classifier.NewDefaultConfig()
	cfg.Keywords = map[string][]string{"banned": {"new innocent keyword"}}
	p, ledger := moderationProcessor(t, db, cfg, moderation.Config{AutomaticBan: true})
	hash := protocol.ID{44}
	seedModerationTorrent(t, db, hash, "Harmless new innocent keyword handbook", "full/path/handbook.pdf")
	id := moderation.IdentifierForInfoHash(hash)
	require.NoError(t, ledger.Allow(t.Context(), id, "synthetic moderator"))
	require.NoError(t, p.Process(t.Context(), MessageParams{InfoHashes: []protocol.ID{hash}}))
	record, err := ledger.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, moderation.Allowed, record.Status)
	require.Nil(t, record.Evidence)
	contents, err := p.search.TorrentContent(t.Context(), query.Where(search.TorrentContentInfoHashCriteria(hash)))
	require.NoError(t, err)
	require.Len(t, contents.Items, 1)
	require.Equal(t, model.ContentTypeEbook, contents.Items[0].ContentType.ContentType)
}

func TestProcessStillReportsMissingUnmoderatedTorrent(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	p, _ := moderationProcessor(t, db, classifier.NewDefaultConfig(), moderation.NewDefaultConfig())
	err := p.Process(t.Context(), MessageParams{InfoHashes: []protocol.ID{{45}}})

	var missing MissingHashesError
	require.ErrorAs(t, err, &missing)
	require.Equal(t, []protocol.ID{{45}}, missing.InfoHashes)
}

func TestCategoryDeleteDoesNotCreateModerationDecision(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	cfg := classifier.NewDefaultConfig()
	cfg.Flags = map[string]any{
		"delete_content_types": []any{"ebook"},
		"apis_enabled":         false,
		"local_search_enabled": false,
	}
	p, ledger := moderationProcessor(t, db, cfg, moderation.NewDefaultConfig())
	hash := protocol.ID{46}
	seedModerationTorrent(t, db, hash, "Ordinary handbook", "handbook.pdf")
	require.NoError(t, p.Process(t.Context(), MessageParams{InfoHashes: []protocol.ID{hash}}))
	visible, err := p.search.Torrents(t.Context(), query.Where(search.TorrentInfoHashCriteria(hash)))
	require.NoError(t, err)
	require.Empty(t, visible.Items)
	_, err = ledger.Get(t.Context(), moderation.IdentifierForInfoHash(hash))
	require.ErrorIs(t, err, moderation.ErrNotFound)
}

func TestKeywordQuarantinePreservesMissingFileMetadata(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	p, ledger := moderationProcessor(t, db, classifier.NewDefaultConfig(), moderation.NewDefaultConfig())
	hash := protocol.ID{47}
	now := time.Now().UTC()
	require.NoError(t, db.Query.Torrent.WithContext(t.Context()).Create(&model.Torrent{
		InfoHash: hash, Name: "Harmless synthetic review marker.pdf", FilesStatus: model.FilesStatusNoInfo,
		Size: 1024, CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, p.Process(t.Context(), MessageParams{InfoHashes: []protocol.ID{hash}}))
	record, err := ledger.Get(t.Context(), moderation.IdentifierForInfoHash(hash))
	require.NoError(t, err)
	require.NotNil(t, record.Evidence)
	require.Nil(t, record.Evidence.Files)
	require.Equal(t, "Harmless synthetic review marker.pdf", record.Evidence.Name)
}
