package enhancement

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestDurableAcceptedMetadataSurvivesFailedAttempt(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	hash := protocol.ID{42}
	require.NoError(
		t,
		db.Gorm.Create(
			&model.Torrent{
				InfoHash:    hash,
				Name:        "opaque.release",
				FilesStatus: model.FilesStatusNoInfo,
			},
		).Error,
	)
	require.NoError(t, db.Gorm.Create(&model.TorrentContent{InfoHash: hash}).Error)

	var torrent model.Torrent
	require.NoError(t, db.Gorm.First(&torrent).Error)

	tc0 := model.TorrentContent{InfoHash: hash}

	require.NoError(
		t,
		db.Gorm.Transaction(
			func(tx *gorm.DB) error { return RecordOrdinary(tx, torrent, &tc0, "test", false) },
		),
	)
	service := NewApplication(db.Gorm, nil, nil, NewDefaultConfig())
	run, err := service.Prepare(t.Context(), hash, false, false)
	require.NoError(t, err)
	require.NoError(
		t,
		service.Complete(
			t.Context(),
			run,
			Evaluation{
				Outcome: OutcomeAccepted,
				Result:  &Metadata{Title: "Hidden Orchard", Year: 1998},
			},
			nil,
		),
	)

	var tc model.TorrentContent
	require.NoError(t, db.Gorm.Where("info_hash = ?", hash).First(&tc).Error)
	require.Equal(t, "Hidden Orchard (1998)", tc.Title())
	next, err := service.Prepare(t.Context(), hash, true, false)
	require.NoError(t, err)
	require.Equal(
		t,
		run.InputFingerprint,
		next.InputFingerprint,
		"own output must not change input",
	)
	require.NoError(
		t,
		service.Complete(t.Context(), next, Evaluation{Outcome: OutcomeProviderFailure}, nil),
	)
	require.NoError(t, db.Gorm.Where("info_hash = ?", hash).First(&tc).Error)
	require.Equal(t, "Hidden Orchard (1998)", tc.Title())
}

func applicationFixture(t *testing.T) (*dbtest.DB, *Application, protocol.ID) {
	t.Helper()
	db := dbtest.New(t)
	hash := protocol.ID{42}
	torrent := model.Torrent{
		InfoHash:    hash,
		Name:        "opaque.release",
		FilesStatus: model.FilesStatusSingle,
	}
	require.NoError(t, db.Gorm.Create(&torrent).Error)

	tc := model.TorrentContent{InfoHash: hash}
	require.NoError(t, db.Gorm.Create(&tc).Error)
	require.NoError(
		t,
		db.Gorm.Transaction(
			func(tx *gorm.DB) error { return RecordOrdinary(tx, torrent, &tc, "test", false) },
		),
	)

	return db, NewApplication(db.Gorm, nil, nil, NewDefaultConfig()), hash
}

func TestApplicationRejectsStaleConcurrentCanceledAndHiddenResults(t *testing.T) {
	t.Parallel()

	for _, scenario := range []string{
		"newer_run", "evidence_change", "hint_change",
		"catalogue_change", "cancellation", "moderation",
	} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			db, app, hash := applicationFixture(t)
			old, err := app.Prepare(t.Context(), hash, false, false)
			require.NoError(t, err)
			ctx := t.Context()

			switch scenario {
			case "newer_run":
				newer, err := app.Prepare(ctx, hash, true, false)
				require.NoError(t, err)
				require.NoError(
					t,
					app.Complete(
						ctx,
						newer,
						Evaluation{
							Outcome: OutcomeAccepted,
							Result:  &Metadata{Title: "New answer"},
						},
						nil,
					),
				)
			case "evidence_change":
				require.NoError(
					t,
					db.Gorm.Model(&model.Torrent{}).
						Where("info_hash = ?", hash).
						Update("name", "different").
						Error,
				)
			case "hint_change":
				require.NoError(
					t,
					db.Gorm.Create(
						&model.TorrentHint{
							InfoHash:    hash,
							ContentType: model.ContentTypeMovie,
							Title:       model.NewNullString("Authoritative"),
						},
					).Error,
				)
			case "catalogue_change":
				c := model.Content{
					Type:   model.ContentTypeMovie,
					Source: model.SourceTmdb,
					ID:     "123",
					Title:  "Catalog",
				}
				require.NoError(t, db.Gorm.Create(&c).Error)
				require.NoError(
					t,
					db.Gorm.Model(&model.TorrentContent{}).
						Where("info_hash = ?", hash).
						Updates(map[string]any{"content_type": "movie", "content_source": "tmdb", "content_id": "123"}).
						Error,
				)
			case "cancellation":
				var cancel context.CancelFunc

				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "moderation":
				require.NoError(
					t,
					moderation.NewService(db.Gorm).
						Flag(ctx, moderation.IdentifierForInfoHash(hash),
							moderation.Evidence{InfoHash: &hash, Name: "harmless"}, "test"),
				)
			}

			require.Error(
				t,
				app.Complete(
					ctx,
					old,
					Evaluation{
						Outcome: OutcomeAccepted,
						Result: &Metadata{
							Title: "Late answer",
							Tags:  []string{tagEditionDeluxe},
						},
					},
					nil,
				),
			)

			var count int64
			require.NoError(
				t,
				db.Gorm.Model(&model.TorrentContent{}).
					Where("info_hash = ? AND inferred_title = ?", hash, "Late answer").
					Count(&count).
					Error,
			)
			require.Zero(t, count)
			require.NoError(
				t,
				db.Gorm.Model(&model.TorrentTag{}).Where("info_hash = ?", hash).Count(&count).Error,
			)
			require.Zero(t, count)
		})
	}
}

