package moderation_test

import (
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/stretchr/testify/require"
)

func TestFlagRetainsPrivateEvidenceIdempotently(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	service := moderation.NewService(db.Gorm)
	id := moderation.IdentifierForInfoHash(protocol.MustParseID("000102030405060708090a0b0c0d0e0f10111213"))
	evidence := moderation.Evidence{
		Name:    "Harmless documentary",
		Files:   []string{"Harmless documentary/part 1/video.mkv"},
		Rule:    "synthetic-rule",
		Matches: []string{"documentary"},
	}
	require.NoError(t, service.Flag(t.Context(), id, evidence, "moderator-1"))
	require.NoError(t, service.Flag(t.Context(), id, evidence, "moderator-1"))
	record, err := service.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, moderation.Pending, record.Status)
	require.Equal(t, moderation.Moderator, record.Origin)
	require.Equal(t, &evidence, record.Evidence)
	history, err := service.History(t.Context(), id, moderation.Page{})
	require.NoError(t, err)
	require.Len(t, history.Items, 1)
	require.Equal(t, "flag", history.Items[0].Action)
	require.Equal(t, moderation.Pending, history.Items[0].Status)
}

func TestLocalAllowSurvivesAutomaticAndImportedBans(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	service := moderation.NewService(db.Gorm)
	id := moderation.IdentifierForInfoHash(protocol.ID{1})
	require.NoError(t, service.Flag(t.Context(), id, moderation.Evidence{Name: "Harmless archive"}, "reviewer"))
	require.NoError(t, service.Allow(t.Context(), id, "reviewer"))
	require.NoError(
		t,
		service.ApplyAutomatic(t.Context(), id, moderation.Banned, moderation.Evidence{Rule: "new-rule"}),
	)
	require.NoError(t, service.ApplyImport(t.Context(), id, moderation.Moderator, "trusted-file"))
	record, err := service.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, moderation.Allowed, record.Status)
	require.Equal(t, moderation.Moderator, record.Origin)
	require.Equal(t, "Harmless archive", record.Evidence.Name)
	history, err := service.History(t.Context(), id, moderation.Page{})
	require.NoError(t, err)
	require.Len(t, history.Items, 2)
	require.Equal(t, "allow", history.Items[1].Action)
}

func TestReopenIsExplicitAndBansKeepTheirOrigin(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	service := moderation.NewService(db.Gorm)
	id := moderation.IdentifierForInfoHash(protocol.ID{2})
	require.NoError(t, service.Allow(t.Context(), id, "reviewer"))
	require.NoError(t, service.Flag(t.Context(), id, moderation.Evidence{Name: "new metadata"}, "reviewer"))
	record, err := service.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, moderation.Allowed, record.Status)
	require.NoError(t, service.Reopen(t.Context(), id, "reviewer"))
	record, err = service.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, moderation.Pending, record.Status)
	require.NoError(t, service.Ban(t.Context(), id, "reviewer"))
	require.NoError(
		t,
		service.ApplyAutomatic(t.Context(), id, moderation.Pending, moderation.Evidence{Rule: "match"}),
	)
	require.NoError(t, service.ApplyImport(t.Context(), id, moderation.Automatic, "another-list"))
	record, err = service.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, moderation.Banned, record.Status)
	require.Equal(t, moderation.Moderator, record.Origin)
	require.Empty(t, record.ImportedOrigin)
	history, err := service.History(t.Context(), id, moderation.Page{})
	require.NoError(t, err)
	require.Len(t, history.Items, 3)
	require.Equal(t, "ban", history.Items[2].Action)
	require.ErrorIs(
		t,
		service.Reopen(t.Context(), moderation.IdentifierForInfoHash(protocol.ID{9}), "reviewer"),
		moderation.ErrNotFound,
	)
}

