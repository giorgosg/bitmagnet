package enhancement_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/enhancement"
	"github.com/bitmagnet-io/bitmagnet/internal/tmdb"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type sentMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// sentMessages records the messages of every model request.
func sentMessages(t *testing.T, r *http.Request, into *[][]sentMessage) {
	t.Helper()

	var request struct {
		Messages []sentMessage `json:"messages"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		t.Error(err)
	}

	*into = append(*into, request.Messages)
}

func TestSystemPromptIsSingleAndOperatorInstructionsOptional(t *testing.T) {
	t.Parallel()

	system := func(prompt string) string {
		var sent [][]sentMessage

		e, _ := evaluator(t, func(w http.ResponseWriter, r *http.Request) {
			sentMessages(t, r, &sent)
			answer(w, `{}`, 10, 0)
		}, func(c *enhancement.Config) { c.Prompt = prompt })
		require.Equal(t, "unresolved", e.Evaluate(t.Context(), enhancement.Evidence{}).Outcome)
		require.Len(t, sent, 1)

		return sent[0][0].Content
	}

	require.Empty(t, enhancement.NewDefaultConfig().Prompt)

	defaults := system("")
	require.NotContains(t, defaults, "Additional operator instructions")
	require.Contains(t, defaults, "one-year difference")
	require.Contains(t, defaults, "Never use episode zero")
	require.True(t, strings.HasSuffix(defaults, "llm-edition-collectors"), "tags close the fixed prefix")

	custom := system("custom")
	require.Equal(t, defaults+"\nAdditional operator instructions: custom", custom)
}

func TestModelEvidenceOmitsEmptyValuesButKeepsEpisodeMarkers(t *testing.T) {
	t.Parallel()

	var sent [][]sentMessage

	e, _ := evaluator(t, func(w http.ResponseWriter, r *http.Request) {
		sentMessages(t, r, &sent)
		answer(w, `{}`, 10, 0)
	}, nil)
	evidence := enhancement.Evidence{
		InfoHash: "aa",
		Name:     "Show.S01E15",
		Size:     18014398509481985,
		Classification: json.RawMessage(
			`[{"title":"Show","video3D":null,"languages":[],"episodes":{"1":{"15":{}}}}]`,
		),
		Hint: json.RawMessage(`{"title":null,"releaseGroup":"","episodes":null}`),
	}
	require.Equal(t, "unresolved", e.Evaluate(t.Context(), evidence).Outcome)
	require.JSONEq(
		t,
		`{"infoHash":"aa","name":"Show.S01E15","size":18014398509481985,"incompleteFiles":false,
		  "classification":[{"title":"Show","episodes":{"1":{"15":{}}}}]}`,
		sent[0][1].Content,
	)
	// JSONEq compares numbers as float64, so check the exact text separately.
	require.Contains(t, sent[0][1].Content, `"size":18014398509481985`)
}

func TestSearchSummariesAreTrimmed(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("o", 300)

	catalogue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/authentication" {
			_, _ = w.Write([]byte(`{"success":true}`))
			return
		}

		_, _ = w.Write([]byte(`{"results":[
  {"id":1,"title":"Same","original_title":"Same","release_date":"2001-01-01","overview":"` + long + `"},
  {"id":2,"title":"Local","original_title":"Original","release_date":"2002-01-01","overview":"short"}]}`))
	}))
	defer catalogue.Close()

	var sent [][]sentMessage

	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sentMessages(t, r, &sent)

		if len(sent) == 1 {
			toolAnswer(w, "search_works", `{"title":"Same","type":"movie"}`)
			return
		}

		answer(w, `{}`, 10, 0)
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
	require.Equal(t, "unresolved", e.Evaluate(t.Context(), enhancement.Evidence{Name: "Same"}).Outcome)
	require.Len(t, sent, 2)

	tool := sent[1][len(sent[1])-1]
	require.Equal(t, "tool", tool.Role)
	require.JSONEq(t, `[
  {"type":"movie","id":1,"title":"Same","date":"2001-01-01","overview":"`+long[:100]+`"},
  {"type":"movie","id":2,"title":"Local","originalTitle":"Original","date":"2002-01-01","overview":"short"}]`,
		tool.Content)
}