func TestCompletedUnresolvedAndLimitedSuppressRepeatButFailuresDoNot(t *testing.T) {
	t.Parallel()

	for _, outcome := range []string{OutcomeUnresolved, OutcomeLimited, OutcomeProviderFailure, OutcomeMalformed} {
		t.Run(outcome, func(t *testing.T) {
			t.Parallel()
			_, app, hash := applicationFixture(t)
			run, err := app.Prepare(t.Context(), hash, false, false)
			require.NoError(t, err)
			require.NoError(t, app.Complete(t.Context(), run, Evaluation{Outcome: outcome}, nil))

			_, err = app.Prepare(t.Context(), hash, false, false)
			if outcome == OutcomeUnresolved || outcome == OutcomeLimited {
				require.ErrorIs(t, err, ErrEvaluated)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestGeneratedTagsReplaceAtomicallyAndKeepUserTags(t *testing.T) {
	t.Parallel()
	db, app, hash := applicationFixture(t)
	require.NoError(t, db.Gorm.Create(&model.TorrentTag{InfoHash: hash, Name: "user-kept"}).Error)

	for _, tag := range []string{tagEditionDeluxe, tagPlatformLinux} {
		if tag == tagPlatformLinux {
			app.config.Tags = []string{tagPlatformLinux}
		}

		run, err := app.Prepare(t.Context(), hash, true, false)
		require.NoError(t, err)
		require.NoError(
			t,
			app.Complete(
				t.Context(),
				run,
				Evaluation{Outcome: OutcomeAccepted, Result: &Metadata{Tags: []string{tag}}},
				nil,
			),
		)

		var tags []string
		require.NoError(
			t,
			db.Gorm.Model(&model.TorrentTag{}).
				Where("info_hash = ?", hash).
				Pluck("name", &tags).
				Error,
		)
		require.ElementsMatch(t, []string{"user-kept", tag}, tags)
	}
}

func TestImporterHintsProtectFieldsUnlessExplicitRematch(t *testing.T) {
	t.Parallel()
	db, app, hash := applicationFixture(t)
	h := model.TorrentHint{
		InfoHash:    hash,
		ContentType: model.ContentTypeMovie,
		Title:       model.NewNullString("Supplied title"),
		ReleaseYear: 1984,
		Languages:   model.Languages{model.Language("fr"): {}},
		Episodes:    model.Episodes{1: {2: {}}},
	}
	require.NoError(t, db.Gorm.Create(&h).Error)

	var torrent model.Torrent
	require.NoError(t, db.Gorm.Preload("Hint").Where("info_hash = ?", hash).First(&torrent).Error)
	baseline := model.TorrentContent{
		InfoHash:      hash,
		ContentType:   model.NewNullContentType(model.ContentTypeMovie),
		InferredTitle: "Supplied title",
		InferredYear:  1984,
		Languages:     h.Languages,
		Episodes:      h.Episodes,
	}
	require.NoError(
		t,
		db.Gorm.Model(&model.TorrentContent{}).
			Where("info_hash = ?", hash).
			Updates(map[string]any{"content_type": "movie", "inferred_title": "Supplied title", "inferred_year": 1984}).
			Error,
	)
	require.NoError(
		t,
		db.Gorm.Transaction(
			func(tx *gorm.DB) error { return RecordOrdinary(tx, torrent, &baseline, "test", false) },
		),
	)

	for _, rematch := range []bool{false, true} {
		run, err := app.Prepare(t.Context(), hash, true, rematch)
		require.NoError(t, err)
		require.NoError(
			t,
			app.Complete(
				t.Context(),
				run,
				Evaluation{
					Outcome: OutcomeAccepted,
					Result: &Metadata{
						Type:      "tv_show",
						Title:     "Enhanced title",
						Year:      2002,
						Languages: []string{"en"},
						Episodes:  map[int][]int{2: {5}},
					},
				},
				nil,
			),
		)

		var got model.TorrentContent
		require.NoError(t, db.Gorm.Where("info_hash = ?", hash).First(&got).Error)

		if rematch {
			require.Equal(t, "Enhanced title (2002) S02E05", got.Title())
			require.Equal(t, model.ContentTypeTvShow, got.ContentType.ContentType)
		} else {
			require.Equal(t, "Supplied title (1984) S01E02", got.Title())
			require.Equal(t, model.ContentTypeMovie, got.ContentType.ContentType)
			require.Equal(t, h.Languages, got.Languages)
		}
	}

	var untouched model.TorrentHint
	require.NoError(t, db.Gorm.Where("info_hash = ?", hash).First(&untouched).Error)
	require.Equal(t, "Supplied title", untouched.Title.String)
	require.Equal(t, model.Year(1984), untouched.ReleaseYear)
}

type evaluationFunc func(context.Context, Evidence) Evaluation

func (f evaluationFunc) Evaluate(ctx context.Context, e Evidence) Evaluation { return f(ctx, e) }

func TestDeadlineRemembersLimitedWithoutApplyingLateOutput(t *testing.T) {
	t.Parallel()
	db, app, hash := applicationFixture(t)
	first, err := app.Prepare(t.Context(), hash, false, false)
	require.NoError(t, err)
	require.NoError(
		t,
		app.Complete(
			t.Context(),
			first,
			Evaluation{Outcome: OutcomeAccepted, Result: &Metadata{Title: "Retained title"}},
			nil,
		),
	)
	app.config.Timeout = 5 * time.Second
	app.engine = evaluationFunc(func(ctx context.Context, _ Evidence) Evaluation {
		<-ctx.Done()
		return Evaluation{Outcome: OutcomeAccepted, Result: &Metadata{Title: "Late output"}}
	})
	result, err := app.Enhance(t.Context(), hash, true, false)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, OutcomeLimited, result.Outcome)

	var tc model.TorrentContent
	require.NoError(t, db.Gorm.Where("info_hash = ?", hash).First(&tc).Error)
	require.Equal(t, "Retained title", tc.Title())
	_, err = app.Prepare(t.Context(), hash, false, false)
	require.ErrorIs(t, err, ErrEvaluated)
}

func TestExistingCatalogueIdentityIsProtectedAndRematchCanReplaceIt(t *testing.T) {
	t.Parallel()
	db, app, hash := applicationFixture(t)
	oldContent := model.Content{
		Type:        model.ContentTypeMovie,
		Source:      model.SourceTmdb,
		ID:          "10",
		Title:       "Known work",
		ReleaseYear: 1984,
	}
	require.NoError(t, db.Gorm.Create(&oldContent).Error)
	baseline := model.TorrentContent{
		InfoHash:      hash,
		ContentType:   model.NewNullContentType(model.ContentTypeMovie),
		ContentSource: model.NewNullString(model.SourceTmdb),
		ContentID:     model.NewNullString("10"),
		Content:       oldContent,
	}
	require.NoError(
		t,
		db.Gorm.Model(&model.TorrentContent{}).
			Where("info_hash = ?", hash).
			Updates(map[string]any{"content_type": "movie", "content_source": "tmdb", "content_id": "10"}).
			Error,
	)

	var torrent model.Torrent
	require.NoError(t, db.Gorm.Where("info_hash = ?", hash).First(&torrent).Error)
	require.NoError(
		t,
		db.Gorm.Transaction(
			func(tx *gorm.DB) error { return RecordOrdinary(tx, torrent, &baseline, "test", false) },
		),
	)

	newContent := model.Content{
		Type:        model.ContentTypeTvShow,
		Source:      model.SourceTmdb,
		ID:          "20",
		Title:       "Other work",
		ReleaseYear: 2001,
	}

	for _, rematch := range []bool{false, true} {
		run, err := app.Prepare(t.Context(), hash, true, rematch)
		require.NoError(t, err)
		require.NoError(
			t,
			app.Complete(
				t.Context(),
				run,
				Evaluation{
					Outcome: OutcomeAccepted,
					Result: &Metadata{
						Type:     WorkSeries,
						Work:     &Work{Type: WorkSeries, ID: 20},
						Episodes: map[int][]int{2: {5}},
					},
				},
				&newContent,
			),
		)

		var got model.TorrentContent
		require.NoError(
			t,
			db.Gorm.Preload("Content").Where("info_hash = ?", hash).First(&got).Error,
		)

		if rematch {
			require.Equal(t, "20", got.ContentID.String)
			require.Equal(t, "Other work (2001) S02E05", got.Title())
		} else {
			require.Equal(t, "10", got.ContentID.String)
			require.Equal(t, "Known work (1984) S02E05", got.Title())
		}
	}
	// Re-selecting the original ordinary work has equal evidence, but explicit
	// ordinary rematch must still supersede the enhancement's later identity.
	restored := baseline

	require.NoError(
		t,
		db.Gorm.Transaction(
			func(tx *gorm.DB) error { return RecordOrdinary(tx, torrent, &restored, "test", true) },
		),
	)
	require.Equal(t, "10", restored.ContentID.String)
	require.True(t, restored.Episodes.HasEpisode(2, 5), "valid release fields survive explicit ordinary rematch")
	require.NoError(t, db.Gorm.Where("info_hash = ?", hash).Delete(&model.TorrentContent{}).Error)

	replayed := baseline

	require.NoError(
		t,
		db.Gorm.Transaction(
			func(tx *gorm.DB) error { return RecordOrdinary(tx, torrent, &replayed, "test", false) },
		),
	)
	require.Equal(
		t,
		"10",
		replayed.ContentID.String,
		"normal replay cannot resurrect the superseded enhancement identity",
	)
}

func TestRematchCanDetachCatalogueIdentityWhenTypeChanges(t *testing.T) {
	t.Parallel()
	db, app, hash := applicationFixture(t)
	content := model.Content{
		Type:   model.ContentTypeMovie,
		Source: model.SourceTmdb,
		ID:     "10",
		Title:  "Known movie",
	}
	require.NoError(t, db.Gorm.Create(&content).Error)
	baseline := model.TorrentContent{
		InfoHash:      hash,
		ContentType:   model.NewNullContentType(model.ContentTypeMovie),
		ContentSource: model.NewNullString(model.SourceTmdb),
		ContentID:     model.NewNullString("10"),
		Content:       content,
	}
	require.NoError(
		t,
		db.Gorm.Model(&model.TorrentContent{}).
			Where("info_hash = ?", hash).
			Updates(map[string]any{"content_type": "movie", "content_source": "tmdb", "content_id": "10"}).
			Error,
	)

	var torrent model.Torrent
	require.NoError(t, db.Gorm.Where("info_hash = ?", hash).First(&torrent).Error)
	require.NoError(
		t,
		db.Gorm.Transaction(
			func(tx *gorm.DB) error { return RecordOrdinary(tx, torrent, &baseline, "test", false) },
		),
	)
	run, err := app.Prepare(t.Context(), hash, true, true)
	require.NoError(t, err)
	require.NoError(
		t,
		app.Complete(
			t.Context(),
			run,
			Evaluation{
				Outcome: OutcomeAccepted,
				Result:  &Metadata{Type: "game", Title: "Portable Game"},
			},
			nil,
		),
	)

	var got model.TorrentContent
	require.NoError(t, db.Gorm.Where("info_hash = ?", hash).First(&got).Error)
	require.False(t, got.ContentID.Valid)
	require.Equal(t, "Portable Game", got.Title())
	require.NoError(t, db.Gorm.Where("info_hash = ?", hash).Delete(&model.TorrentContent{}).Error)

	replayed := baseline

	require.NoError(
		t,
		db.Gorm.Transaction(
			func(tx *gorm.DB) error { return RecordOrdinary(tx, torrent, &replayed, "test", false) },
		),
	)
	require.False(t, replayed.ContentID.Valid)
	require.Equal(t, model.ContentTypeGame, replayed.ContentType.ContentType)
	require.Equal(t, "Portable Game", replayed.Title())
}

func TestTypeOnlyHintAllowsCompatibleMissingIdentity(t *testing.T) {
	t.Parallel()

	for _, scenario := range []string{"compatible", "conflicting_type", "supplied_identity", "explicit_rematch"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			db, app, hash := applicationFixture(t)
			hint := model.TorrentHint{InfoHash: hash, ContentType: model.ContentTypeMovie}

			baseline := model.TorrentContent{
				InfoHash:    hash,
				ContentType: model.NewNullContentType(model.ContentTypeMovie),
			}
			if scenario == "supplied_identity" {
				original := model.Content{
					Type:   model.ContentTypeMovie,
					Source: model.SourceTmdb,
					ID:     "10",
					Title:  "Supplied work",
				}
				require.NoError(t, db.Gorm.Create(&original).Error)

				hint.ContentSource = model.NewNullString(model.SourceTmdb)
				hint.ContentID = model.NewNullString("10")
				baseline.ContentSource = hint.ContentSource
				baseline.ContentID = hint.ContentID
				baseline.Content = original
			}

			require.NoError(t, db.Gorm.Create(&hint).Error)
			require.NoError(
				t,
				db.Gorm.Where("info_hash = ?", hash).Delete(&model.TorrentContent{}).Error,
			)
			require.NoError(t, db.Gorm.Omit(clause.Associations).Create(&baseline).Error)

			var torrent model.Torrent
			require.NoError(
				t,
				db.Gorm.Preload("Hint").Where("info_hash = ?", hash).First(&torrent).Error,
			)
			require.NoError(
				t,
				db.Gorm.Transaction(
					func(tx *gorm.DB) error { return RecordOrdinary(tx, torrent, &baseline, "test", false) },
				),
			)

			selectedType := model.ContentTypeMovie
			if scenario == "conflicting_type" || scenario == "explicit_rematch" {
				selectedType = model.ContentTypeTvShow
			}

			selected := model.Content{
				Type:   selectedType,
				Source: model.SourceTmdb,
				ID:     "20",
				Title:  "Retrieved work",
			}
			run, err := app.Prepare(t.Context(), hash, true, scenario == "explicit_rematch")
			require.NoError(t, err)
			require.NoError(
				t,
				app.Complete(
					t.Context(),
					run,
					Evaluation{
						Outcome: OutcomeAccepted,
						Result: &Metadata{
							Type: selectedType.String(),
							Work: &Work{Type: selectedType.String(), ID: 20},
						},
					},
					&selected,
				),
			)

			var got model.TorrentContent
			require.NoError(t, db.Gorm.Where("info_hash = ?", hash).First(&got).Error)

			switch scenario {
			case "compatible", "explicit_rematch":
				require.Equal(t, "20", got.ContentID.String)
				require.Equal(t, selectedType, got.ContentType.ContentType)
			case "conflicting_type":
				require.False(t, got.ContentID.Valid)
				require.Equal(t, model.ContentTypeMovie, got.ContentType.ContentType)
			case "supplied_identity":
				require.Equal(t, "10", got.ContentID.String)
			}

			var unchanged model.TorrentHint
			require.NoError(t, db.Gorm.Where("info_hash = ?", hash).First(&unchanged).Error)
			require.Equal(t, hint.ContentType, unchanged.ContentType)
			require.Equal(t, hint.ContentID, unchanged.ContentID)
		})
	}
}

func TestCompletionLockWaitHonorsRunDeadlineAndCancellation(t *testing.T) {
	t.Parallel()

	for _, scenario := range []string{"deadline", "cancellation"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			db, app, hash := applicationFixture(t)
			initial, err := app.Prepare(t.Context(), hash, false, false)
			require.NoError(t, err)
			require.NoError(
				t,
				app.Complete(
					t.Context(),
					initial,
					Evaluation{
						Outcome: OutcomeAccepted,
						Result:  &Metadata{Title: "Retained title"},
					},
					nil,
				),
			)

			var accepted model.ClassificationEvaluation
			require.NoError(
				t,
				db.Gorm.Where("info_hash = ? AND stage = 'enhancement'", hash).
					First(&accepted).
					Error,
			)

			app.config.Timeout = 5 * time.Second

			runCtx, cancel := context.WithCancel(t.Context())
			defer cancel()

			run, err := app.Prepare(runCtx, hash, true, false)
			require.NoError(t, err)
			blocker := db.Gorm.WithContext(t.Context()).Begin()
			require.NoError(t, blocker.Error)
			t.Cleanup(func() { _ = blocker.Rollback().Error })
			require.NoError(t, LockTorrent(blocker, hash))

			done := make(chan error, 1)
			go func() {
				done <- app.Complete(
					context.WithoutCancel(t.Context()), run,
					Evaluation{Outcome: OutcomeAccepted, Result: &Metadata{Title: "Late title"}}, nil,
				)
			}()

			require.Eventually(t, func() bool {
				var waiting int64

				err := db.Gorm.Raw(`SELECT count(*) FROM pg_stat_activity WHERE datname = ?
  AND wait_event_type = 'Lock' AND wait_event = 'advisory'`, db.Name).Scan(&waiting).Error

				return err == nil && waiting > 0
			},
				2*time.Second, 10*time.Millisecond,
				"completion must have entered its transaction and be waiting on the lock",
			)

			expected := context.DeadlineExceeded

			if scenario == "cancellation" {
				cancel()

				expected = context.Canceled
			} else {
				<-time.After(app.config.Timeout + 50*time.Millisecond)
			}

			require.NoError(t, blocker.Rollback().Error)

			select {
			case err := <-done:
				require.ErrorIs(t, err, expected)
			case <-time.After(2 * time.Second):
				t.Fatal("completion did not stop after its run expired or was cancelled")
			}

			var tc model.TorrentContent
			require.NoError(t, db.Gorm.Where("info_hash = ?", hash).First(&tc).Error)
			require.Equal(t, "Retained title", tc.Title())

			var after model.ClassificationEvaluation
			require.NoError(
				t,
				db.Gorm.Where("info_hash = ? AND stage = 'enhancement'", hash).First(&after).Error,
			)
			require.Equal(t, accepted.AcceptedOutput, after.AcceptedOutput)
			require.Equal(t, accepted.CompletedAt, after.CompletedAt)
		})
	}
}

