package enhancementcmd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/app/cmd/enhancementcmd"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/database/postgres"
	"github.com/bitmagnet-io/bitmagnet/internal/enhancement"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/processor"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/bitmagnet-io/bitmagnet/internal/tmdb"
	tmdb_mocks "github.com/bitmagnet-io/bitmagnet/internal/tmdb/mocks"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
	"go.uber.org/fx/fxtest"
	"gorm.io/gorm"
)

type unusedProcessor struct{}

func (unusedProcessor) Process(context.Context, processor.MessageParams) error {
	return errors.New("unexpected ordinary classification")
}

func TestOperatorCLIPersistInspectForceAndBudget(t *testing.T) {
	t.Parallel()
	lockCLI(t)
	db := dbtest.New(t)
	hash := protocol.MustParseID(strings.Repeat("19", 20))
	torrent := model.Torrent{InfoHash: hash, Name: "Example.S02E05.1080p", FilesStatus: model.FilesStatusSingle}
	require.NoError(t, db.Gorm.Create(&torrent).Error)

	hint := model.TorrentHint{
		InfoHash:    hash,
		ContentType: model.ContentTypeMovie,
		Title:       model.NewNullString("Supplied title"),
	}
	require.NoError(t, db.Gorm.Create(&hint).Error)
	torrent.Hint = hint
	tc := model.TorrentContent{
		InfoHash:      hash,
		ContentType:   model.NewNullContentType(model.ContentTypeMovie),
		InferredTitle: "Supplied title",
	}
	require.NoError(t, db.Gorm.Create(&tc).Error)
	require.NoError(
		t,
		db.Gorm.Transaction(
			func(tx *gorm.DB) error { return enhancement.RecordOrdinary(tx, torrent, &tc, "test", false) },
		),
	)

	var (
		calls atomic.Int32
		fail  atomic.Bool
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)

		if fail.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		_ = json.NewEncoder(w).
			Encode(map[string]any{
				"choices": []any{
					map[string]any{
						"message": map[string]string{
							"role":    "assistant",
							"content": `{"title":"Example","episodes":{"2":[5]}}`,
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]int{"prompt_tokens": 200, "completion_tokens": 10},
			})
	}))
	defer server.Close()

	cfg := enhancement.NewDefaultConfig()
	cfg.BaseURL = server.URL
	cfg.APIKey = "never-output-this-secret"
	cfg.Model = "fake"
	cfg.DailyBudget = 1
	cfg.MaxRunCost = 1
	cfg.InputCostPerMillion = 1
	cfg.OutputCostPerMillion = 1
	catalogue := tmdb_mocks.NewClient(t)
	pc := postgres.NewDefaultConfig()
	pc.DSN = db.DSN
	run := func(args ...string) map[string]any {
		t.Helper()

		var output bytes.Buffer

		cmd := enhancementcmd.New(enhancementcmd.Params{
			Lifecycle: fxtest.NewLifecycle(t), Config: cfg, Postgres: pc,
			Dao:       lazy.New(func() (*dao.Query, error) { return db.Query, nil }),
			Catalogue: lazy.New(func() (tmdb.Client, error) { return catalogue, nil }),
			Processor: lazy.New(func() (processor.Processor, error) { return unusedProcessor{}, nil }),
		}).Command
		app := &cli.App{Writer: &output, Commands: []*cli.Command{cmd}}
		require.NoError(t, app.RunContext(t.Context(), append([]string{"test", "enhancement"}, args...)))
		require.NotContains(t, output.String(), cfg.APIKey)

		var report map[string]any
		require.NoError(t, json.Unmarshal(output.Bytes(), &report))

		return report
	}
	first := run("process", "--hash", hash.String())
	require.Equal(t, "accepted", first["evaluations"].([]any)[0].(map[string]any)["outcome"])
	require.EqualValues(t, 1, calls.Load())

	preview := run("preview", "--hash", hash.String())
	require.Equal(t, "unchanged_completed", preview["items"].([]any)[0].(map[string]any)["eligibility"])
	run("process", "--hash", hash.String())
	require.EqualValues(t, 1, calls.Load(), "unchanged results suppress explicit calls without force")
	run("process", "--hash", hash.String(), "--force")
	require.EqualValues(t, 2, calls.Load())

	var effective model.TorrentContent
	require.NoError(t, db.Gorm.Where("info_hash=?", hash).First(&effective).Error)
	require.Equal(t, "Supplied title", effective.InferredTitle, "force does not grant importer rematch authority")
	run("process", "--hash", hash.String(), "--rematch")
	require.EqualValues(t, 3, calls.Load())

	effective = model.TorrentContent{}
	require.NoError(t, db.Gorm.Where("info_hash=?", hash).First(&effective).Error)
	require.Equal(t, "Example", effective.InferredTitle, "only explicit rematch replaces supplied title")
	fail.Store(true)
	run("process", "--hash", hash.String(), "--force")
	require.EqualValues(t, 4, calls.Load())

	status := run("status", "--hash", hash.String())
	require.Equal(t, true, status["paused"])
	items := status["items"].([]any)
	states := items[0].(map[string]any)["evaluations"].([]any)

	var latest map[string]any

	for _, s := range states {
		if s.(map[string]any)["stage"] == "enhancement" {
			latest = s.(map[string]any)
		}
	}

	require.Equal(t, "accepted", latest["outcome"])
	require.Equal(t, "provider_failure", latest["attemptOutcome"])
	require.NotNil(t, latest["acceptedOutput"])
	require.NotEmpty(t, status["dailySpending"])
	run("process", "--hash", hash.String(), "--force")
	require.EqualValues(t, 4, calls.Load(), "persistent pause blocks explicit processing")
	run("resume")
	fail.Store(false)

	cfg.DailyBudget = 0.000001
	paused := run("process", "--hash", hash.String(), "--force")
	require.Equal(t, "deferred", paused["evaluations"].([]any)[0].(map[string]any)["outcome"])
	require.EqualValues(t, 4, calls.Load(), "manual requests cannot bypass daily spending")
}

func TestPersistentCommandCannotRaiseSharedConcurrency(t *testing.T) {
	t.Parallel()
	lockCLI(t)

	cfg := enhancement.NewDefaultConfig()
	cfg.BaseURL = "https://example.invalid/v1"
	cfg.APIKey = "fake"
	cfg.Model = "fake"
	cfg.MaxRunCost = 1
	cfg.DailyBudget = 1
	cfg.InputCostPerMillion = 1
	cfg.OutputCostPerMillion = 1
	command := enhancementcmd.New(enhancementcmd.Params{
		Lifecycle: fxtest.NewLifecycle(t),
		Config:    cfg,
		Dao: lazy.New(
			func() (*dao.Query, error) { return nil, errors.New("unexpected persistent database access") },
		),
	}).Command
	app := &cli.App{Writer: &bytes.Buffer{}, Commands: []*cli.Command{command}}
	err := app.RunContext(
		t.Context(),
		[]string{"test", "enhancement", "process", "--hash", strings.Repeat("24", 20), "--concurrency", "100"},
	)
	require.ErrorContains(t, err, "configured shared concurrency")
}
