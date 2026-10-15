package processor

import (
	"context"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/classifier"
	"github.com/bitmagnet-io/bitmagnet/internal/classifier/classification"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/database/query"
	"github.com/bitmagnet-io/bitmagnet/internal/database/search"
	"github.com/bitmagnet-io/bitmagnet/internal/enhancement"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/bitmagnet-io/bitmagnet/internal/tmdb"
	tmdb_mocks "github.com/bitmagnet-io/bitmagnet/internal/tmdb/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProcessPreservesRuleDerivedTypeWithoutStoredFiles(t *testing.T) {
	t.Parallel()

	for _, filesStatus := range []model.FilesStatus{
		model.FilesStatusNoInfo,
		model.FilesStatusOverThreshold,
	} {
		t.Run(filesStatus.String(), func(t *testing.T) {
			t.Parallel()

			db := dbtest.New(t)
			ctx := t.Context()
			now := time.Now().UTC()
			infoHash := protocol.ID{1}

			require.NoError(t, db.Query.Torrent.WithContext(ctx).Create(&model.Torrent{
				InfoHash:    infoHash,
				Name:        "rule-derived type",
				CreatedAt:   now,
				UpdatedAt:   now,
				FilesStatus: filesStatus,
			}))
			require.NoError(
				t,
				db.Query.TorrentContent.WithContext(ctx).Create(&model.TorrentContent{
					InfoHash:    infoHash,
					ContentType: model.NewNullContentType(model.ContentTypeXxx),
					CreatedAt:   now,
					UpdatedAt:   now,
				}),
			)

			searchResult := search.New(search.Params{
				Query: lazy.New(func() (*dao.Query, error) { return db.Query, nil }),
			})
			searcher, err := searchResult.Search.Get()
			require.NoError(t, err)

			runner := &hintPreservingRunner{}
			processor := processor{
				defaultWorkflow: "default",
				search:          searcher,
				runner:          runner,
				dao:             db.Query,
			}
			require.NoError(t, processor.Process(ctx, MessageParams{
				InfoHashes:   []protocol.ID{infoHash},
				ClassifyMode: ClassifyModeRematch,
			}))

			assert.Equal(t, model.ContentTypeXxx, runner.hint.ContentType)
			assert.False(t, runner.hint.ContentSource.Valid)

			contents, err := db.Query.TorrentContent.WithContext(ctx).
				Where(db.Query.TorrentContent.InfoHash.Eq(infoHash)).
				Find()
			require.NoError(t, err)
			require.Len(t, contents, 1)
			assert.Equal(t, model.ContentTypeXxx, contents[0].ContentType.ContentType)
			assert.False(t, contents[0].ContentSource.Valid)
		})
	}
}

type hintPreservingRunner struct {
	hint model.TorrentHint
}

func (r *hintPreservingRunner) Run(
	_ context.Context,
	_ string,
	_ classifier.Flags,
	torrent model.Torrent,
) (classification.Result, error) {
	r.hint = torrent.Hint

	return classification.Result{
		ContentAttributes: classification.ContentAttributes{
			ContentType: torrent.Hint.NullContentType(),
		},
	}, nil
}