func TestEnhancementCatalogueAuthoritySurvivesReevaluationAndReplay(t *testing.T) {
	t.Parallel()

	for _, fieldsOnly := range []bool{false, true} {
		t.Run(fmt.Sprint("fieldsOnly=", fieldsOnly), func(t *testing.T) {
			t.Parallel()
			db, app, hash := applicationFixture(t)
			a := model.Content{
				Type:        model.ContentTypeMovie,
				Source:      model.SourceTmdb,
				ID:          "10",
				Title:       "First work",
				ReleaseYear: 1984,
			}
			b := model.Content{
				Type:        model.ContentTypeMovie,
				Source:      model.SourceTmdb,
				ID:          "20",
				Title:       "Other work",
				ReleaseYear: 2001,
			}
			first, err := app.Prepare(t.Context(), hash, false, false)
			require.NoError(t, err)
			require.NoError(
				t,
				app.Complete(
					t.Context(),
					first,
					Evaluation{
						Outcome: OutcomeAccepted,
						Result: &Metadata{
							Type: WorkMovie,
							Work: &Work{Type: WorkMovie, ID: 10},
						},
					},
					&a,
				),
			)
			next, err := app.Prepare(t.Context(), hash, true, false)
			require.NoError(t, err)
			require.Equal(t, first.Evidence, next.Evidence)

			metadata := &Metadata{Type: WorkMovie, Work: &Work{Type: WorkMovie, ID: 20}}
			details := &b

			if fieldsOnly {
				metadata = &Metadata{Title: "Release detail", Year: 1998}
				details = nil
			}

			require.NoError(
				t,
				app.Complete(
					t.Context(),
					next,
					Evaluation{Outcome: OutcomeAccepted, Result: metadata},
					details,
				),
			)

			var got model.TorrentContent
			require.NoError(t, db.Gorm.Where("info_hash = ?", hash).First(&got).Error)
			require.Equal(t, "10", got.ContentID.String, "force must preserve existing catalogue authority")
			// Search rows can be deleted independently of durable evaluation state.
			require.NoError(t, db.Gorm.Where("info_hash = ?", hash).Delete(&model.TorrentContent{}).Error)

			var torrent model.Torrent
			require.NoError(t, db.Gorm.Where("info_hash = ?", hash).First(&torrent).Error)
			baseline := model.TorrentContent{InfoHash: hash}

			require.NoError(
				t,
				db.Gorm.Transaction(
					func(tx *gorm.DB) error { return RecordOrdinary(tx, torrent, &baseline, "test", false) },
				),
			)
			require.Equal(
				t,
				"10",
				baseline.ContentID.String,
				"replay must retain authority without a search row",
			)
			require.NoError(t, db.Gorm.Omit(clause.Associations).Create(&baseline).Error)
			rematch, err := app.Prepare(t.Context(), hash, true, true)
			require.NoError(t, err)
			require.NoError(
				t,
				app.Complete(
					t.Context(),
					rematch,
					Evaluation{
						Outcome: OutcomeAccepted,
						Result: &Metadata{
							Type: WorkMovie,
							Work: &Work{Type: WorkMovie, ID: 20},
						},
					},
					&b,
				),
			)

			baseline = model.TorrentContent{InfoHash: hash}

			require.NoError(
				t,
				db.Gorm.Transaction(
					func(tx *gorm.DB) error { return RecordOrdinary(tx, torrent, &baseline, "test", false) },
				),
			)
			require.Equal(t, "20", baseline.ContentID.String)
		})
	}
}

