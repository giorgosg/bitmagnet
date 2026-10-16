package enhancement

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestOrdinaryCompletionDurablyHandsOff(t *testing.T) {
	t.Parallel()
	db, _, hash := applicationFixture(t)

	var ready bool
	require.NoError(
		t,
		db.Gorm.Raw("SELECT enhancement_ready FROM torrents WHERE info_hash = ?", hash).Scan(&ready).Error,
	)
	require.True(t, ready)
	require.NoError(
		t,
		db.Gorm.Exec("UPDATE torrents SET enhancement_ready = false WHERE info_hash = ?", hash).Error,
	)

	var torrent model.Torrent
	require.NoError(t, db.Gorm.Where("info_hash=?", hash).First(&torrent).Error)
	tc := model.TorrentContent{InfoHash: hash}
	rollback := errors.New("rollback classification")
	require.ErrorIs(t, db.Gorm.Transaction(func(tx *gorm.DB) error {
		if err := LockTorrent(tx, hash); err != nil {
			return err
		}

		if err := RecordOrdinary(tx, torrent, &tc, "rollback-recipe", false); err != nil {
			return err
		}

		return rollback
	}), rollback)
	require.NoError(
		t,
		db.Gorm.Raw("SELECT enhancement_ready FROM torrents WHERE info_hash=?", hash).Scan(&ready).Error,
	)
	require.False(t, ready, "a rolled back classification must not hand off work")
	require.NoError(t, db.Gorm.Transaction(func(tx *gorm.DB) error {
		if err := LockTorrent(tx, hash); err != nil {
			return err
		}

		return RecordOrdinary(tx, torrent, &tc, "committed-recipe", false)
	}))
	require.NoError(
		t,
		db.Gorm.Raw("SELECT enhancement_ready FROM torrents WHERE info_hash=?", hash).Scan(&ready).Error,
	)
	require.True(t, ready)

	var count int64
	require.NoError(
		t,
		db.Gorm.Table("classification_evaluations").
			Where("info_hash = ? AND stage = 'ordinary'", hash).
			Count(&count).
			Error,
	)
	require.EqualValues(t, 1, count)
}

func automaticConfig() Config {
	c := NewDefaultConfig()
	c.Automatic = true
	c.DailyBudget = 1
	c.InputCostPerMillion = 1
	c.OutputCostPerMillion = 1
	c.BaseURL = "https://example.invalid/v1"
	c.APIKey = "test"
	c.Model = "test"

	return c
}

func TestSchedulerFairRankedClaimsAndBorrowing(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	c := automaticConfig()
	c.Concurrency = 6
	now := time.Now().UTC()

	for i, row := range []struct {
		days    int
		seeders *uint
	}{{1, ptr(uint(2))}, {20, ptr(uint(9))}, {1, nil}, {20, ptr(uint(3))}, {2, ptr(uint(2))}, {20, nil}} {
		h := protocol.ID{byte(i + 1)}
		require.NoError(
			t,
			db.Gorm.Create(
				&model.Torrent{
					InfoHash:    h,
					Name:        "populated",
					CreatedAt:   now.Add(-time.Duration(row.days) * 24 * time.Hour),
					FilesStatus: model.FilesStatusSingle,
				},
			).Error,
		)
		require.NoError(
			t,
			db.Gorm.Create(
				&model.TorrentContent{InfoHash: h, InferredTitle: "Existing title", InferredYear: 2000},
			).Error,
		)

		if row.seeders != nil {
			require.NoError(t, db.Gorm.Create(&model.TorrentSource{Key: fmt.Sprint(i)}).Error)
			require.NoError(
				t,
				db.Gorm.Create(
					&model.TorrentsTorrentSource{
						InfoHash: h,
						Source:   fmt.Sprint(i),
						Seeders:  model.NewNullUint(*row.seeders),
					},
				).Error,
			)
		}

		require.NoError(
			t,
			db.Gorm.Exec("UPDATE torrents SET enhancement_ready=true WHERE info_hash=?", h).Error,
		)
	}

	s := NewScheduler(db.Gorm, c, nil, nil)
	for _, expected := range []byte{5, 2, 1, 4, 3, 6} {
		claim, err := s.Claim(t.Context())
		require.NoError(t, err)
		require.Equal(t, protocol.ID{expected}, claim.InfoHash)
	}

	claim, err := s.Claim(t.Context())
	require.NoError(t, err)
	require.Empty(t, claim.Token, "cross-process capacity enforced")
}
func ptr[T any](v T) *T { return &v }