func TestCatalogueDeletionAndEvidencePurgeKeepBan(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	service := moderation.NewService(db.Gorm)
	hash := protocol.ID{3}
	id := moderation.IdentifierForInfoHash(hash)
	torrent := model.Torrent{
		InfoHash:    hash,
		Name:        "Harmless archive",
		Size:        2048,
		FilesStatus: model.FilesStatusNoInfo,
	}
	require.NoError(t, db.Gorm.Create(&torrent).Error)
	evidence := moderation.Evidence{
		InfoHash: &hash,
		Name:     torrent.Name,
		Files:    []string{"collection/full/path/video.mkv"},
		Rule:     "synthetic-rule",
		Matches:  []string{"archive"},
	}
	require.NoError(t, service.Flag(t.Context(), id, evidence, "private-reviewer"))
	require.NoError(t, service.Ban(t.Context(), id, "private-reviewer"))
	require.NoError(t, db.Gorm.Delete(&torrent).Error)
	record, err := service.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, &evidence, record.Evidence)
	require.NoError(t, service.PurgeEvidence(t.Context(), id))
	record, err = service.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, moderation.Banned, record.Status)
	require.Equal(t, moderation.Moderator, record.Origin)
	require.Nil(t, record.Evidence)
	history, err := service.History(t.Context(), id, moderation.Page{})
	require.NoError(t, err)
	require.Len(t, history.Items, 1)
	require.Equal(t, "purge", history.Items[0].Action)
	require.Empty(t, history.Items[0].Actor)
	require.Empty(t, history.Items[0].Source)
}

func TestSearchAllowedRecordsAndPaginatePrivateHistory(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	service := moderation.NewService(db.Gorm)

	for i := byte(1); i <= 4; i++ {
		id := moderation.IdentifierForInfoHash(protocol.ID{i})
		require.NoError(
			t,
			service.Flag(
				t.Context(),
				id,
				moderation.Evidence{
					Name:  "Harmless archive",
					Files: []string{"collection/full/folder/video.mkv"},
				},
				"reviewer",
			),
		)
		require.NoError(t, service.Allow(t.Context(), id, "reviewer"))
	}

	request := moderation.SearchRequest{
		Status: moderation.Allowed,
		Query:  "full/folder",
		Page:   moderation.Page{Limit: 2},
	}
	first, err := service.Search(t.Context(), request)
	require.NoError(t, err)
	require.Len(t, first.Items, 2)
	require.NotEmpty(t, first.Next)
	request.Page.After = first.Next
	second, err := service.Search(t.Context(), request)
	require.NoError(t, err)
	require.Len(t, second.Items, 2)
	require.Empty(t, second.Next)
	require.NotEqual(t, first.Items[1].Identifier, second.Items[0].Identifier)
	require.Equal(t, "Harmless archive", first.Items[0].Name)

	request.Query = "%"
	request.Page.After = ""
	literal, err := service.Search(t.Context(), request)
	require.NoError(t, err)
	require.Empty(t, literal.Items)
	firstHistory, err := service.History(t.Context(), first.Items[0].Identifier, moderation.Page{Limit: 1})
	require.NoError(t, err)
	require.Len(t, firstHistory.Items, 1)
	require.NotEmpty(t, firstHistory.Next)
	nextHistory, err := service.History(
		t.Context(),
		first.Items[0].Identifier,
		moderation.Page{Limit: 1, After: firstHistory.Next},
	)
	require.NoError(t, err)
	require.Len(t, nextHistory.Items, 1)
	require.Equal(t, "allow", nextHistory.Items[0].Action)
	require.Empty(t, nextHistory.Next)
	_, err = service.Search(t.Context(), moderation.SearchRequest{Page: moderation.Page{Limit: 101}})
	require.ErrorIs(t, err, moderation.ErrInvalidPage)
	_, err = service.History(t.Context(), first.Items[0].Identifier, moderation.Page{After: "oops"})
	require.ErrorIs(t, err, moderation.ErrInvalidPage)
}

