package enhancement_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/enhancement"
	"github.com/bitmagnet-io/bitmagnet/internal/tmdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestSearchThenSelectSeries(t *testing.T) {
	t.Parallel()

	searches := 0

	catalogue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/authentication" {
			_, _ = w.Write([]byte(`{"success":true}`))
			return
		}

		searches++

		assert.Equal(t, "/search/tv", r.URL.Path)
		assert.Empty(t, r.URL.Query().Get("first_air_date_year"))

		_, _ = w.Write(
			[]byte(
				`{
  "results": [
    {
      "id": 42,
      "name": "The Show",
      "original_name": "Le Show",
      "first_air_date": "1999-01-01",
      "original_language": "fr",
      "overview": "A series"
    }
  ]
}`,
			),
		)
	}))
	defer catalogue.Close()

	calls := 0

	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++

		var req map[string]any
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))

		if calls == 1 {
			_, _ = w.Write(
				[]byte(
					`{
  "choices": [
    {
      "message": {
        "role": "assistant",
        "reasoning_content": "opaque replay fixture",
        "tool_calls": [
          {
            "id": "s1",
            "type": "function",
            "function": {
              "name": "search_works",
              "arguments": "{\"title\":\"The Show\",\"type\":\"tv_show\"}"
            }
          }
        ]
      },
      "finish_reason": "tool_calls"
    }
  ],
  "usage": {
    "prompt_tokens": 100,
    "completion_tokens": 20
  }
}`,
				),
			)
		} else {
			encoded, err := json.Marshal(req)
			assert.NoError(t, err)
			assert.Contains(t, string(encoded), "opaque replay fixture")

			_, _ = w.Write(
				[]byte(
					`{
  "choices": [
    {
      "message": {
        "role": "assistant",
        "content": "{\"type\":\"tv_show\",\"work\":{\"type\":\"tv_show\",\"id\":42},\"episodes\":{\"2\":[5]}}"
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 120,
    "completion_tokens": 40,
    "completion_tokens_details": {
      "reasoning_tokens": 30
    }
  }
}`,
				),
			)
		}
	}))
	defer model.Close()

	cfg := enhancement.NewDefaultConfig()
	cfg.BaseURL = model.URL
	cfg.APIKey = "fake"
	cfg.Model = "fake"
	tc := tmdb.New(
		tmdb.Params{
			Config: tmdb.Config{Enabled: true, BaseURL: catalogue.URL, APIKey: "fake", RateLimitBurst: 5},
			Logger: zap.NewNop().Sugar(),
		},
	).Client
	e, err := enhancement.NewEvaluator(cfg, tc, http.DefaultClient)
	require.NoError(t, err)

	result := e.Evaluate(context.Background(), enhancement.Evidence{Name: "The.Show.S02E05.2024.mkv"})
	require.Equal(t, "accepted", result.Outcome, "%+v", result)
	require.Equal(t, int64(42), result.Result.Work.ID)
	require.Empty(t, result.Result.Languages)
	require.Equal(t, 60, result.Usage.OutputTokens)
	require.Equal(t, 1, searches)
}

func TestRefinedSearchAndToolBounds(t *testing.T) {
	t.Parallel()

	for _, maxTools := range []int{1, 6} {
		t.Run(fmt.Sprint(maxTools), func(t *testing.T) {
			t.Parallel()

			searches := 0

			catalogue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")

				if r.URL.Path == "/authentication" {
					_, _ = w.Write([]byte(`{"success":true}`))
					return
				}

				searches++
				if searches == 1 {
					_, _ = w.Write(
						[]byte(
							`{
  "results": [
    {
      "id": 1,
      "title": "The Thing",
      "release_date": "1951-01-01"
    },
    {
      "id": 2,
      "title": "The Thing",
      "release_date": "1982-01-01"
    }
  ]
}`,
						),
					)
				} else {
					assert.Equal(t, "1982", r.URL.Query().Get("year"))

					_, _ = w.Write(
						[]byte(
							`{"results":[{"id":2,"title":"The Thing","release_date":"1982-01-01"}]}`,
						),
					)
				}
			}))
			defer catalogue.Close()

			calls := 0

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				if calls <= 2 {
					args := `{"title":"The Thing","type":"movie"}`
					if calls == 2 {
						args = `{"title":"The Thing","type":"movie","year":1982}`
					}

					_ = json.NewEncoder(w).
						Encode(map[string]any{
							"choices": []any{
								map[string]any{
									"message": map[string]any{
										"role": "assistant",
										"tool_calls": []any{
											map[string]any{
												"id": fmt.Sprint(
													calls,
												),
												"type": "function",
												"function": map[string]string{
													"name":      "search_works",
													"arguments": args,
												},
											},
										},
									},
									"finish_reason": "tool_calls",
								},
							},
							"usage": map[string]int{
								"prompt_tokens": 100, "completion_tokens": 50,
							},
						})
				} else {
					answer(w, `{"type":"movie","work":{"type":"movie","id":2}}`, 40, 0)
				}
			}))
			defer server.Close()

			cfg := enhancement.NewDefaultConfig()
			cfg.APIKey = "fake"
			cfg.Model = "fake"
			cfg.BaseURL = server.URL
			cfg.MaxToolCalls = maxTools
			tc := tmdb.New(
				tmdb.Params{
					Config: tmdb.Config{
						Enabled:        true,
						BaseURL:        catalogue.URL,
						APIKey:         "fake",
						RateLimitBurst: 5,
					},
					Logger: zap.NewNop().Sugar(),
				},
			).Client
			e, err := enhancement.NewEvaluator(cfg, tc, http.DefaultClient)
			require.NoError(t, err)

			result := e.Evaluate(t.Context(), enhancement.Evidence{Name: "The Thing 1982"})
			if maxTools == 1 {
				require.Equal(t, "limited", result.Outcome)
				require.Equal(t, 1, searches)
				require.Equal(t, 2, calls)
			} else {
				require.Equal(t, "accepted", result.Outcome)
				require.Equal(t, 2, searches)
				require.Equal(t, int64(2), result.Result.Work.ID)
			}
		})
	}
}
