package enhancement

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/database/catalogue"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/bitmagnet-io/bitmagnet/internal/tmdb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrStale         = errors.New("enhancement input or generation is stale")
	ErrEvaluated     = errors.New("input and recipe already evaluated")
	ErrNotClassified = errors.New("ordinary classification has not completed for current evidence")
	ErrHidden        = errors.New("torrent is hidden by moderation")
)

type Engine interface {
	Evaluate(context.Context, Evidence) Evaluation
}

// Application is an explicitly invoked, bounded persistence boundary. It starts no
// workers and is deliberately separate from the read-only evaluator/CLI wiring.
type Application struct {
	db        *gorm.DB
	engine    Engine
	catalogue tmdb.Client
	config    Config
}

type Run struct {
	Evidence             Evidence
	InputFingerprint     string
	hash                 protocol.ID
	generation           int64
	recipeFingerprint    string
	rematch              bool
	authorityFingerprint string
	ordinaryRecipe       string
	ctx                  context.Context
	expires              time.Time
}

type catalogIdentity struct {
	Type   string `json:"type"`
	Source string `json:"source"`
	ID     string `json:"id"`
}

type acceptedOutput struct {
	Catalog     *catalogIdentity `json:"catalog,omitempty"`
	CatalogHint *catalogIdentity `json:"catalogHint,omitempty"`
	Recipe      string           `json:"recipe"`
	Metadata    Metadata         `json:"metadata"`
	Rematch     bool             `json:"rematch"`
	Tags        []string         `json:"reservedTags"`
}

func NewApplication(db *gorm.DB, engine Engine, catalogue tmdb.Client, config Config) *Application {
	return &Application{db: db, engine: engine, catalogue: catalogue, config: config}
}

// Enhance completes one torrent within the configured deadline. Inference and
// catalogue detail retrieval occur outside the short application transaction.
func (s *Application) Enhance(
	ctx context.Context,
	hash protocol.ID,
	force, rematch bool,
) (Evaluation, error) {
	ctx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()

	run, err := s.Prepare(ctx, hash, force, rematch)
	if err != nil {
		return Evaluation{}, err
	}

	if s.engine == nil {
		return Evaluation{}, errors.New("enhancement engine is required")
	}

	if a := automaticAccounting(ctx); a != nil {
		updated := s.db.WithContext(ctx).
			Exec(`UPDATE enhancement_claims SET generation = ?, input_fingerprint = ?, recipe_fingerprint = ? WHERE
token = ? AND expires_at > now()`, run.generation, run.InputFingerprint, run.recipeFingerprint, a.claim)
		if updated.Error != nil {
			return Evaluation{}, updated.Error
		}

		if updated.RowsAffected != 1 {
			return Evaluation{}, ErrLease
		}
	}

	result := s.engine.Evaluate(ctx, run.Evidence)
	if result.Outcome == OutcomeDeferred {
		return result, nil
	}

	var content *model.Content
	if result.Outcome == OutcomeAccepted && result.Result != nil && result.Result.Work != nil {
		content, err = s.details(ctx, *result.Result.Work)
		if err != nil {
			result.Outcome = OutcomeProviderFailure
			result.Result = nil
		}
	}

	if ctx.Err() != nil {
		result.Outcome = OutcomeLimited
		result.Result = nil
		// A bounded cleanup records only the limited evaluation, never late metadata.
		cleanup, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancelCleanup()

		run.ctx = cleanup
		run.expires = time.Now().Add(5 * time.Second)

		return result, errors.Join(ctx.Err(), s.Complete(cleanup, run, result, nil))
	}

	applyErr := s.Complete(ctx, run, result, content)

	return result, errors.Join(err, applyErr)
}

func (s *Application) details(ctx context.Context, w Work) (*model.Content, error) {
	if s.catalogue == nil {
		return nil, errors.New("catalogue client is required")
	}

	var (
		c   model.Content
		err error
	)

	switch w.Type {
	case WorkMovie:
		d, e := s.catalogue.MovieDetails(ctx, tmdb.MovieDetailsRequest{ID: w.ID})
		if e != nil {
			return nil, e
		}

		c, err = tmdb.MovieDetailsToMovieModel(d)
	case WorkSeries:
		d, e := s.catalogue.TvDetails(
			ctx,
			tmdb.TvDetailsRequest{SeriesID: w.ID, AppendToResponse: []string{"external_ids"}},
		)
		if e != nil {
			return nil, e
		}

		c, err = tmdb.TvShowDetailsToTvShowModel(d)
	default:
		return nil, errors.New("invalid work type")
	}

	if err != nil {
		return nil, err
	}

	c.UpdateTsv()

	return &c, nil
}

// LockTorrent shares the moderation ledger's lock order, including when no decision
// exists yet. Row locks then serialize application with ingestion/reconstruction.
func LockTorrent(tx *gorm.DB, hash protocol.ID) error {
	id := moderation.IdentifierForInfoHash(hash)
	if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(binary.BigEndian.Uint64(id[:8]))).Error; err != nil {
		return err
	}

	var t model.Torrent
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("info_hash = ?", hash).
		First(&t).
		Error; err != nil {
		return err
	}

	var visible bool
	if err := tx.Model(&model.Torrent{}).
		Select(catalogue.VisibleInfoHash("torrents.info_hash")).
		Where("info_hash = ?", hash).
		Scan(&visible).
		Error; err != nil {
		return err
	}

	if !visible {
		return ErrHidden
	}

	return nil
}

