package enhancement

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/tmdb"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestTerminalUnknownUsageRetiresReservationWithoutRefund(t *testing.T) {
	t.Parallel()

	for _, scenario := range []string{"missing_usage", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			db, _, _ := applicationFixture(t)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario == "timeout" {
					_, _ = io.Copy(io.Discard, r.Body)
					select {
					case <-r.Context().Done():
					case <-time.After(time.Second):
					}

					return
				}

				_, _ = w.Write(
					[]byte(
						`{"choices":[{"message":{"role":"assistant","content":"{}"},"finish_reason":"stop"}]} `,
					),
				)
			}))
			defer server.Close()

			c := automaticConfig()

			c.BaseURL = server.URL
			if scenario == "timeout" {
				c.Timeout = 100 * time.Millisecond
			}

			engine, err := NewEvaluator(c, nil, server.Client())
			require.NoError(t, err)

			s := NewScheduler(db.Gorm, c, NewApplication(db.Gorm, engine, nil, c), nil)
			claim, err := s.Claim(t.Context())
			require.NoError(t, err)
			_, _ = s.Process(t.Context(), claim)

			var count int64
			require.NoError(t, db.Gorm.Table("enhancement_reservations").Count(&count).Error)
			require.Zero(t, count, "terminal uncertainty is retained in daily charges, not orphan tokens")

			var charged float64
			require.NoError(
				t,
				db.Gorm.Raw("SELECT charged FROM enhancement_daily_spending").Scan(&charged).Error,
			)
			require.InDelta(t, 0.01, charged, 1e-9)
		})
	}
}

func TestTerminalUnknownCannotBeRefundedByDuplicateLateSettlement(t *testing.T) {
	t.Parallel()
	db, _, _ := applicationFixture(t)
	c := automaticConfig()
	s := NewScheduler(db.Gorm, c, nil, nil)
	claim, err := s.Claim(t.Context())
	require.NoError(t, err)

	a := &accounting{db: db.Gorm, config: c, claim: claim.Token}
	for range 20 {
		token, err := a.reserve(t.Context(), 0.01)
		require.NoError(t, err)
		require.NoError(t, a.settle(t.Context(), token, nil, 200))

		actual := 0.0
		require.NoError(t, a.settle(t.Context(), token, &actual, 200))
	}

	var count int64
	require.NoError(t, db.Gorm.Table("enhancement_reservations").Count(&count).Error)
	require.Zero(t, count)

	var charged float64
	require.NoError(t, db.Gorm.Raw("SELECT charged FROM enhancement_daily_spending").Scan(&charged).Error)
	require.InDelta(t, 0.2, charged, 1e-9)
}

func TestCrashReservationsRetireInBoundedPagesWithoutRefund(t *testing.T) {
	t.Parallel()
	db, _, _ := applicationFixture(t)
	c := automaticConfig()
	s := NewScheduler(db.Gorm, c, nil, nil)
	claim, err := s.Claim(t.Context())
	require.NoError(t, err)

	a := &accounting{db: db.Gorm, config: c, claim: claim.Token}

	var expired string

	for range 150 {
		token, e := a.reserve(t.Context(), 0.001)
		require.NoError(t, e)

		expired = token
	}

	require.NoError(
		t,
		db.Gorm.Exec("UPDATE enhancement_reservations SET expires_at=now()-interval '1 minute'").Error,
	)
	live, err := a.reserve(t.Context(), 0.2)
	require.NoError(t, err)
	require.NoError(t, db.Gorm.Exec("DELETE FROM enhancement_claims").Error)

	actual := 0.0
	require.NoError(t, a.settle(t.Context(), expired, &actual, 200))

	for _, want := range []int64{87, 23, 1} {
		_, _ = s.Claim(t.Context())

		var count int64
		require.NoError(t, db.Gorm.Table("enhancement_reservations").Count(&count).Error)
		require.Equal(t, want, count)
	}

	actual = 0.1
	require.NoError(t, a.settle(t.Context(), live, &actual, 200))
	require.NoError(t, a.settle(t.Context(), live, &actual, 200))

	var charged float64
	require.NoError(t, db.Gorm.Raw("SELECT charged FROM enhancement_daily_spending").Scan(&charged).Error)
	require.InDelta(t, 0.25, charged, 1e-9)
}

