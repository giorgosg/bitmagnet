package enhancement_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/enhancement"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/tmdb"
	"github.com/stretchr/testify/require"
)

func evaluator(
	t *testing.T,
	h http.HandlerFunc,
	change func(*enhancement.Config),
) (*enhancement.Evaluator, *atomic.Int32) {
	t.Helper()

	calls := new(atomic.Int32)
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) { calls.Add(1); h(w, request) }),
	)
	t.Cleanup(server.Close)

	c := enhancement.NewDefaultConfig()
	c.BaseURL = server.URL
	c.Model = "fake"

	c.APIKey = "fake"
	if change != nil {
		change(&c)
	}

	e, err := enhancement.NewEvaluator(
		c,
		lazy.New(func() (tmdb.Client, error) { panic("unexpected catalogue access") }),
		http.DefaultClient,
	)
	require.NoError(t, err)

	return e, calls
}

func answer(w http.ResponseWriter, content string, output, reasoning int) {
	if err := json.NewEncoder(w).
		Encode(map[string]any{
			"choices": []any{
				map[string]any{
					"message": map[string]string{
						"role": "assistant", "content": content,
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens": 100, "completion_tokens": output,
				"completion_tokens_details": map[string]int{
					"reasoning_tokens": reasoning,
				},
			},
		}); err != nil {
		panic(err)
	}
}

func TestMalformedTransportEnvelopeGetsOnlyOneRetry(t *testing.T) {
	t.Parallel()
	e, calls := evaluator(
		t,
		func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{broken`)) },
		nil,
	)
	result := e.Evaluate(t.Context(), enhancement.Evidence{Name: "Unknown"})
	require.Equal(t, "malformed", result.Outcome)
	require.EqualValues(t, 2, calls.Load())
}

func TestRejectsInvalidStructuredMetadata(t *testing.T) {
	t.Parallel()

	for name, content := range map[string]string{
		"invented movie":    `{"type":"movie","work":{"type":"movie","id":99}}`,
		"episode ID":        `{"type":"tv_show","work":{"type":"episode","id":99}}`,
		"unknown type":      `{"type":"documentary"}`,
		"year range":        `{"year":9999}`,
		"unknown language":  `{"languages":["zz"]}`,
		"invalid codec":     `{"videoCodec":"potato"}`,
		"non-whitelist tag": `{"tags":["llm-made-up"]}`,
		"confidence":        `{"confidence":0.99}`,
		"justification":     `{"justification":"because"}`,
		"unknown field":     `{"author":"somebody"}`,
		"mixed work":        `{"mixedWorks":true,"type":"movie","work":{"type":"movie","id":42}}`,
		"invalid season":    `{"episodes":{"-1":[1]}}`,
		"invalid episode":   `{"episodes":{"2":[0]}}`,
		"null result":       `null`,
		"trailing result":   `{} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e, calls := evaluator(
				t,
				func(w http.ResponseWriter, _ *http.Request) { answer(w, content, 20, 10) },
				nil,
			)
			result := e.Evaluate(t.Context(), enhancement.Evidence{Name: "Unknown"})
			require.Equal(t, "malformed", result.Outcome)
			require.Nil(t, result.Result)
			require.EqualValues(t, 2, calls.Load())
		})
	}
}

func TestUnknownAndMixedWorksRetainUsefulReleaseMetadata(t *testing.T) {
	t.Parallel()

	for _, content := range []string{
		`{}`,
		`{"type":"movie","mixedWorks":true,"languages":["en"],"videoCodec":"x265"}`,
		`{"type":"software","tags":["llm-platform-linux"]}`,
		`{"type":"tv_show","episodes":{"1":[],"2":[1,2,3]}}`,
	} {
		e, _ := evaluator(t, func(w http.ResponseWriter, _ *http.Request) { answer(w, content, 20, 5) }, nil)
		result := e.Evaluate(t.Context(), enhancement.Evidence{Name: "A pack"})

		want := "accepted"
		if content == `{}` {
			want = "unresolved"
		}

		require.Equal(t, want, result.Outcome)
		require.Nil(t, result.Result.Work)
	}
}

func TestBilledReasoningAndUnknownUsageExhaustLimits(t *testing.T) {
	t.Parallel()
	t.Run("reasoning included once", func(t *testing.T) {
		t.Parallel()
		e, _ := evaluator(
			t,
			func(w http.ResponseWriter, _ *http.Request) { answer(w, `{}`, 80, 70) },
			func(c *enhancement.Config) { c.MaxOutputTokens = 100; c.RequestOutputTokens = 100 },
		)
		r := e.Evaluate(t.Context(), enhancement.Evidence{})
		require.Equal(t, "unresolved", r.Outcome)
		require.Equal(t, 80, r.Usage.OutputTokens)
		require.Equal(t, 70, r.Usage.ReasoningTokens)
	})
	t.Run("reasoning exceeds bound", func(t *testing.T) {
		t.Parallel()
		e, calls := evaluator(
			t,
			func(w http.ResponseWriter, _ *http.Request) { answer(w, `{}`, 10, 101) },
			func(c *enhancement.Config) { c.MaxOutputTokens = 100; c.RequestOutputTokens = 100 },
		)
		r := e.Evaluate(t.Context(), enhancement.Evidence{})
		require.Equal(t, "limited", r.Outcome)
		require.Nil(t, r.Result)
		require.EqualValues(t, 1, calls.Load())
	})
	t.Run("malformed consumes tokens", func(t *testing.T) {
		t.Parallel()
		e, calls := evaluator(
			t,
			func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`broken`)) },
			func(c *enhancement.Config) { c.MaxOutputTokens = 100; c.RequestOutputTokens = 100 },
		)
		r := e.Evaluate(t.Context(), enhancement.Evidence{})
		require.Equal(t, "limited", r.Outcome)
		require.True(t, r.Usage.Estimated)
		require.EqualValues(t, 1, calls.Load())
	})
}