func loadTorrent(tx *gorm.DB, hash protocol.ID) (model.Torrent, error) {
	var t model.Torrent

	err := tx.Preload("Files").Preload("Hint").Where("info_hash = ?", hash).First(&t).Error

	return t, err
}

func inputEvidence(t model.Torrent, ordinary model.TorrentContent) (Evidence, string, error) {
	t.Contents = []model.TorrentContent{ordinary}

	e, err := BuildEvidence(t)
	if err != nil {
		return e, "", err
	}

	b, err := json.Marshal(e)

	return e, fingerprint(b), err
}

func ObservationFingerprint(t model.Torrent) (string, error) {
	t.Contents = nil

	e, err := BuildEvidence(t)
	if err != nil {
		return "", err
	}

	b, err := json.Marshal(e)

	return fingerprint(b), err
}

func (s *Application) Prepare(
	ctx context.Context,
	hash protocol.ID,
	force, rematch bool,
) (run Run, err error) {
	run.hash = hash

	run.ctx = ctx
	if s.config.Timeout <= 0 {
		return run, errors.New("enhancement deadline must be positive")
	}

	run.expires = time.Now().Add(s.config.Timeout)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(run.expires) {
		run.expires = deadline
	}

	run.rematch = rematch
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := LockTorrent(tx, hash); err != nil {
			return err
		}

		t, err := loadTorrent(tx, hash)
		if err != nil {
			return err
		}

		var ordinary model.ClassificationEvaluation
		if err := tx.Where("info_hash = ? AND stage = 'ordinary'", hash).First(&ordinary).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotClassified
			}

			return err
		}

		observation, err := ObservationFingerprint(t)
		if err != nil {
			return err
		}

		if ordinary.InputFingerprint != observation {
			return ErrNotClassified
		}

		tc, err := decodeOrdinary(ordinary.OrdinaryOutput)
		if err != nil {
			return err
		}

		recipe, canonical, err := Recipe(s.config, ordinary.RecipeFingerprint)
		if err != nil {
			return err
		}

		run.recipeFingerprint = recipe
		run.ordinaryRecipe = ordinary.RecipeFingerprint

		run.authorityFingerprint, err = effectiveFingerprint(tx, t)
		if err != nil {
			return err
		}

		run.Evidence, run.InputFingerprint, err = inputEvidence(t, tc)
		if err != nil {
			return err
		}

		state := model.ClassificationEvaluation{InfoHash: hash, Stage: "enhancement"}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error; err != nil {
			return err
		}

		if err := tx.Where("info_hash = ? AND stage = 'enhancement'", hash).First(&state).Error; err != nil {
			return err
		}

		if !force && !rematch && state.InputFingerprint == run.InputFingerprint &&
			state.RecipeFingerprint == recipe &&
			(state.Outcome == OutcomeAccepted ||
				state.Outcome == OutcomeUnresolved || state.Outcome == OutcomeLimited) {
			return ErrEvaluated
		}

		if automaticAccounting(ctx) != nil && !force && !rematch &&
			state.AttemptInput == run.InputFingerprint &&
			state.AttemptRecipe == recipe &&
			(state.AttemptOutcome == OutcomeProviderFailure || state.AttemptOutcome == OutcomeMalformed) {
			return ErrEvaluated
		}

		state.AttemptInput = run.InputFingerprint
		state.AttemptRecipe = recipe
		state.Generation++
		state.AttemptOutcome = "running"
		state.AttemptedAt = sql.NullTime{Time: time.Now().UTC(), Valid: true}

		run.generation = state.Generation
		if err := tx.Save(&state).Error; err != nil {
			return err
		}

		return tx.Clauses(clause.OnConflict{DoNothing: true}).
			Create(&model.KeyValue{Key: "classifier-recipe/" + recipe, Value: canonical}).
			Error
	})

	return run, err
}