func TestCatalogueAuthenticationPausesAutomaticSearchAndDetails(t *testing.T) {
	t.Parallel()

	for _, scenario := range []struct {
		stage  string
		status int
	}{{"search", 401}, {"details", 401}, {"search", 422}, {"details", 422}} {
		stage, status := scenario.stage, scenario.status
		t.Run(fmt.Sprintf("%s_%d", stage, status), func(t *testing.T) {
			t.Parallel()
			db, _, _ := applicationFixture(t)

			catalogue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")

				switch {
				case r.URL.Path == "/authentication":
					_, _ = w.Write([]byte(`{"success":true}`))
				case stage == "details" && r.URL.Path == "/search/movie":
					_, _ = w.Write(
						[]byte(
							`{"results":[{"id":42,"title":"Example","release_date":"2024-01-01"}]}`,
						),
					)
				default:
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`secret-body`))
				}
			}))
			defer catalogue.Close()

			var (
				calls   atomic.Int32
				resumed atomic.Bool
			)

			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				call := calls.Add(1)

				if resumed.Load() {
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
    "prompt_tokens": 10,
    "completion_tokens": 10
  }
}`,
						),
					)

					return
				}

				if call == 1 {
					_, _ = w.Write(
						[]byte(
							`{
  "choices": [
    {
      "message": {
        "role": "assistant",
        "tool_calls": [
          {
            "id": "s1",
            "type": "function",
            "function": {
              "name": "search_works",
              "arguments": "{\"title\":\"Example\",\"type\":\"movie\"}"
            }
          }
        ]
      },
      "finish_reason": "tool_calls"
    }
  ],
  "usage": {
    "prompt_tokens": 10,
    "completion_tokens": 10
  }
}`,
						),
					)
				} else {
					_, _ = w.Write(
						[]byte(
							`{
  "choices": [
    {
      "message": {
        "role": "assistant",
        "content": "{\"type\":\"movie\",\"work\":{\"type\":\"movie\",\"id\":42}}"
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 10,
    "completion_tokens": 10
  }
}`,
						),
					)
				}
			}))
			defer provider.Close()

			c := automaticConfig()
			c.BaseURL = provider.URL
			cat := tmdb.New(
				tmdb.Params{
					Config: tmdb.Config{
						Enabled:        true,
						BaseURL:        catalogue.URL,
						APIKey:         "secret-key",
						RateLimitBurst: 10,
					},
					Logger: zap.NewNop().Sugar(),
				},
			).Client
			engine, err := NewEvaluator(c, cat, provider.Client())
			require.NoError(t, err)
			client, err := cat.Get()
			require.NoError(t, err)

			if stage == "search" {
				readonly := engine.Evaluate(t.Context(), Evidence{Name: "Example.2024.mkv"})
				require.Equal(t, OutcomeProviderFailure, readonly.Outcome)

				var pauses int64
				require.NoError(
					t,
					db.Gorm.Table("key_values").
						Where("key = ?", "enhancement/paused").
						Count(&pauses).
						Error,
				)
				require.Zero(t, pauses)
				calls.Store(0)
			}

			s := NewScheduler(db.Gorm, c, NewApplication(db.Gorm, engine, client, c), nil)
			claim, err := s.Claim(t.Context())
			require.NoError(t, err)

			result, processErr := s.Process(t.Context(), claim)
			if processErr != nil {
				require.NotContains(t, processErr.Error(), "secret")
			}

			require.Equal(t, OutcomeProviderFailure, result.Outcome)
			require.NotContains(t, result.Error, "secret")

			if status == 401 {
				require.Equal(t, "authentication", result.Failure)
			} else {
				require.Equal(t, "configuration", result.Failure)
			}

			expected := int32(1)
			if stage == "details" {
				expected = 2
			}

			require.Equal(t, expected, calls.Load())
			_, err = NewScheduler(db.Gorm, c, nil, nil).Claim(t.Context())
			require.ErrorIs(t, err, ErrPaused)

			before := calls.Load()

			require.NoError(t, s.Resume(t.Context()))
			resumed.Store(true)

			claim, err = s.Claim(t.Context())
			require.NoError(t, err)
			_, err = s.Process(t.Context(), claim)
			require.NoError(t, err)
			require.Equal(t, before+1, calls.Load())
		})
	}
}

func TestPausedAdmissionCommitsExpiredReservationCleanup(t *testing.T) {
	t.Parallel()
	db, _, _ := applicationFixture(t)
	c := automaticConfig()

	var calls atomic.Int32

	provider := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer provider.Close()

	c.BaseURL = provider.URL
	engine, err := NewEvaluator(c, nil, provider.Client())
	require.NoError(t, err)

	s := NewScheduler(db.Gorm, c, NewApplication(db.Gorm, engine, nil, c), nil)
	claim, err := s.Claim(t.Context())
	require.NoError(t, err)

	a := &accounting{db: db.Gorm, config: c, claim: claim.Token}
	_, err = a.reserve(t.Context(), 0.01)
	require.NoError(t, err)
	require.NoError(
		t,
		db.Gorm.Exec("UPDATE enhancement_reservations SET expires_at=now()-interval '1 minute'").Error,
	)
	require.NoError(t, db.Gorm.Exec("DELETE FROM enhancement_claims").Error)
	require.NoError(t, a.pause(t.Context()))
	claim, err = s.Claim(t.Context())
	require.ErrorIs(t, err, ErrPaused)
	require.Empty(t, claim.Token)

	var count int64
	require.NoError(t, db.Gorm.Table("enhancement_reservations").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Gorm.Table("enhancement_claims").Count(&count).Error)
	require.Zero(t, count)
	require.Zero(t, calls.Load())

	var charged float64
	require.NoError(t, db.Gorm.Raw("SELECT charged FROM enhancement_daily_spending").Scan(&charged).Error)
	require.InDelta(t, 0.01, charged, 1e-9)
}