func TestSharedBudgetConservativelyReservesAndReconciles(t *testing.T) {
	t.Parallel()
	db, _, hash := applicationFixture(t)
	c := automaticConfig()
	s := NewScheduler(db.Gorm, c, nil, nil)
	claim, err := s.Claim(t.Context())
	require.NoError(t, err)
	require.Equal(t, hash, claim.InfoHash)
	a := &accounting{db: db.Gorm, config: c, claim: claim.Token}

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		tokens []string
	)

	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			token, e := a.reserve(t.Context(), 0.3)
			if e == nil {
				mu.Lock()

				tokens = append(tokens, token)
				mu.Unlock()
			} else {
				assert.ErrorIs(t, e, ErrBudget)
			}
		}()
	}

	wg.Wait()
	require.Len(t, tokens, 3)

	actual := 0.1
	require.NoError(t, a.settle(t.Context(), tokens[0], &actual, 200))
	require.NoError(t, a.settle(t.Context(), tokens[0], &actual, 200))

	var spent float64
	require.NoError(t, db.Gorm.Raw("SELECT charged FROM enhancement_daily_spending").Scan(&spent).Error)
	require.InDelta(t, 0.7, spent, 1e-9)
	// Unknown completion retains the full reservation, including across restarts.
	require.NoError(t, a.settle(t.Context(), tokens[1], nil, 0))
	_, err = a.reserve(t.Context(), 0.31)
	require.ErrorIs(t, err, ErrBudget)
	require.NoError(t, a.settle(t.Context(), tokens[2], nil, 401))
	_, err = a.reserve(t.Context(), 0.01)
	require.ErrorIs(t, err, ErrPaused)
}

func TestAutomaticRequiresExplicitBudget(t *testing.T) {
	t.Parallel()

	c := automaticConfig()
	c.DailyBudget = 0
	require.Error(t, c.Validate())
	c.Automatic = false
	require.NoError(t, c.Validate())
}

func TestAutomaticEvaluatorChargesRetriesAndSuppressesRestart(t *testing.T) {
	t.Parallel()
	db, _, hash := applicationFixture(t)

	var calls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		_, _ = w.Write(
			[]byte(
				`{
  "choices": [
    {
      "message": {
        "role": "assistant",
        "content": "{}"
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 100,
    "completion_tokens": 20,
    "completion_tokens_details": {
      "reasoning_tokens": 50
    }
  }
}`,
			),
		)
	}))
	defer server.Close()

	c := automaticConfig()
	c.BaseURL = server.URL
	engine, err := NewEvaluator(c, nil, server.Client())
	require.NoError(t, err)

	s := NewScheduler(db.Gorm, c, NewApplication(db.Gorm, engine, nil, c), nil)
	claim, err := s.Claim(t.Context())
	require.NoError(t, err)
	result, err := s.Process(t.Context(), claim)
	require.NoError(t, err)
	require.Equal(t, OutcomeUnresolved, result.Outcome)
	require.EqualValues(t, 2, calls.Load())

	var spent float64
	require.NoError(t, db.Gorm.Raw("SELECT charged FROM enhancement_daily_spending").Scan(&spent).Error)
	require.InDelta(
		t,
		0.01015,
		spent,
		1e-9,
		"unknown first request retains 8000 input + 2000 output; reasoning included in second",
	)
	require.NoError(t, db.Gorm.Exec("UPDATE torrents SET enhancement_ready=true WHERE info_hash=?", hash).Error)
	claim, err = s.Claim(t.Context())
	require.NoError(t, err)
	_, err = s.Process(t.Context(), claim)
	require.ErrorIs(t, err, ErrEvaluated)
	require.EqualValues(t, 2, calls.Load())
}