func TestProcessReconstructsAcceptedEnhancementWithoutSelfTriggering(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	hash := protocol.ID{71}
	require.NoError(
		t,
		db.Gorm.Create(
			&model.Torrent{
				InfoHash:    hash,
				Name:        "opaque.file",
				FilesStatus: model.FilesStatusSingle,
				FilesCount:  model.NewNullUint(1),
			},
		).Error,
	)

	sr := search.New(
		search.Params{Query: lazy.New(func() (*dao.Query, error) { return db.Query, nil })},
	)
	searcher, err := sr.Search.Get()
	require.NoError(t, err)

	p := processor{
		defaultWorkflow: "default",
		search:          searcher,
		runner:          &hintPreservingRunner{},
		dao:             db.Query,
	}
	require.NoError(t, p.Process(t.Context(), MessageParams{InfoHashes: []protocol.ID{hash}}))

	app := enhancement.NewApplication(db.Gorm, nil, nil, enhancement.NewDefaultConfig())
	run, err := app.Prepare(t.Context(), hash, false, false)
	require.NoError(t, err)
	require.NoError(
		t,
		app.Complete(
			t.Context(),
			run,
			enhancement.Evaluation{
				Outcome: "accepted",
				Result: &enhancement.Metadata{
					Type:     "tv_show",
					Title:    "Hidden Orchard",
					Year:     1998,
					Episodes: map[int][]int{2: {5}},
					Tags:     []string{"llm-edition-deluxe"},
				},
			},
			nil,
		),
	)
	require.NoError(t, p.Process(t.Context(), MessageParams{InfoHashes: []protocol.ID{hash}}))

	var rows []model.TorrentContent
	require.NoError(t, db.Gorm.Where("info_hash = ?", hash).Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, "Hidden Orchard (1998) S02E05", rows[0].Title())
	next, err := app.Prepare(t.Context(), hash, true, false)
	require.NoError(t, err)
	require.Equal(t, run.InputFingerprint, next.InputFingerprint)
	require.NoError(
		t,
		db.Gorm.Model(&model.Torrent{}).
			Where("info_hash = ?", hash).
			Update("name", "changed evidence").
			Error,
	)
	require.NoError(t, p.Process(t.Context(), MessageParams{InfoHashes: []protocol.ID{hash}}))

	rows = nil
	require.NoError(t, db.Gorm.Where("info_hash = ?", hash).Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Empty(t, rows[0].InferredTitle)
	require.False(t, rows[0].ContentType.Valid)

	var staleTags int64
	require.NoError(
		t,
		db.Gorm.Model(&model.TorrentTag{}).
			Where("info_hash = ? AND name = ?", hash, "llm-edition-deluxe").
			Count(&staleTags).
			Error,
	)
	require.Zero(t, staleTags, "invalidated evidence must not retain generated output tags")
}

type selectedWorkEngine struct{ id int64 }

func (e *selectedWorkEngine) Evaluate(
	_ context.Context,
	evidence enhancement.Evidence,
) enhancement.Evaluation {
	return enhancement.Evaluation{
		InfoHash: evidence.InfoHash,
		Outcome:  "accepted",
		Result: &enhancement.Metadata{
			Type:     "tv_show",
			Title:    "Inferred",
			Year:     2020,
			Work:     &enhancement.Work{Type: "tv_show", ID: e.id},
			Episodes: map[int][]int{2: {5}},
			Tags:     []string{"llm-edition-deluxe"},
		},
	}
}

func TestSeriesIdentitySurvivesReconstructionRematchAndReindex(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	hash := protocol.ID{72}
	require.NoError(
		t,
		db.Gorm.Create(
			&model.Torrent{
				InfoHash:    hash,
				Name:        "Show.S02E05",
				FilesStatus: model.FilesStatusSingle,
			},
		).Error,
	)

	sr := search.New(
		search.Params{Query: lazy.New(func() (*dao.Query, error) { return db.Query, nil })},
	)
	searcher, err := sr.Search.Get()
	require.NoError(t, err)

	p := processor{
		defaultWorkflow: "default",
		search:          searcher,
		runner:          &hintPreservingRunner{},
		dao:             db.Query,
	}
	require.NoError(t, p.Process(t.Context(), MessageParams{InfoHashes: []protocol.ID{hash}}))

	catalogue := tmdb_mocks.NewClient(t)
	for _, id := range []int64{42, 43} {
		catalogue.EXPECT().
			TvDetails(mock.Anything, tmdb.TvDetailsRequest{SeriesID: id, AppendToResponse: []string{"external_ids"}}).
			Return(tmdb.TvDetailsResponse{
				ID:               id,
				Name:             "Catalogue Series",
				FirstAirDate:     "2001-01-01",
				OriginalLanguage: "en",
				Genres:           []tmdb.Genre{{ID: 8, Name: "Synthetic genre"}},
				PosterPath:       "/poster.jpg",
			}, nil).
			Once()
	}

	engine := &selectedWorkEngine{id: 42}
	app := enhancement.NewApplication(db.Gorm, engine, catalogue, enhancement.NewDefaultConfig())
	result, err := app.Enhance(t.Context(), hash, false, false)
	require.NoError(t, err)
	require.Equal(t, "accepted", result.Outcome)

	var state model.ClassificationEvaluation
	require.NoError(
		t,
		db.Gorm.Where("info_hash = ? AND stage = 'enhancement'", hash).First(&state).Error,
	)

	var first model.TorrentContent
	require.NoError(
		t,
		db.Gorm.Preload("Content.Attributes").
			Preload("Content.Collections").
			Where("info_hash = ?", hash).
			First(&first).
			Error,
	)
	require.Equal(t, "42", first.ContentID.String)
	require.Equal(t, "Catalogue Series (2001) S02E05", first.Title())
	require.Empty(t, first.Languages, "catalogue original language must not become release audio")
	require.Len(t, first.Content.Collections, 1)
	require.NotEmpty(t, first.Content.Attributes)
	require.NoError(t, p.Process(t.Context(), MessageParams{InfoHashes: []protocol.ID{hash}}))

	var replayed model.TorrentContent
	require.NoError(t, db.Gorm.Where("info_hash = ?", hash).First(&replayed).Error)
	require.Equal(t, first.ID, replayed.ID)
	require.Equal(t, first.Episodes, replayed.Episodes)

	// A later ordinary enhancement cannot replace authority introduced by the first one.
	for _, metadata := range []*enhancement.Metadata{
		{Type: "tv_show", Work: &enhancement.Work{Type: "tv_show", ID: 43}},
		{Title: "Release-only update", Episodes: map[int][]int{2: {5}}},
	} {
		run, prepareErr := app.Prepare(t.Context(), hash, true, false)
		require.NoError(t, prepareErr)

		var details *model.Content
		if metadata.Work != nil {
			details = &model.Content{
				Type:        model.ContentTypeTvShow,
				Source:      model.SourceTmdb,
				ID:          "43",
				Title:       "Replacement series",
				ReleaseYear: 2002,
			}
		}

		require.NoError(
			t,
			app.Complete(
				t.Context(),
				run,
				enhancement.Evaluation{Outcome: enhancement.OutcomeAccepted, Result: metadata},
				details,
			),
		)
		require.NoError(t, p.Process(t.Context(), MessageParams{InfoHashes: []protocol.ID{hash}}))

		var retained model.TorrentContent
		require.NoError(t, db.Gorm.Where("info_hash = ?", hash).First(&retained).Error)
		require.Equal(t, "42", retained.ContentID.String)
	}

	engine.id = 43
	_, err = app.Enhance(t.Context(), hash, true, true)
	require.NoError(t, err)
	require.NoError(t, p.Process(t.Context(), MessageParams{InfoHashes: []protocol.ID{hash}}))

	replayed = model.TorrentContent{}
	require.NoError(
		t,
		db.Gorm.Preload("Torrent").
			Preload("Content").
			Where("info_hash = ?", hash).
			First(&replayed).
			Error,
	)
	require.Equal(t, "43", replayed.ContentID.String)
	require.NotEqual(t, first.ID, replayed.ID)
	// Reindex is a vector rebuild only: no stage is newly evaluated.
	var before model.ClassificationEvaluation
	require.NoError(
		t,
		db.Gorm.Where("info_hash = ? AND stage = 'enhancement'", hash).First(&before).Error,
	)
	replayed.UpdateTsv()
	require.NoError(t, db.Gorm.Model(&replayed).Update("tsv", replayed.Tsv).Error)

	var after model.ClassificationEvaluation
	require.NoError(
		t,
		db.Gorm.Where("info_hash = ? AND stage = 'enhancement'", hash).First(&after).Error,
	)
	require.Equal(t, before, after)

	// Ordinary explicit rematch also has authority to select a different work.
	replacement := model.Content{
		Type:        model.ContentTypeTvShow,
		Source:      model.SourceTmdb,
		ID:          "44",
		Title:       "Ordinary rematch",
		ReleaseYear: 2003,
	}
	require.NoError(t, db.Gorm.Create(&replacement).Error)
	p.runner = &catalogueResultRunner{content: replacement}
	require.NoError(
		t,
		p.Process(
			t.Context(),
			MessageParams{InfoHashes: []protocol.ID{hash}, ClassifyMode: ClassifyModeRematch},
		),
	)

	var replaced model.TorrentContent
	require.NoError(t, db.Gorm.Where("info_hash = ?", hash).First(&replaced).Error)
	require.Equal(t, "44", replaced.ContentID.String)
}

func TestOrdinaryProcessorAppliesStoredHintTitleAndYear(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	hash := protocol.ID{73}
	require.NoError(
		t,
		db.Gorm.Create(
			&model.Torrent{
				InfoHash:    hash,
				Name:        "opaque.mkv",
				FilesStatus: model.FilesStatusSingle,
			},
		).Error,
	)
	require.NoError(
		t,
		db.Gorm.Create(
			&model.TorrentHint{
				InfoHash:    hash,
				ContentType: model.ContentTypeMovie,
				Title:       model.NewNullString("Hinted Orchard"),
				ReleaseYear: 1984,
			},
		).Error,
	)

	cfg := classifier.NewDefaultConfig()
	cfg.Flags = map[string]any{"apis_enabled": false, "local_search_enabled": false}
	sr := search.New(
		search.Params{Query: lazy.New(func() (*dao.Query, error) { return db.Query, nil })},
	)
	ml := lazy.New(
		func() (*moderation.Service, error) { return moderation.NewService(db.Gorm), nil },
	)
	cr := classifier.New(
		classifier.Params{
			Config:           cfg,
			ModerationConfig: moderation.NewDefaultConfig(),
			Moderation:       ml,
			MetadataSearch:   sr.MetadataSearch,
			TmdbClient: lazy.New(
				func() (tmdb.Client, error) { return tmdb_mocks.NewClient(t), nil },
			),
		},
	)
	p, err := New(
		Params{
			Moderation:       ml,
			ClassifierConfig: cfg,
			ClassifierSource: cr.Source,
			Search:           sr.Search,
			Workflow:         cr.Runner,
			Dao:              lazy.New(func() (*dao.Query, error) { return db.Query, nil }),
		},
	).Processor.Get()
	require.NoError(t, err)
	require.NoError(t, p.Process(t.Context(), MessageParams{InfoHashes: []protocol.ID{hash}}))

	var tc model.TorrentContent
	require.NoError(t, db.Gorm.Where("info_hash = ?", hash).First(&tc).Error)
	require.Equal(t, "Hinted Orchard (1984)", tc.Title())
	// Reprocessing must use current shared work metadata rather than a stale copy
	// in the durable ordinary snapshot, including when rebuilding full-text search.
	content := model.Content{
		Type:        model.ContentTypeMovie,
		Source:      model.SourceTmdb,
		ID:          "99",
		Title:       "Old work",
		ReleaseYear: 2001,
	}
	require.NoError(t, db.Gorm.Create(&content).Error)
	require.NoError(t, db.Gorm.Model(&model.TorrentHint{}).Where("info_hash = ?", hash).
		Updates(map[string]any{"content_source": "tmdb", "content_id": "99"}).Error)

	var torrent model.Torrent
	require.NoError(t, db.Gorm.Preload("Hint").Where("info_hash = ?", hash).First(&torrent).Error)
	baseline := model.TorrentContent{
		InfoHash:    hash,
		ContentType: model.NewNullContentType(model.ContentTypeMovie),
		ContentSource: model.NewNullString(
			model.SourceTmdb,
		),
		ContentID: model.NewNullString("99"),
		Content:   content,
	}

	require.NoError(
		t,
		db.Gorm.Transaction(
			func(tx *gorm.DB) error { return enhancement.RecordOrdinary(tx, torrent, &baseline, "test", false) },
		),
	)
	require.NoError(t, db.Gorm.Model(&content).Update("title", "Fresh Orchard").Error)
	require.NoError(t, p.Process(t.Context(), MessageParams{InfoHashes: []protocol.ID{hash}}))

	searcher, err := sr.Search.Get()
	require.NoError(t, err)
	rows, err := searcher.TorrentContent(
		t.Context(),
		search.TorrentContentDefaultOption(),
		query.SearchString("Fresh Orchard"),
	)
	require.NoError(t, err)
	require.Len(t, rows.Items, 1)
	require.Equal(t, "Fresh Orchard (2001)", rows.Items[0].Title())
}

type ordinaryTitleRunner struct {
	title string
	year  model.Year
}

func (r *ordinaryTitleRunner) Run(
	_ context.Context,
	_ string,
	_ classifier.Flags,
	_ model.Torrent,
) (classification.Result, error) {
	return classification.Result{ContentAttributes: classification.ContentAttributes{
		ContentType: model.NewNullContentType(
			model.ContentTypeMovie,
		), BaseTitle: model.NewNullString(r.title), Date: model.NewDateFromParts(r.year, 1, 1),
	}}, nil
}

func TestOrdinaryFallbackChangesInvalidateEnhancementWithoutSelfTriggering(t *testing.T) {
	t.Parallel()

	for _, outcome := range []string{enhancement.OutcomeAccepted, enhancement.OutcomeUnresolved} {
		t.Run(outcome, func(t *testing.T) {
			t.Parallel()
			db := dbtest.New(t)
			hash := protocol.ID{74}
			require.NoError(
				t,
				db.Gorm.Create(
					&model.Torrent{
						InfoHash:    hash,
						Name:        "opaque.release",
						FilesStatus: model.FilesStatusSingle,
					},
				).Error,
			)

			sr := search.New(
				search.Params{Query: lazy.New(func() (*dao.Query, error) { return db.Query, nil })},
			)
			searcher, err := sr.Search.Get()
			require.NoError(t, err)

			runner := &ordinaryTitleRunner{title: "Ordinary Orchard", year: 1984}
			p := processor{
				defaultWorkflow: "default",
				search:          searcher,
				runner:          runner,
				dao:             db.Query,
			}
			require.NoError(
				t,
				p.Process(t.Context(), MessageParams{InfoHashes: []protocol.ID{hash}}),
			)

			app := enhancement.NewApplication(db.Gorm, nil, nil, enhancement.NewDefaultConfig())
			run, err := app.Prepare(t.Context(), hash, false, false)
			require.NoError(t, err)
			require.Contains(t, string(run.Evidence.Classification), `"title":"Ordinary Orchard"`)
			require.Contains(t, string(run.Evidence.Classification), `"year":1984`)

			result := enhancement.Evaluation{Outcome: outcome}
			if outcome == enhancement.OutcomeAccepted {
				result.Result = &enhancement.Metadata{Title: "Enhanced Orchard", Year: 1998}
			}

			require.NoError(t, app.Complete(t.Context(), run, result, nil))
			require.NoError(
				t,
				p.Process(t.Context(), MessageParams{InfoHashes: []protocol.ID{hash}}),
			)
			unchanged, err := app.Prepare(t.Context(), hash, true, false)
			require.NoError(t, err)
			require.Equal(t, run.InputFingerprint, unchanged.InputFingerprint)

			var current model.TorrentContent
			require.NoError(t, db.Gorm.Where("info_hash = ?", hash).First(&current).Error)

			if outcome == enhancement.OutcomeAccepted {
				require.Equal(t, "Enhanced Orchard (1998)", current.Title())
			}

			runner.title = "Changed Orchard"
			runner.year = 2002

			require.NoError(
				t,
				p.Process(t.Context(), MessageParams{InfoHashes: []protocol.ID{hash}}),
			)

			current = model.TorrentContent{}
			require.NoError(t, db.Gorm.Where("info_hash = ?", hash).First(&current).Error)
			require.Equal(t, "Changed Orchard (2002)", current.Title())
			changed, err := app.Prepare(t.Context(), hash, false, false)
			require.NoError(t, err)
			require.NotEqual(t, run.InputFingerprint, changed.InputFingerprint)
		})
	}
}

type catalogueResultRunner struct{ content model.Content }

func (r *catalogueResultRunner) Run(
	_ context.Context,
	_ string,
	_ classifier.Flags,
	_ model.Torrent,
) (classification.Result, error) {
	return classification.Result{
		ContentAttributes: classification.ContentAttributes{
			ContentType: model.NewNullContentType(r.content.Type),
		},
		Content: &r.content,
	}, nil
}

func TestRetainedCatalogueIdentityRemainsSearchableAfterEvidenceChanges(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	hash := protocol.ID{75}
	require.NoError(
		t,
		db.Gorm.Create(
			&model.Torrent{InfoHash: hash, Name: "opaque", FilesStatus: model.FilesStatusSingle},
		).Error,
	)

	sr := search.New(search.Params{Query: lazy.New(func() (*dao.Query, error) { return db.Query, nil })})
	searcher, err := sr.Search.Get()
	require.NoError(t, err)

	runner := &ordinaryTitleRunner{title: "Initial", year: 1984}
	p := processor{defaultWorkflow: "default", search: searcher, runner: runner, dao: db.Query}
	require.NoError(t, p.Process(t.Context(), MessageParams{InfoHashes: []protocol.ID{hash}}))

	app := enhancement.NewApplication(db.Gorm, nil, nil, enhancement.NewDefaultConfig())
	run, err := app.Prepare(t.Context(), hash, false, false)
	require.NoError(t, err)

	work := model.Content{
		Type:        model.ContentTypeMovie,
		Source:      model.SourceTmdb,
		ID:          "10",
		Title:       "Orchard",
		ReleaseYear: 2001,
	}
	work.UpdateTsv()
	require.NoError(
		t,
		app.Complete(
			t.Context(),
			run,
			enhancement.Evaluation{
				Outcome: enhancement.OutcomeAccepted,
				Result: &enhancement.Metadata{
					Type: enhancement.WorkMovie,
					Work: &enhancement.Work{Type: enhancement.WorkMovie, ID: 10},
				},
			},
			&work,
		),
	)
	require.NoError(t, db.Gorm.Model(&model.Torrent{}).Where("info_hash = ?", hash).Update("name", "changed").Error)

	runner.title = "Unrelated"

	require.NoError(t, p.Process(t.Context(), MessageParams{InfoHashes: []protocol.ID{hash}}))
	rows, err := searcher.TorrentContent(
		t.Context(),
		search.TorrentContentDefaultOption(),
		query.SearchString("Orchard"),
	)
	require.NoError(t, err)
	require.Len(t, rows.Items, 1, "retained work must remain searchable after ordinary reconstruction")
	require.Equal(t, "10", rows.Items[0].ContentID.String)
	rows, err = searcher.TorrentContent(
		t.Context(),
		search.TorrentContentDefaultOption(),
		query.SearchString("Unrelated"),
	)
	require.NoError(t, err)
	require.Empty(t, rows.Items, "superseded inferred title must not remain in the effective vector")
}