func TestReadOnlyEvidenceUsesOrdinarySnapshotAfterEnhancement(t *testing.T) {
	t.Parallel()
	db, app, hash := applicationFixture(t)

	var torrent model.Torrent
	require.NoError(t, db.Gorm.Where("info_hash = ?", hash).First(&torrent).Error)
	require.NoError(
		t,
		db.Gorm.Create(
			&model.TorrentHint{
				InfoHash:     hash,
				ContentType:  model.ContentTypeTvShow,
				ReleaseGroup: model.NewNullString("supplied-group"),
			},
		).Error,
	)
	require.NoError(t, db.Gorm.Preload("Hint").Where("info_hash = ?", hash).First(&torrent).Error)
	// A large directory plus a small one exercises bounded representative selection.
	for i := range 25 {
		require.NoError(
			t,
			db.Gorm.Create(
				&model.TorrentFile{
					InfoHash: hash,
					Index:    uint(i),
					Path:     fmt.Sprintf("large/%02d.mkv", i),
					Size:     uint(1000 - i),
				},
			).Error,
		)
	}

	for i := range 2 {
		require.NoError(
			t,
			db.Gorm.Create(
				&model.TorrentFile{
					InfoHash: hash,
					Index:    uint(25 + i),
					Path:     fmt.Sprintf("small/%02d.mkv", i),
					Size:     uint(10 - i),
				},
			).Error,
		)
	}

	require.NoError(t, db.Gorm.Preload("Hint").Preload("Files").Where("info_hash = ?", hash).First(&torrent).Error)

	require.Len(t, torrent.Files, 27)

	baseline := model.TorrentContent{
		InfoHash:      hash,
		ContentType:   model.NewNullContentType(model.ContentTypeTvShow),
		InferredTitle: "Ordinary Orchard",
		InferredYear:  1984,
	}

	require.NoError(
		t,
		db.Gorm.Transaction(
			func(tx *gorm.DB) error { return RecordOrdinary(tx, torrent, &baseline, "test", false) },
		),
	)
	first, err := app.Prepare(t.Context(), hash, false, false)
	require.NoError(t, err)

	content := model.Content{
		Type:        model.ContentTypeTvShow,
		Source:      model.SourceTmdb,
		ID:          "10",
		Title:       "Enhanced series",
		ReleaseYear: 2001,
	}
	require.NoError(
		t,
		app.Complete(
			t.Context(),
			first,
			Evaluation{
				Outcome: OutcomeAccepted,
				Result: &Metadata{
					Type:      WorkSeries,
					Work:      &Work{Type: WorkSeries, ID: 10},
					Languages: []string{"en"},
					Episodes:  map[int][]int{2: {5}},
				},
			},
			&content,
		),
	)
	persistent, err := app.Prepare(t.Context(), hash, true, false)
	require.NoError(t, err)
	require.Equal(t, first.Evidence, persistent.Evidence)
	tx, err := db.Pool.BeginTx(t.Context(), pgx.TxOptions{AccessMode: pgx.ReadOnly})

	require.NoError(t, err)
	defer func() { _ = tx.Rollback(t.Context()) }()

	readonly, err := LoadEvidence(t.Context(), tx, []protocol.ID{hash}, 0)
	require.NoError(t, err)
	require.Len(t, readonly, 1)
	require.JSONEq(t, string(persistent.Evidence.Classification), string(readonly[0].Classification))
	require.Equal(t, persistent.Evidence, readonly[0])
	require.NoError(t, tx.Rollback(t.Context()))
	// Without durable ordinary state, legacy classifications still use the same projection.
	require.NoError(
		t,
		db.Gorm.Where("info_hash = ? AND stage = 'ordinary'", hash).
			Delete(&model.ClassificationEvaluation{}).
			Error,
	)
	require.NoError(
		t,
		db.Gorm.Preload("Hint").
			Preload("Files").
			Preload("Contents.Content").
			Where("info_hash = ?", hash).
			First(&torrent).
			Error,
	)
	expected, err := BuildEvidence(torrent)
	require.NoError(t, err)
	legacy, err := db.Pool.BeginTx(t.Context(), pgx.TxOptions{AccessMode: pgx.ReadOnly})

	require.NoError(t, err)
	defer func() { _ = legacy.Rollback(t.Context()) }()

	actual, err := LoadEvidence(t.Context(), legacy, []protocol.ID{hash}, 0)
	require.NoError(t, err)
	require.Equal(t, expected, actual[0])
}