func TestCrashAfterRequestPreservesCapsAndUnknownCharge(t *testing.T) {
	t.Parallel()
	db, app, hash := applicationFixture(t)
	c := automaticConfig()
	s := NewScheduler(db.Gorm, c, app, nil)
	claim, err := s.Claim(t.Context())
	require.NoError(t, err)
	ctx := automaticContext(t.Context(), &accounting{db: db.Gorm, config: c, claim: claim.Token})
	run, err := app.Prepare(ctx, hash, false, false)
	require.NoError(t, err)
	require.NoError(
		t,
		db.Gorm.Exec(
			`UPDATE enhancement_claims SET generation=?,input_fingerprint=?,recipe_fingerprint=? WHERE token=?`,
			run.generation,
			run.InputFingerprint,
			run.recipeFingerprint,
			claim.Token,
		).Error,
	)

	a := automaticAccounting(ctx)
	_, err = a.reserve(ctx, 0.25)
	require.NoError(t, err)
	require.NoError(
		t,
		db.Gorm.Exec(
			"UPDATE enhancement_claims SET expires_at=now()-interval '1 second' WHERE token=?",
			claim.Token,
		).Error,
	)
	next, err := s.Claim(t.Context())
	require.NoError(t, err)
	require.Empty(t, next.Token)
	_, err = app.Prepare(t.Context(), hash, false, false)
	require.ErrorIs(t, err, ErrEvaluated)

	var state model.ClassificationEvaluation
	require.NoError(t, db.Gorm.Where("info_hash=? AND stage='enhancement'", hash).First(&state).Error)
	require.Equal(t, OutcomeLimited, state.Outcome)

	var spent float64
	require.NoError(t, db.Gorm.Raw("SELECT charged FROM enhancement_daily_spending").Scan(&spent).Error)
	require.InDelta(t, 0.25, spent, 1e-9)
}

func TestAuthenticationPauseSurvivesRestartAndResumeRetries(t *testing.T) {
	t.Parallel()
	db, _, _ := applicationFixture(t)

	var calls atomic.Int32

	server := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusUnauthorized) },
		),
	)
	defer server.Close()

	c := automaticConfig()
	c.BaseURL = server.URL
	engine, err := NewEvaluator(c, nil, server.Client())
	require.NoError(t, err)

	s := NewScheduler(db.Gorm, c, NewApplication(db.Gorm, engine, nil, c), nil)
	claim, err := s.Claim(t.Context())
	require.NoError(t, err)
	_, err = s.Process(t.Context(), claim)
	require.NoError(t, err)
	_, err = s.Claim(t.Context())
	require.ErrorIs(t, err, ErrPaused)
	require.NoError(t, s.Resume(t.Context()))
	claim, err = s.Claim(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, claim.Token)
	_, err = s.Process(t.Context(), claim)
	require.NoError(t, err)
	require.EqualValues(t, 2, calls.Load())
}

func TestSchedulerSelectionUsesRankIndexAtFixtureScale(t *testing.T) {
	t.Parallel()
	db := dbtest.NewSeeded(t)
	require.NoError(t, db.Gorm.Exec("ANALYZE torrents").Error)

	for _, recent := range []bool{true, false} {
		t.Run(fmt.Sprint(recent), func(t *testing.T) {
			t.Parallel()

			rows, err := db.Gorm.Raw("EXPLAIN " + selectionQuery(recent)).Rows()
			require.NoError(t, err)

			defer rows.Close()

			var plan strings.Builder

			for rows.Next() {
				var line string
				require.NoError(t, rows.Scan(&line))
				plan.WriteString(line)
				plan.WriteByte('\n')
			}

			require.NoError(t, rows.Err())
			t.Logf("recent=%v\n%s", recent, plan.String())
			require.Contains(t, plan.String(), "torrents_enhancement_rank")
			require.NotContains(t, plan.String(), "Seq Scan on torrents")
		})
	}
}