func (s *Application) Complete(
	ctx context.Context,
	run Run,
	result Evaluation,
	content *model.Content,
) error {
	if run.ctx == nil || run.generation == 0 {
		return ErrStale
	}

	if err := completionContextError(ctx, run); err != nil {
		return err
	}

	ctx, cancel := context.WithDeadline(ctx, run.expires)
	defer cancel()

	// The stored admission context supplies cancellation independently of the caller.
	//nolint:contextcheck
	stop := context.AfterFunc(run.ctx, cancel)
	defer stop()

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := LockTorrent(tx, run.hash); err != nil {
			return err
		}

		t, err := loadTorrent(tx, run.hash)
		if err != nil {
			return err
		}

		var state, ordinary model.ClassificationEvaluation
		if err := tx.Where("info_hash = ? AND stage = 'enhancement'", run.hash).
			First(&state).
			Error; err != nil {
			return err
		}

		if a := automaticAccounting(ctx); a != nil {
			var active int64
			if err := tx.Table("enhancement_claims").
				Where("token = ? AND expires_at > now()", a.claim).
				Count(&active).
				Error; err != nil {
				return err
			}

			if active != 1 {
				return ErrLease
			}
		}

		if state.Generation != run.generation {
			return ErrStale
		}

		if err := tx.Where("info_hash = ? AND stage = 'ordinary'", run.hash).
			First(&ordinary).
			Error; err != nil {
			return err
		}

		if ordinary.RecipeFingerprint != run.ordinaryRecipe {
			return ErrStale
		}

		tc, err := decodeOrdinary(ordinary.OrdinaryOutput)
		if err != nil {
			return err
		}

		_, current, err := inputEvidence(t, tc)
		if err != nil {
			return err
		}

		if current != run.InputFingerprint {
			return ErrStale
		}

		authority, err := effectiveFingerprint(tx, t)
		if err != nil {
			return err
		}

		if authority != run.authorityFingerprint {
			return ErrStale
		}

		now := sql.NullTime{Time: time.Now().UTC(), Valid: true}
		state.AttemptOutcome = result.Outcome
		state.AttemptedAt = now

		switch result.Outcome {
		case OutcomeAccepted, OutcomeUnresolved, OutcomeLimited:
			state.InputFingerprint = current
			state.RecipeFingerprint = run.recipeFingerprint
			state.Outcome = result.Outcome
			state.CompletedAt = now
		case OutcomeMalformed, OutcomeProviderFailure:
			if err := tx.Save(&state).Error; err != nil {
				return err
			}

			return completionContextError(ctx, run)
		default:
			return errors.New("invalid evaluation outcome")
		}

		if result.Outcome == OutcomeAccepted {
			if result.Result == nil {
				return errors.New("accepted result is missing metadata")
			}

			encoded, err := json.Marshal(result.Result)
			if err != nil {
				return err
			}

			candidates := make(map[Work]struct{})
			if result.Result.Work != nil {
				candidates[*result.Result.Work] = struct{}{}
			}

			if _, err := validateMetadata(string(encoded), s.config, candidates); err != nil {
				return err
			}

			if result.Result.Work != nil && content == nil {
				return errors.New("selected work requires provider details")
			}

			if result.Result.Work != nil &&
				(content.ID != workID(*result.Result.Work) ||
					content.Source != model.SourceTmdb || content.Type.String() != result.Result.Work.Type) {
				return errors.New("provider details do not match selected work")
			}

			reserved := append([]string(nil), s.config.Tags...)

			var prior acceptedOutput

			if state.AcceptedOutput != "" {
				if err := json.Unmarshal([]byte(state.AcceptedOutput), &prior); err != nil {
					return err
				}

				reserved = append(reserved, prior.Tags...)
			}

			slices.Sort(reserved)
			reserved = slices.Compact(reserved)

			b, err := json.Marshal(acceptedOutput{
				Catalog:     prior.Catalog,
				CatalogHint: prior.CatalogHint,
				Recipe:      run.recipeFingerprint,
				Metadata:    *result.Result,
				Rematch:     run.rematch,
				Tags:        reserved,
			})
			if err != nil {
				return err
			}

			state.AcceptedInput = current
			state.AcceptedOutput = string(b)

			if content != nil {
				if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).
					Create(content).
					Error; err != nil {
					return err
				}
			}

			tc.Torrent = t

			mode := preserveCatalog
			if run.rematch {
				mode = replaceCatalog
			}

			if err := applyAccepted(tx, t, &tc, &state, mode); err != nil {
				return err
			}

			if err := tx.Where("info_hash = ? AND id <> ?", run.hash, tc.InferID()).
				Delete(&model.TorrentContent{}).
				Error; err != nil {
				return err
			}

			tc.ID = ""
			tc.Content = model.Content{}

			tc.Torrent = model.Torrent{}
			if err := tx.Omit(clause.Associations).
				Clauses(clause.OnConflict{UpdateAll: true}).
				Create(&tc).
				Error; err != nil {
				return err
			}
		}

		if err := ctx.Err(); err != nil {
			return err
		}

		if err := tx.Save(&state).Error; err != nil {
			return err
		}

		return completionContextError(ctx, run)
	})
	if err != nil {
		if contextErr := completionContextError(ctx, run); contextErr != nil {
			return contextErr
		}
	}

	return err
}

func effectiveFingerprint(tx *gorm.DB, t model.Torrent) (string, error) {
	var rows []model.TorrentContent
	if err := tx.Preload("Content").Where("info_hash = ?", t.InfoHash).Order("id").Find(&rows).Error; err != nil {
		return "", err
	}

	t.Contents = rows

	e, err := BuildEvidence(t)
	if err != nil {
		return "", err
	}

	b, err := json.Marshal(e)

	return fingerprint(b), err
}

func completionContextError(ctx context.Context, run Run) error {
	if err := run.ctx.Err(); err != nil {
		return err
	}

	if time.Now().After(run.expires) {
		return context.DeadlineExceeded
	}

	return ctx.Err()
}