func TestTransientFailuresAndRunBudgetBoundRequests(t *testing.T) {
	t.Parallel()

	for name, status := range map[string]int{
		"transient": 503, "authentication": 401,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e, calls := evaluator(
				t,
				func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) },
				nil,
			)
			r := e.Evaluate(t.Context(), enhancement.Evidence{})
			require.Equal(t, "provider_failure", r.Outcome)

			want := int32(3)
			if status == 401 {
				want = 1
			}

			require.Equal(t, want, calls.Load())
		})
	}

	t.Run("shared requests", func(t *testing.T) {
		t.Parallel()
		e, calls := evaluator(
			t,
			func(w http.ResponseWriter, _ *http.Request) { answer(w, `{}`, 10, 0) },
			func(c *enhancement.Config) { c.MaxRunRequests = 1 },
		)
		require.Equal(t, "unresolved", e.Evaluate(t.Context(), enhancement.Evidence{}).Outcome)
		require.Equal(t, "limited", e.Evaluate(t.Context(), enhancement.Evidence{}).Outcome)
		require.EqualValues(t, 1, calls.Load())
	})
	t.Run("spend reservation", func(t *testing.T) {
		t.Parallel()
		e, calls := evaluator(
			t,
			func(_ http.ResponseWriter, _ *http.Request) { t.Error("budget should reject before HTTP") },
			func(c *enhancement.Config) {
				c.MaxRunCost = 0.001
				c.InputCostPerMillion = 1
				c.OutputCostPerMillion = 1
			},
		)
		require.Equal(t, "limited", e.Evaluate(t.Context(), enhancement.Evidence{}).Outcome)
		require.Zero(t, calls.Load())
	})
}

func TestCancellationRejectsLateResultsAndWaitingCalls(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	release := make(chan struct{})
	e, calls := evaluator(t, func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		answer(w, `{"title":"Late"}`, 20, 0)
	}, func(c *enhancement.Config) { c.Timeout = time.Second; c.Concurrency = 1 })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.Equal(t, "limited", e.Evaluate(ctx, enhancement.Evidence{}).Outcome)
	require.Zero(t, calls.Load())

	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()

	done := make(chan enhancement.Evaluation, 1)
	go func() { done <- e.Evaluate(ctx, enhancement.Evidence{}) }()

	<-entered
	cancel()

	result := <-done

	close(release)
	require.Equal(t, "limited", result.Outcome)
	require.Nil(t, result.Result)
	require.EqualValues(t, 1, calls.Load())
}

func TestConcurrentEvaluationsShareBudgetReservation(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	e, calls := evaluator(
		t,
		func(w http.ResponseWriter, _ *http.Request) { entered <- struct{}{}; <-release; answer(w, `{}`, 10, 0) },
		func(c *enhancement.Config) { c.MaxRunRequests = 1; c.Concurrency = 2 },
	)

	done := make(chan enhancement.Evaluation, 1)
	go func() { done <- e.Evaluate(t.Context(), enhancement.Evidence{}) }()

	<-entered
	require.Equal(t, "limited", e.Evaluate(t.Context(), enhancement.Evidence{}).Outcome)
	close(release)
	require.Equal(t, "unresolved", (<-done).Outcome)
	require.EqualValues(t, 1, calls.Load())
}

func TestConfiguredOutputCapParameterIsSentWithoutFallback(t *testing.T) {
	t.Parallel()
	e, _ := evaluator(t, func(w http.ResponseWriter, r *http.Request) {
		var request map[string]json.RawMessage

		err := json.NewDecoder(r.Body).Decode(&request)
		if err != nil {
			t.Error(err)
		}

		if string(request["max_tokens"]) != "2000" {
			t.Error("configured output cap was not sent")
		}

		if _, ok := request["max_completion_tokens"]; ok {
			t.Error("unexpected alternate output parameter")
		}

		answer(w, `{}`, 10, 0)
	}, func(c *enhancement.Config) { c.OutputTokenParameter = "max_tokens" })
	require.Equal(t, "unresolved", e.Evaluate(t.Context(), enhancement.Evidence{}).Outcome)
}

func TestProviderThinkingOverrideDoesNotReplaceBudgetCap(t *testing.T) {
	t.Parallel()
	e, _ := evaluator(t, func(w http.ResponseWriter, r *http.Request) {
		var request map[string]json.RawMessage

		err := json.NewDecoder(r.Body).Decode(&request)
		if err != nil {
			t.Error(err)
		}

		if string(request["thinking"]) != `{"type":"disabled"}` {
			t.Error("thinking override missing")
		}

		if string(request["max_completion_tokens"]) != "2000" {
			t.Error("output budget cap missing")
		}

		answer(w, `{}`, 10, 0)
	}, func(c *enhancement.Config) { c.Thinking = "disabled" })
	require.Equal(t, "unresolved", e.Evaluate(t.Context(), enhancement.Evidence{}).Outcome)
}
