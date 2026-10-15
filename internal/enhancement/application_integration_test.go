package enhancement

import (
	"context"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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
			func(tx *gorm.DB) error { return RecordOrdinary(tx, torrent, &tc0, "test") },
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
			func(tx *gorm.DB) error { return RecordOrdinary(tx, torrent, &tc, "test") },
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
			func(tx *gorm.DB) error { return RecordOrdinary(tx, torrent, &baseline, "test") },
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
			func(tx *gorm.DB) error { return RecordOrdinary(tx, torrent, &baseline, "test") },
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
			func(tx *gorm.DB) error { return RecordOrdinary(tx, torrent, &baseline, "test") },
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
}