func TestImportedBanSupportsMissingMetadataAndLaterEvidence(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	service := moderation.NewService(db.Gorm)
	id := moderation.IdentifierForInfoHash(protocol.ID{5})
	require.NoError(t, service.ApplyImport(t.Context(), id, moderation.Automatic, "trusted-file"))
	require.NoError(t, service.ApplyImport(t.Context(), id, moderation.Automatic, "trusted-file"))
	record, err := service.Get(t.Context(), id)
	require.NoError(t, err)
	require.Nil(t, record.Evidence)
	require.Equal(t, moderation.Banned, record.Status)
	require.Equal(t, moderation.Imported, record.Origin)
	require.Equal(t, moderation.Automatic, record.ImportedOrigin)
	require.Equal(t, "trusted-file", record.Source)

	evidence := moderation.Evidence{
		Name:  "Synthetic archive",
		Files: []string{"collection/full/video.mkv"},
		Rule:  "synthetic-rule",
	}
	require.NoError(t, service.RetainEvidence(t.Context(), id, evidence))
	require.NoError(
		t,
		service.ApplyAutomatic(t.Context(), id, moderation.Pending, moderation.Evidence{Rule: "another-rule"}),
	)
	record, err = service.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, &evidence, record.Evidence)
	require.Equal(t, moderation.Banned, record.Status)
	require.Equal(t, moderation.Imported, record.Origin)
	history, err := service.History(t.Context(), id, moderation.Page{})
	require.NoError(t, err)
	require.Len(t, history.Items, 2)
	require.Equal(t, moderation.Automatic, history.Items[0].ImportedOrigin)
	// An import promotes an existing pending review and retains its original metadata.
	pending := moderation.IdentifierForInfoHash(protocol.ID{6})
	require.NoError(t, service.Flag(t.Context(), pending, evidence, "reviewer"))
	require.NoError(t, service.ApplyImport(t.Context(), pending, moderation.Moderator, "another-file"))
	record, err = service.Get(t.Context(), pending)
	require.NoError(t, err)
	require.Equal(t, moderation.Banned, record.Status)
	require.Equal(t, &evidence, record.Evidence)
	require.NoError(t, service.Allow(t.Context(), pending, "reviewer"))
	require.NoError(t, service.RetainEvidence(t.Context(), pending, moderation.Evidence{Rule: "new-rule"}))
	record, err = service.Get(t.Context(), pending)
	require.NoError(t, err)
	require.Equal(t, &evidence, record.Evidence)
}

func TestConcurrentAllowAndImportAlwaysCommitLocalAllow(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	service := moderation.NewService(db.Gorm)

	for i := byte(10); i < 34; i++ {
		id := moderation.IdentifierForInfoHash(protocol.ID{i})
		start := make(chan struct{})
		results := make(chan error, 2)

		go func() { <-start; results <- service.Allow(t.Context(), id, "reviewer") }()
		go func() { <-start; results <- service.ApplyImport(t.Context(), id, moderation.Moderator, "trusted-file") }()

		close(start)
		require.NoError(t, <-results)
		require.NoError(t, <-results)
		record, err := service.Get(t.Context(), id)
		require.NoError(t, err)
		require.Equal(t, moderation.Allowed, record.Status)
		require.Equal(t, moderation.Moderator, record.Origin)
		history, err := service.History(t.Context(), id, moderation.Page{})
		require.NoError(t, err)
		require.NotEmpty(t, history.Items)
		require.Equal(t, "allow", history.Items[len(history.Items)-1].Action)
		require.Equal(t, record.Status, history.Items[len(history.Items)-1].Status)
	}
}

func TestFailedHistoryWriteRollsBackDecisionAndEvidence(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	service := moderation.NewService(db.Gorm)
	id := moderation.IdentifierForInfoHash(protocol.ID{40})
	evidence := moderation.Evidence{Name: "Harmless original", Files: []string{"folder/original.mkv"}}
	require.NoError(t, service.Flag(t.Context(), id, evidence, "reviewer"))
	// Simulate a storage failure at the database boundary, after the state/evidence writes.
	require.NoError(t, db.Gorm.Exec(`CREATE FUNCTION fail_history() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN RAISE EXCEPTION 'synthetic history storage failure'; END $$;
 CREATE TRIGGER fail_history BEFORE INSERT ON moderation_history FOR EACH ROW EXECUTE FUNCTION fail_history();`).Error)
	require.Error(t, service.Ban(t.Context(), id, "reviewer"))
	require.Error(t, service.RetainEvidence(t.Context(), id, moderation.Evidence{Name: "Replacement metadata"}))
	require.Error(t, service.PurgeEvidence(t.Context(), id))
	record, err := service.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, moderation.Pending, record.Status)
	require.Equal(t, &evidence, record.Evidence)
	history, err := service.History(t.Context(), id, moderation.Page{})
	require.NoError(t, err)
	require.Len(t, history.Items, 1)

	unseen := moderation.IdentifierForInfoHash(protocol.ID{41})
	require.Error(t, service.Flag(t.Context(), unseen, evidence, "reviewer"))
	_, err = service.Get(t.Context(), unseen)
	require.ErrorIs(t, err, moderation.ErrNotFound)
}