func TestLegacyClassificationsAreReprocessedBeforeEnhancement(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	h := protocol.ID{89}
	torrent := model.Torrent{InfoHash: h, Name: "legacy", FilesStatus: model.FilesStatusSingle}
	require.NoError(t, db.Gorm.Create(&torrent).Error)

	tc := model.TorrentContent{
		InfoHash:      h,
		InferredTitle: "Existing populated metadata",
		ContentType:   model.NewNullContentType(model.ContentTypeMovie),
	}
	require.NoError(t, db.Gorm.Create(&tc).Error)
	require.NoError(t, db.Gorm.Exec("UPDATE torrents SET enhancement_ready=true WHERE info_hash=?", h).Error)

	c := automaticConfig()

	var classified int

	app := NewApplication(
		db.Gorm,
		engineFunc(
			func(context.Context, Evidence) Evaluation { return Evaluation{Outcome: OutcomeUnresolved} },
		),
		nil,
		c,
	)
	s := NewScheduler(db.Gorm, c, app, func(ctx context.Context, hash protocol.ID) error {
		classified++

		require.Equal(t, h, hash)

		return db.Gorm.WithContext(ctx).
			Transaction(func(tx *gorm.DB) error {
				return RecordOrdinary(tx, torrent, &tc, "real-classifier-recipe", false)
			})
	})
	claim, err := s.Claim(t.Context())
	require.NoError(t, err)
	result, err := s.Process(t.Context(), claim)
	require.NoError(t, err)
	require.Equal(t, OutcomeUnresolved, result.Outcome)
	require.Equal(t, 1, classified)
}

type engineFunc func(context.Context, Evidence) Evaluation

func (f engineFunc) Evaluate(ctx context.Context, e Evidence) Evaluation { return f(ctx, e) }

func TestSchedulerRefreshesSourceMaximumBoundaryAndBorrows(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	c := automaticConfig()
	c.Concurrency = 4

	old := time.Now().Add(-8 * 24 * time.Hour)
	for _, h := range []protocol.ID{{1}, {2}, {3}} {
		require.NoError(
			t,
			db.Gorm.Create(
				&model.Torrent{InfoHash: h, CreatedAt: old, FilesStatus: model.FilesStatusSingle},
			).Error,
		)
		require.NoError(t, db.Gorm.Create(&model.TorrentContent{InfoHash: h}).Error)
	}

	for _, key := range []string{"one", "two"} {
		require.NoError(t, db.Gorm.Create(&model.TorrentSource{Key: key}).Error)
	}

	require.NoError(
		t,
		db.Gorm.Create(
			&model.TorrentsTorrentSource{
				InfoHash: protocol.ID{1},
				Source:   "one",
				Seeders:  model.NewNullUint(5),
			},
		).Error,
	)
	require.NoError(
		t,
		db.Gorm.Create(
			&model.TorrentsTorrentSource{
				InfoHash: protocol.ID{1},
				Source:   "two",
				Seeders:  model.NewNullUint(6),
			},
		).Error,
	)
	require.NoError(
		t,
		db.Gorm.Create(
			&model.TorrentsTorrentSource{
				InfoHash: protocol.ID{2},
				Source:   "one",
				Seeders:  model.NewNullUint(7),
			},
		).Error,
	)
	// Simulate the seven-day boundary crossing. Ranking refresh moves all three to
	// older; an empty recent group must lend every remaining slot.
	require.NoError(t, db.Gorm.Exec("UPDATE torrents SET enhancement_ready=true,enhancement_recent=true").Error)

	s := NewScheduler(db.Gorm, c, nil, nil)
	for _, expected := range []byte{2, 1, 3} {
		claim, err := s.Claim(t.Context())
		require.NoError(t, err)
		require.Equal(t, protocol.ID{expected}, claim.InfoHash)
	}
}

