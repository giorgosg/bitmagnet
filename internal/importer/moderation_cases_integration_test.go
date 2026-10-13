package importer

import (
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestImportPendingRetainsEvidenceAndLocalAllowIngests(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	ledger := moderation.NewService(db.Gorm)
	pending, allowed := protocol.ID{85}, protocol.ID{86}
	require.NoError(
		t,
		ledger.Flag(t.Context(), moderation.IdentifierForInfoHash(pending), moderation.Evidence{}, "synthetic"),
	)
	require.NoError(t, ledger.Allow(t.Context(), moderation.IdentifierForInfoHash(allowed), "synthetic"))
	require.NoError(
		t,
		ledger.ApplyImport(
			t.Context(),
			moderation.IdentifierForInfoHash(allowed),
			moderation.Moderator,
			"synthetic",
		),
	)
	imp := importer{dao: db.Query, bufferSize: 2, maxWaitTime: time.Hour}.New(t.Context(), Info{})
	require.NoError(
		t,
		imp.Import(
			Item{Source: "synthetic", InfoHash: pending, Name: "Pending handbook"},
			Item{Source: "synthetic", InfoHash: allowed, Name: "Allowed handbook"},
		),
	)
	imp.Drain()
	require.NoError(t, imp.Close())
	record, err := ledger.Get(t.Context(), moderation.IdentifierForInfoHash(pending))
	require.NoError(t, err)
	require.NotNil(t, record.Evidence)
	require.Equal(t, "Pending handbook", record.Evidence.Name)
	_, err = db.Query.Torrent.WithContext(t.Context()).Where(db.Query.Torrent.InfoHash.Eq(pending)).Delete()
	require.NoError(t, err)
	record, err = ledger.Get(t.Context(), moderation.IdentifierForInfoHash(pending))
	require.NoError(t, err)
	require.Equal(t, "Pending handbook", record.Evidence.Name)
	count, err := db.Query.Torrent.WithContext(t.Context()).Where(db.Query.Torrent.InfoHash.Eq(allowed)).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

func TestImportWaitsForRacingBan(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	ledger := moderation.NewService(db.Gorm)
	hash := protocol.ID{87}
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

	imp := importer{dao: db.Query, bufferSize: 1, maxWaitTime: time.Hour}.New(t.Context(), Info{})
	require.NoError(t, imp.Import(Item{Source: "synthetic", InfoHash: hash, Name: "Racing handbook"}))

	drained := make(chan struct{})

	go func() { imp.Drain(); close(drained) }()

	select {
	case <-drained:
		t.Error("ingestion completed while ban was uncommitted")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	require.NoError(t, <-banned)

	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("import did not finish")
	}

	require.NoError(t, imp.Close())
	count, err := db.Query.Torrent.WithContext(t.Context()).Where(db.Query.Torrent.InfoHash.Eq(hash)).Count()
	require.NoError(t, err)
	require.Zero(t, count, "racing ban must prevent catalogue resurrection")
}

func TestBannedImportPreservesExistingCataloguePaths(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	hash := protocol.ID{97}
	require.NoError(t, db.Query.Torrent.WithContext(t.Context()).Create(&model.Torrent{
		InfoHash:    hash,
		Name:        "Existing handbook",
		FilesStatus: model.FilesStatusMulti,
		FilesCount:  model.NewNullUint(1),
	}))
	require.NoError(t, db.Query.TorrentFile.WithContext(t.Context()).Create(&model.TorrentFile{
		InfoHash: hash, Path: "full/private/handbook.pdf", Size: 1024,
	}))
	ledger := moderation.NewService(db.Gorm)
	id := moderation.IdentifierForInfoHash(hash)
	require.NoError(t, ledger.ApplyImport(t.Context(), id, moderation.Moderator, "synthetic"))
	imp := importer{dao: db.Query, bufferSize: 1, maxWaitTime: time.Hour}.New(t.Context(), Info{})
	require.NoError(t, imp.Import(Item{Source: "synthetic", InfoHash: hash, Name: "Observed handbook"}))
	imp.Drain()
	require.NoError(t, imp.Err())
	record, err := ledger.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, []string{"full/private/handbook.pdf"}, record.Evidence.Files)
	require.NoError(t, ledger.RetainEvidence(t.Context(), id, moderation.Evidence{
		Files: []string{"full/private/handbook.pdf", "uncapped/second.pdf"},
	}))
	require.NoError(t, imp.Import(Item{Source: "synthetic", InfoHash: hash, Name: "Observed handbook"}))
	imp.Drain()
	require.NoError(t, imp.Close())
	_, err = db.Query.Torrent.WithContext(t.Context()).Where(db.Query.Torrent.InfoHash.Eq(hash)).Delete()
	require.NoError(t, err)
	record, err = ledger.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, "Observed handbook", record.Evidence.Name)
	require.Equal(t, []string{"full/private/handbook.pdf", "uncapped/second.pdf"}, record.Evidence.Files)
}
