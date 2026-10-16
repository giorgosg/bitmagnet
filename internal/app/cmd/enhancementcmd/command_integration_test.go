package enhancementcmd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/app/appfx"
	"github.com/bitmagnet-io/bitmagnet/internal/app/cli/hooks"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/database/postgres"
	"github.com/bitmagnet-io/bitmagnet/internal/enhancement"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
	"go.uber.org/fx"
)

// urfave's global help flag is mutated by RunContext. These integration tests run
// separate real CLI applications sequentially; evaluator concurrency is tested separately.
var cliAppMu sync.Mutex

func lockCLI(t *testing.T) {
	t.Helper()
	cliAppMu.Lock()
	t.Cleanup(cliAppMu.Unlock)
}

type audit struct {
	mu         sync.Mutex
	statements []string
}

func (a *audit) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.statements = append(a.statements, d.SQL)

	return ctx
}
func (*audit) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestCommandWiringNeverAttemptsWrites(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	hash := protocol.MustParseID(strings.Repeat("01", 20))
	require.NoError(
		t,
		db.Gorm.Create(
			&model.Torrent{
				InfoHash:    hash,
				Name:        "A release",
				FilesStatus: model.FilesStatusSingle,
				Size:        42,
				FilesCount:  model.NewNullUint(1),
			},
		).Error,
	)

	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "provider failure"}[failure], func(t *testing.T) {
			t.Parallel()
			lockCLI(t)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if failure {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}

				_ = json.NewEncoder(w).
					Encode(map[string]any{
						"choices": []any{
							map[string]any{
								"message": map[string]string{
									"role": "assistant", "content": "{}",
								},
								"finish_reason": "stop",
							},
						},
						"usage": map[string]int{
							"prompt_tokens": 200, "completion_tokens": 5,
						},
					})
			}))
			defer server.Close()

			cfg := enhancement.NewDefaultConfig()
			cfg.BaseURL = server.URL
			cfg.APIKey = "fake"
			cfg.Model = "fake"
			cfg.Automatic = true
			cfg.DailyBudget = 1
			cfg.MaxRunCost = 1
			cfg.InputCostPerMillion = 1
			cfg.OutputCostPerMillion = 1
			// default_transaction_read_only is installed before any application SQL.
			pc := postgres.NewDefaultConfig()
			pc.DSN = db.DSN + "?default_transaction_read_only=on"
			trace := &audit{}

			var output bytes.Buffer

			app := fx.New(
				appfx.New(),
				fx.NopLogger,
				fx.Replace(cfg, pc),
				fx.Replace(
					fx.Annotate(
						[]string{
							"bitmagnet",
							"enhancement",
							"evaluate",
							"--hash",
							hash.String(),
						},
						fx.ResultTags(`name:"cli_args"`),
					),
				),
				fx.Supply(fx.Annotate(trace, fx.As(new(pgx.QueryTracer)))),
				fx.Decorate(
					func(commandApp *cli.App) *cli.App { commandApp.Writer = &output; return commandApp },
				),
				fx.Invoke(func(_ *cli.App, _ hooks.AttachedHooks) {}),
			)
			require.NoError(t, app.Err())
			require.NoError(t, app.Start(t.Context()))

			select {
			case <-app.Done():
			case <-time.After(10 * time.Second):
				t.Fatal("command did not terminate")
			}

			require.NoError(t, app.Stop(t.Context()))

			var report struct {
				Evaluations []enhancement.Evaluation `json:"evaluations"`
			}
			require.NoError(t, json.Unmarshal(output.Bytes(), &report))
			require.Len(t, report.Evaluations, 1)

			want := "unresolved"
			if failure {
				want = "provider_failure"
			}

			require.Equal(t, want, report.Evaluations[0].Outcome)
			require.NotEmpty(t, trace.statements)

			for _, sql := range trace.statements {
				upper := strings.ToUpper(strings.TrimSpace(sql))
				require.True(
					t,
					strings.HasPrefix(upper, "SELECT ") || strings.HasPrefix(upper, "BEGIN ") ||
						upper == "ROLLBACK",
					sql,
				)
			}
		})
	}
}