func TestSharedBudgetDayRolloverSettlesOriginalDay(t *testing.T) {
	t.Parallel()
	db, _, _ := applicationFixture(t)
	c := automaticConfig()
	s := NewScheduler(db.Gorm, c, nil, nil)
	claim, err := s.Claim(t.Context())
	require.NoError(t, err)

	a := &accounting{db: db.Gorm, config: c, claim: claim.Token}
	require.NoError(
		t,
		db.Gorm.Exec(
			`INSERT INTO enhancement_daily_spending(day,charged) VALUES ((now() AT TIME ZONE 'UTC')::date-1,0.9)`,
		).Error,
	)
	require.NoError(
		t,
		db.Gorm.Exec(
			`INSERT INTO enhancement_reservations(token,day,cost,expires_at) VALUES ('yesterday',
(now() AT TIME ZONE 'UTC')::date-1,0.9,now()+interval '1 hour')`,
		).Error,
	)
	_, err = a.reserve(t.Context(), 0.8)
	require.NoError(t, err)

	actual := 0.2
	require.NoError(t, a.settle(t.Context(), "yesterday", &actual, 200))

	var today, yesterday float64
	require.NoError(
		t,
		db.Gorm.Raw("SELECT charged FROM enhancement_daily_spending WHERE day=(now() AT TIME ZONE 'UTC')::date").
			Scan(&today).
			Error,
	)
	require.NoError(
		t,
		db.Gorm.Raw("SELECT charged FROM enhancement_daily_spending WHERE day=(now() AT TIME ZONE 'UTC')::date-1").
			Scan(&yesterday).
			Error,
	)
	require.InDelta(t, 0.8, today, 1e-9)
	require.InDelta(t, 0.2, yesterday, 1e-9)
}

func TestExpiredClaimCannotApplyLateMetadata(t *testing.T) {
	t.Parallel()
	db, app, hash := applicationFixture(t)
	c := automaticConfig()
	s := NewScheduler(db.Gorm, c, app, nil)
	claim, err := s.Claim(t.Context())
	require.NoError(t, err)
	ctx := automaticContext(t.Context(), &accounting{db: db.Gorm, config: c, claim: claim.Token})
	run, err := app.Prepare(ctx, hash, false, false)
	require.NoError(t, err)
	require.NoError(
		t,
		db.Gorm.Exec(
			"UPDATE enhancement_claims SET expires_at=now()-interval '1 second' WHERE token=?",
			claim.Token,
		).Error,
	)
	require.ErrorIs(
		t,
		app.Complete(
			ctx,
			run,
			Evaluation{Outcome: OutcomeAccepted, Result: &Metadata{Title: "Late write"}},
			nil,
		),
		ErrLease,
	)

	var tc model.TorrentContent
	require.NoError(t, db.Gorm.Where("info_hash=?", hash).First(&tc).Error)
	require.Empty(t, tc.InferredTitle)
}

func TestHiddenPriorityPrefixIsDiscardedInBoundedPages(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	c := automaticConfig()

	for i := range 70 {
		h := protocol.ID{byte(i + 1)}
		require.NoError(
			t,
			db.Gorm.Create(
				&model.Torrent{InfoHash: h, Name: "hidden", FilesStatus: model.FilesStatusSingle},
			).Error,
		)
		require.NoError(t, db.Gorm.Create(&model.TorrentContent{InfoHash: h}).Error)
		require.NoError(
			t,
			db.Gorm.Exec("UPDATE torrents SET enhancement_ready=true WHERE info_hash=?", h).Error,
		)
		require.NoError(
			t,
			db.Gorm.Exec(
				`INSERT INTO moderation_decisions(identifier,status,origin,created_at,updated_at) VALUES (?,
'pending' ,'automatic',now(),now())`,
				moderation.IdentifierForInfoHash(h).String(),
			).Error,
		)
	}

	s := NewScheduler(db.Gorm, c, nil, nil)
	claim, err := s.Claim(t.Context())
	require.NoError(t, err)
	require.Empty(t, claim.Token)

	var ready int64
	require.NoError(t, db.Gorm.Table("torrents").Where("enhancement_ready").Count(&ready).Error)
	require.EqualValues(t, 6, ready, "selection must consume at most one page of hidden candidates")
}