func TestOrdinaryReplayAuthorityTransitions(t *testing.T) {
	t.Parallel()

	for _, changed := range []bool{false, true} {
		for _, rematch := range []bool{false, true} {
			for _, work := range []bool{false, true} {
				t.Run(
					fmt.Sprintf("changed=%v/rematch=%v/priorWork=%v", changed, rematch, work),
					func(t *testing.T) {
						t.Parallel()
						db, app, hash := applicationFixture(t)

						var torrent model.Torrent
						require.NoError(
							t,
							db.Gorm.Where("info_hash = ?", hash).First(&torrent).Error,
						)
						baseline := model.TorrentContent{
							InfoHash:      hash,
							ContentType:   model.NewNullContentType(model.ContentTypeMovie),
							InferredTitle: "Initial",
							InferredYear:  1984,
						}

						require.NoError(
							t,
							db.Gorm.Transaction(
								func(tx *gorm.DB) error {
									return RecordOrdinary(
										tx,
										torrent,
										&baseline,
										"test",
										false,
									)
								},
							),
						)
						run, err := app.Prepare(t.Context(), hash, false, false)
						require.NoError(t, err)

						metadata := &Metadata{
							ReleaseGroup: "accepted-group",
							Episodes:     map[int][]int{2: {5}},
						}

						var content *model.Content

						if work {
							metadata.Work = &Work{Type: WorkMovie, ID: 20}
							metadata.Type = WorkMovie
							content = &model.Content{
								Type:        model.ContentTypeMovie,
								Source:      model.SourceTmdb,
								ID:          "20",
								Title:       "Enhanced Orchard",
								ReleaseYear: 2001,
							}
							content.UpdateTsv()
						}

						require.NoError(
							t,
							app.Complete(
								t.Context(),
								run,
								Evaluation{Outcome: OutcomeAccepted, Result: metadata},
								content,
							),
						)

						if changed {
							baseline.InferredTitle = "Changed"
						}

						expectedID := ""
						if work && !rematch {
							expectedID = "20"
						}

						for _, ordinaryRematch := range []bool{rematch, false} {
							next := baseline

							require.NoError(
								t,
								db.Gorm.Transaction(
									func(tx *gorm.DB) error {
										return RecordOrdinary(
											tx,
											torrent,
											&next,
											"test",
											ordinaryRematch,
										)
									},
								),
							)
							require.Equal(
								t,
								expectedID,
								next.ContentID.String,
								"authority must follow explicit ordinary rematch independently of fingerprint equality",
							)

							if changed {
								require.False(t, next.ReleaseGroup.Valid)
								require.Empty(t, next.Episodes)
							} else {
								require.Equal(
									t,
									"accepted-group",
									next.ReleaseGroup.String,
								)
								require.True(t, next.Episodes.HasEpisode(2, 5))
							}
							// Rebuild from durable state after deleting the replaceable
							// effective row.
							require.NoError(
								t,
								db.Gorm.Where("info_hash = ?", hash).
									Delete(&model.TorrentContent{}).
									Error,
							)
						}
					},
				)
			}
		}
	}
}