func TestExplicitAndSampleSelectionExcludeQuarantineAndBans(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)

	hashes := []protocol.ID{
		protocol.MustParseID(strings.Repeat("01", 20)),
		protocol.MustParseID(strings.Repeat("02", 20)),
		protocol.MustParseID(strings.Repeat("03", 20)),
	}
	for i, hash := range hashes {
		require.NoError(
			t,
			db.Gorm.Create(
				&model.Torrent{
					InfoHash:    hash,
					Name:        "Synthetic",
					Size:        10,
					FilesStatus: model.FilesStatusNoInfo,
				},
			).Error,
		)

		if i > 0 {
			status := "pending"
			if i == 2 {
				status = "banned"
			}

			_, err := db.Pool.Exec(
				t.Context(),
				`INSERT INTO moderation_decisions(identifier,status,origin,created_at,updated_at)
 VALUES (encode(sha256(convert_to('magnes:moderation:v1:btih:','UTF8') || $1::bytea),'hex'),
 $2,'moderator',now(),now())`,
				hash.Bytes(),
				status,
			)
			require.NoError(t, err)
		}
	}

	conn, err := pgx.Connect(t.Context(), db.DSN)

	require.NoError(t, err)
	defer func() { _ = conn.Close(t.Context()) }()

	tx, err := conn.BeginTx(t.Context(), pgx.TxOptions{AccessMode: pgx.ReadOnly})

	require.NoError(t, err)
	defer func() { _ = tx.Rollback(t.Context()) }()

	explicit, err := enhancement.LoadEvidence(t.Context(), tx, hashes, 0)
	require.NoError(t, err)
	require.Len(t, explicit, 1)
	require.Equal(t, hashes[0].String(), explicit[0].InfoHash)
	sample, err := enhancement.LoadEvidence(t.Context(), tx, nil, 10)
	require.NoError(t, err)
	require.Len(t, sample, 1)
	require.Equal(t, explicit, sample)
}

func TestApplicationShutdownCancelsReadOnlyEvaluationHTTP(t *testing.T) {
	t.Parallel()
	lockCLI(t)
	db := dbtest.New(t)
	hash := protocol.MustParseID(strings.Repeat("04", 20))
	require.NoError(
		t,
		db.Gorm.Create(
			&model.Torrent{
				InfoHash:    hash,
				Name:        "Synthetic shutdown",
				Size:        10,
				FilesStatus: model.FilesStatusNoInfo,
			},
		).Error,
	)

	entered := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)

		close(entered)

		select {
		case <-r.Context().Done():
			close(canceled)
		case <-release:
		}

		_, _ = w.Write(
			[]byte(
				`{
  "choices": [
    {
      "message": {
        "role": "assistant",
        "content": "{\"title\":\"Late result\"}"
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 100,
    "completion_tokens": 10
  }
}`,
			),
		)
	}))
	defer server.Close()
	defer close(release)

	cfg := enhancement.NewDefaultConfig()
	cfg.BaseURL = server.URL
	cfg.APIKey = "fake"
	cfg.Model = "fake"
	cfg.MaxRunCost = 1
	cfg.InputCostPerMillion = 1
	cfg.OutputCostPerMillion = 1
	pc := postgres.NewDefaultConfig()
	pc.DSN = db.DSN + "?default_transaction_read_only=on"

	var output bytes.Buffer

	app := fx.New(
		appfx.New(),
		fx.NopLogger,
		fx.Replace(cfg, pc),
		fx.Replace(
			fx.Annotate(
				[]string{"bitmagnet", "enhancement", "evaluate", "--hash", hash.String()},
				fx.ResultTags(`name:"cli_args"`),
			),
		),
		fx.Decorate(func(commandApp *cli.App) *cli.App { commandApp.Writer = &output; return commandApp }),
		fx.Invoke(func(_ *cli.App, _ hooks.AttachedHooks) {}),
	)
	require.NoError(t, app.Err())
	require.NoError(t, app.Start(t.Context()))

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("provider request did not start")
	}

	stopCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	require.NoError(t, app.Stop(stopCtx))

	select {
	case <-canceled:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("application shutdown left provider request running")
	}

	var report struct {
		Evaluations []enhancement.Evaluation `json:"evaluations"`
	}
	require.NoError(t, json.Unmarshal(output.Bytes(), &report))
	require.Len(t, report.Evaluations, 1)
	require.Equal(t, "limited", report.Evaluations[0].Outcome)
	require.Nil(t, report.Evaluations[0].Result)
}