func TestSchedulerFinishesBoundaryCatchupBeforeAdmission(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	c := automaticConfig()

	for i := range 65 {
		h := protocol.ID{byte(i + 1)}
		require.NoError(
			t,
			db.Gorm.Create(
				&model.Torrent{
					InfoHash:    h,
					CreatedAt:   time.Now().Add(-8 * 24 * time.Hour),
					FilesStatus: model.FilesStatusSingle,
				},
			).Error,
		)
	}

	require.NoError(t, db.Gorm.Exec("UPDATE torrents SET enhancement_ready=true,enhancement_recent=true").Error)
	s := NewScheduler(db.Gorm, c, nil, nil)
	claim, err := s.Claim(t.Context())
	require.NoError(t, err)
	require.Empty(t, claim.Token, "do not rank an incomplete older group")
	claim, err = s.Claim(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, claim.Token)
}

func TestCompletionCannotEraseConcurrentOrdinaryHandoff(t *testing.T) {
	t.Parallel()
	db, _, hash := applicationFixture(t)
	c := automaticConfig()
	app := NewApplication(
		db.Gorm,
		engineFunc(
			func(context.Context, Evidence) Evaluation { return Evaluation{Outcome: OutcomeUnresolved} },
		),
		nil,
		c,
	)
	s := NewScheduler(db.Gorm, c, app, nil)
	claim, err := s.Claim(t.Context())
	require.NoError(t, err)
	locked := db.Gorm.WithContext(t.Context()).Begin()
	require.NoError(t, locked.Error)
	require.NoError(t, schedulerLock(locked))
	t.Cleanup(func() { _ = locked.Rollback().Error })

	done := make(chan error, 1)

	go func() { _, e := s.Process(t.Context(), claim); done <- e }()

	require.Eventually(t, func() bool {
		var outcome string

		return db.Gorm.Raw(`SELECT outcome FROM classification_evaluations
 WHERE info_hash=? AND stage='enhancement'`, hash).
			Scan(&outcome).
			Error == nil &&
			outcome == OutcomeUnresolved
	}, 5*time.Second, 10*time.Millisecond)

	var torrent model.Torrent
	require.NoError(t, locked.Where("info_hash=?", hash).First(&torrent).Error)
	tc := model.TorrentContent{InfoHash: hash, InferredTitle: "New ordinary evidence"}
	require.NoError(t, LockTorrent(locked, hash))
	require.NoError(t, RecordOrdinary(locked, torrent, &tc, "new-ordinary-recipe", false))
	require.NoError(t, locked.Commit().Error)
	require.NoError(t, <-done)

	var ready bool
	require.NoError(
		t,
		db.Gorm.Raw("SELECT enhancement_ready FROM torrents WHERE info_hash=?", hash).Scan(&ready).Error,
	)
	require.True(t, ready, "finishing an older run must preserve a concurrent durable handoff")
}