func TestAutomaticBanEscalatesPendingWithoutDiscardingMetadata(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	service := moderation.NewService(db.Gorm)
	id := moderation.IdentifierForInfoHash(protocol.ID{42})
	evidence := moderation.Evidence{
		Name:  "Harmless title",
		Files: []string{"full/folder/video.mkv"},
		Rule:  "old-rule",
	}
	require.NoError(t, service.ApplyAutomatic(t.Context(), id, moderation.Pending, evidence))
	require.NoError(
		t,
		service.ApplyAutomatic(t.Context(), id, moderation.Banned, moderation.Evidence{Rule: "new-rule"}),
	)
	record, err := service.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, moderation.Banned, record.Status)
	require.Equal(t, moderation.Automatic, record.Origin)
	require.Equal(t, evidence.Name, record.Evidence.Name)
	require.Equal(t, evidence.Files, record.Evidence.Files)
	require.Equal(t, "new-rule", record.Evidence.Rule)
	require.NoError(t, service.ApplyAutomatic(t.Context(), id, moderation.Pending, evidence))
	record, err = service.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, moderation.Banned, record.Status)
	require.ErrorIs(
		t,
		service.ApplyAutomatic(t.Context(), id, moderation.Allowed, evidence),
		moderation.ErrInvalidTransition,
	)
	require.ErrorIs(
		t,
		service.ApplyImport(t.Context(), id, moderation.Imported, "file"),
		moderation.ErrInvalidTransition,
	)
}

func TestEvidenceCannotDescribeAnotherIdentifier(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	service := moderation.NewService(db.Gorm)
	id := moderation.IdentifierForInfoHash(protocol.ID{50})
	other := protocol.ID{51}
	evidence := moderation.Evidence{InfoHash: &other, Name: "Mismatched fixture"}
	require.ErrorIs(t, service.Flag(t.Context(), id, evidence, "reviewer"), moderation.ErrInvalidEvidence)
	_, err := service.Get(t.Context(), id)
	require.ErrorIs(t, err, moderation.ErrNotFound)
	require.NoError(t, service.ApplyImport(t.Context(), id, moderation.Moderator, "file"))
	require.ErrorIs(t, service.RetainEvidence(t.Context(), id, evidence), moderation.ErrInvalidEvidence)
	record, err := service.Get(t.Context(), id)
	require.NoError(t, err)
	require.Nil(t, record.Evidence)
}

func TestImportRetainsExistingCatalogueEvidenceAfterDeletion(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	service := moderation.NewService(db.Gorm)
	hash := protocol.ID{0x29, 0x09}
	require.NoError(
		t,
		db.Gorm.Create(
			&model.Torrent{
				InfoHash:    hash,
				Name:        "Harmless existing handbook",
				Size:        42,
				FilesStatus: model.FilesStatusMulti,
			},
		).Error,
	)
	require.NoError(
		t,
		db.Gorm.Create(
			&model.TorrentFile{InfoHash: hash, Index: 0, Path: "complete/private/harmless.txt", Size: 42},
		).Error,
	)
	id := moderation.IdentifierForInfoHash(hash)
	require.NoError(t, service.ApplyImport(t.Context(), id, moderation.Automatic, "Synthetic source"))
	record, err := service.Get(t.Context(), id)
	require.NoError(t, err)
	require.NotNil(t, record.Evidence, "import must retain existing catalogue metadata independently")
	require.Equal(t, "Harmless existing handbook", record.Evidence.Name)
	require.Equal(t, []string{"complete/private/harmless.txt"}, record.Evidence.Files)
	require.Equal(t, &hash, record.Evidence.InfoHash)
	require.NoError(t, db.Gorm.Where("info_hash = ?", hash).Delete(&model.Torrent{}).Error)
	retained, err := service.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, record.Evidence, retained.Evidence)
	require.NoError(t, service.PurgeEvidence(t.Context(), id))
	require.NoError(t, service.ApplyImport(t.Context(), id, moderation.Moderator, "Second source"))
	purged, err := service.Get(t.Context(), id)
	require.NoError(t, err)
	require.Nil(t, purged.Evidence, "duplicate imports must not reconstruct explicitly purged evidence")
	require.Equal(t, moderation.Automatic, purged.ImportedOrigin)
	require.Equal(t, "Synthetic source", purged.Source)
}