func TestNewHintAuthorityAfterAnExplicitEnhancementRematch(t *testing.T) {
	t.Parallel()
	db, app, hash := applicationFixture(t)
	hint := model.TorrentHint{InfoHash: hash, ContentType: model.ContentTypeMovie}
	require.NoError(t, db.Gorm.Create(&hint).Error)

	var torrent model.Torrent
	require.NoError(t, db.Gorm.Preload("Hint").Where("info_hash = ?", hash).First(&torrent).Error)
	baseline := model.TorrentContent{InfoHash: hash, ContentType: model.NewNullContentType(model.ContentTypeMovie)}

	require.NoError(
		t,
		db.Gorm.Transaction(
			func(tx *gorm.DB) error { return RecordOrdinary(tx, torrent, &baseline, "test", false) },
		),
	)
	run, err := app.Prepare(t.Context(), hash, true, true)
	require.NoError(t, err)

	work := model.Content{
		Type:   model.ContentTypeTvShow,
		Source: model.SourceTmdb,
		ID:     "20",
		Title:  "Explicitly rematched series",
	}
	work.UpdateTsv()
	require.NoError(
		t,
		app.Complete(
			t.Context(),
			run,
			Evaluation{
				Outcome: OutcomeAccepted,
				Result:  &Metadata{Type: WorkSeries, Work: &Work{Type: WorkSeries, ID: 20}},
			},
			&work,
		),
	)

	replayed := baseline

	require.NoError(
		t,
		db.Gorm.Transaction(
			func(tx *gorm.DB) error { return RecordOrdinary(tx, torrent, &replayed, "test", false) },
		),
	)
	require.Equal(t, "20", replayed.ContentID.String, "unchanged supplied hint must not undo an authorized rematch")
	// A later fields-only result must preserve that established authority too.
	run, err = app.Prepare(t.Context(), hash, true, false)
	require.NoError(t, err)
	require.NoError(
		t,
		app.Complete(
			t.Context(),
			run,
			Evaluation{Outcome: OutcomeAccepted, Result: &Metadata{ReleaseGroup: "new-group"}},
			nil,
		),
	)
	require.NoError(t, db.Gorm.Where("info_hash = ?", hash).Delete(&model.TorrentContent{}).Error)

	replayed = baseline

	require.NoError(
		t,
		db.Gorm.Transaction(
			func(tx *gorm.DB) error { return RecordOrdinary(tx, torrent, &replayed, "test", false) },
		),
	)
	require.Equal(t, "20", replayed.ContentID.String)
	require.Equal(t, "new-group", replayed.ReleaseGroup.String)
	require.NoError(t, db.Gorm.Omit(clause.Associations).Create(&replayed).Error)
	// Newly supplied identity fields supersede the earlier override permission.
	hint.ContentType = model.ContentTypeGame
	require.NoError(t, db.Gorm.Create(&hint).Error)
	require.NoError(t, db.Gorm.Preload("Hint").Where("info_hash = ?", hash).First(&torrent).Error)

	baseline = model.TorrentContent{InfoHash: hash, ContentType: model.NewNullContentType(model.ContentTypeGame)}
	for range 2 {
		replayed = baseline

		require.NoError(
			t,
			db.Gorm.Transaction(
				func(tx *gorm.DB) error { return RecordOrdinary(tx, torrent, &replayed, "test", false) },
			),
		)
		require.Equal(t, model.ContentTypeGame, replayed.ContentType.ContentType)
		require.False(t, replayed.ContentID.Valid)
		require.NoError(t, db.Gorm.Where("info_hash = ?", hash).Delete(&model.TorrentContent{}).Error)
	}
}