func TestLateWorkerPreservesExpiredClaimForChargedRecovery(t *testing.T) {
	t.Parallel()
	db, _, hash := applicationFixture(t)
	c := automaticConfig()
	app := NewApplication(db.Gorm, engineFunc(func(ctx context.Context, _ Evidence) Evaluation {
		a := automaticAccounting(ctx)
		if _, err := a.reserve(ctx, 0.1); err != nil {
			return Evaluation{Outcome: OutcomeProviderFailure}
		}

		_ = db.Gorm.Exec(
			"UPDATE enhancement_claims SET expires_at=now()-interval '1 second' WHERE token=?",
			a.claim,
		).Error

		return Evaluation{Outcome: OutcomeUnresolved}
	}), nil, c)
	s := NewScheduler(db.Gorm, c, app, nil)
	claim, err := s.Claim(t.Context())
	require.NoError(t, err)
	_, err = s.Process(t.Context(), claim)
	require.ErrorIs(t, err, ErrLease)

	var claims int64
	require.NoError(t, db.Gorm.Table("enhancement_claims").Count(&claims).Error)
	require.EqualValues(t, 1, claims, "charged expired claim must remain available to conservative recovery")
	claim, err = s.Claim(t.Context())
	require.NoError(t, err)
	require.Empty(t, claim.Token)

	var state model.ClassificationEvaluation
	require.NoError(t, db.Gorm.Where("info_hash=? AND stage='enhancement'", hash).First(&state).Error)
	require.Equal(t, OutcomeLimited, state.Outcome)
}

func TestBudgetStopAfterRequestCompletesLimitedWithoutRestartingCaps(t *testing.T) {
	t.Parallel()
	db, _, hash := applicationFixture(t)
	c := automaticConfig()
	c.DailyBudget = 0.01

	var calls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)

		_, _ = w.Write(
			[]byte(
				`{
  "choices": [
    {
      "message": {
        "role": "assistant",
        "content": "{\"year\":4000}"
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 8000,
    "completion_tokens": 1000
  }
}`,
			),
		)
	}))
	defer server.Close()

	c.BaseURL = server.URL
	engine, err := NewEvaluator(c, nil, server.Client())
	require.NoError(t, err)

	s := NewScheduler(db.Gorm, c, NewApplication(db.Gorm, engine, nil, c), nil)
	claim, err := s.Claim(t.Context())
	require.NoError(t, err)
	result, err := s.Process(t.Context(), claim)
	require.NoError(t, err)
	require.Equal(t, OutcomeLimited, result.Outcome)
	require.NoError(t, db.Gorm.Exec("UPDATE torrents SET enhancement_ready=true WHERE info_hash=?", hash).Error)
	claim, err = s.Claim(t.Context())
	require.NoError(t, err)
	_, err = s.Process(t.Context(), claim)
	require.ErrorIs(t, err, ErrEvaluated)
	require.EqualValues(t, 1, calls.Load())
}

func TestCompletionStorageFailureCannotResetChargedCaps(t *testing.T) {
	t.Parallel()
	db, _, hash := applicationFixture(t)
	c := automaticConfig()

	require.NoError(
		t,
		db.Gorm.Exec(`CREATE FUNCTION fail_enhancement_completion() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF NEW.stage='enhancement' AND NEW.attempt_outcome='unresolved' THEN
 RAISE EXCEPTION 'injected completion write failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER fail_enhancement_completion BEFORE UPDATE ON classification_evaluations
 FOR EACH ROW EXECUTE FUNCTION fail_enhancement_completion()`).Error,
	)
	app := NewApplication(db.Gorm, engineFunc(func(ctx context.Context, _ Evidence) Evaluation {
		if _, err := automaticAccounting(ctx).reserve(ctx, 0.1); err != nil {
			return Evaluation{Outcome: OutcomeProviderFailure}
		}

		return Evaluation{Outcome: OutcomeUnresolved}
	}), nil, c)
	s := NewScheduler(db.Gorm, c, app, nil)
	claim, err := s.Claim(t.Context())
	require.NoError(t, err)
	_, err = s.Process(t.Context(), claim)
	require.Error(t, err)

	var state model.ClassificationEvaluation
	require.NoError(t, db.Gorm.Where("info_hash=? AND stage='enhancement'", hash).First(&state).Error)
	require.Equal(t, OutcomeLimited, state.Outcome, "a failed completion must not give the paid attempt fresh caps")
}
