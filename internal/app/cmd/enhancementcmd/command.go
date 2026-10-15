// Package enhancementcmd exposes bounded, explicitly requested read-only experiments.
package enhancementcmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"

	"github.com/bitmagnet-io/bitmagnet/internal/database/postgres"
	"github.com/bitmagnet-io/bitmagnet/internal/enhancement"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/bitmagnet-io/bitmagnet/internal/tmdb"
	"github.com/jackc/pgx/v5"
	"github.com/urfave/cli/v2"
	"go.uber.org/fx"
)

type Params struct {
	fx.In
	Config    enhancement.Config
	Postgres  postgres.Config
	Catalogue lazy.Lazy[tmdb.Client]
	Tracer    pgx.QueryTracer `optional:"true"`
}
type Result struct {
	fx.Out
	Command *cli.Command `group:"commands"`
}
type Report struct {
	Accounting  string                   `json:"accounting"`
	Model       string                   `json:"model"`
	Evaluations []enhancement.Evaluation `json:"evaluations"`
}

func New(p Params) Result {
	return Result{
		Command: &cli.Command{
			Name:  "enhancement",
			Usage: "Inspect LLM metadata enhancement without applying results",
			Subcommands: []*cli.Command{
				{
					Name:  "evaluate",
					Usage: "Read visible torrents and return JSON; performs no database writes",
					Flags: []cli.Flag{
						&cli.StringSliceFlag{
							Name:  "hash",
							Usage: "Explicit visible torrent infohash; repeatable (maximum 100)",
						},
						&cli.IntFlag{
							Name:  "sample",
							Usage: "Deterministic visible sample ordered by infohash (maximum 100)",
						},
						&cli.StringFlag{
							Name: "base-url",
						},
						&cli.StringFlag{
							Name: "thinking", Value: p.Config.Thinking,
						},
						&cli.StringFlag{
							Name:  "output-token-parameter",
							Value: p.Config.OutputTokenParameter,
						},
						&cli.StringFlag{Name: "model"},
						&cli.StringFlag{
							Name:  "api-key-env",
							Value: "BITMAGNET_ENHANCEMENT_API_KEY",
							Usage: "Environment variable containing the provider secret",
						},
						&cli.StringFlag{
							Name:  "prompt-file",
							Usage: "Additional operator instructions",
						},
						&cli.Float64Flag{Name: "temperature", Value: p.Config.Temperature},
						&cli.IntFlag{
							Name:  "concurrency",
							Value: p.Config.Concurrency,
						},
						&cli.IntFlag{Name: "max-requests", Value: p.Config.MaxRequests},
						&cli.IntFlag{Name: "max-tool-calls", Value: p.Config.MaxToolCalls},
						&cli.IntFlag{
							Name:  "request-output-tokens",
							Value: p.Config.RequestOutputTokens,
						},
						&cli.IntFlag{
							Name:  "max-output-tokens",
							Value: p.Config.MaxOutputTokens,
						},
						&cli.DurationFlag{Name: "timeout", Value: p.Config.Timeout},
						&cli.IntFlag{
							Name:  "run-max-requests",
							Value: p.Config.MaxRunRequests,
						},
						&cli.IntFlag{
							Name:  "run-max-output-tokens",
							Value: p.Config.MaxRunOutputTokens,
						},
						&cli.DurationFlag{Name: "run-timeout", Value: p.Config.RunTimeout},
						&cli.Float64Flag{
							Name:  "run-max-cost",
							Value: p.Config.MaxRunCost,
							Usage: "Positive whole-command spending ceiling; requires conservative token prices",
						},
						&cli.Float64Flag{
							Name:  "input-cost-per-million",
							Value: p.Config.InputCostPerMillion,
						},
						&cli.Float64Flag{
							Name:  "output-cost-per-million",
							Value: p.Config.OutputCostPerMillion,
						},
					},
					Action: func(c *cli.Context) error { return run(c, p) },
				},
			},
		},
	}
}

func run(c *cli.Context, p Params) error {
	cfg := p.Config
	if c.IsSet("base-url") {
		cfg.BaseURL = c.String("base-url")
	}

	if c.IsSet("model") {
		cfg.Model = c.String("model")
	}

	if key := os.Getenv(c.String("api-key-env")); key != "" {
		cfg.APIKey = key
	}

	if c.IsSet("prompt-file") {
		b, err := os.ReadFile(c.String("prompt-file"))
		if err != nil {
			return errors.New("cannot read prompt file")
		}

		if len(b) > 4000 {
			return errors.New("prompt file exceeds 4000 bytes")
		}

		cfg.Prompt = string(b)
	}

	cfg.Thinking = c.String("thinking")
	cfg.OutputTokenParameter = c.String("output-token-parameter")
	cfg.Temperature = c.Float64("temperature")
	cfg.Concurrency = c.Int("concurrency")
	cfg.MaxRequests = c.Int("max-requests")
	cfg.MaxToolCalls = c.Int("max-tool-calls")
	cfg.RequestOutputTokens = c.Int("request-output-tokens")
	cfg.MaxOutputTokens = c.Int("max-output-tokens")
	cfg.Timeout = c.Duration("timeout")
	cfg.MaxRunRequests = c.Int("run-max-requests")
	cfg.MaxRunOutputTokens = c.Int("run-max-output-tokens")
	cfg.RunTimeout = c.Duration("run-timeout")
	cfg.MaxRunCost = c.Float64("run-max-cost")
	cfg.InputCostPerMillion = c.Float64("input-cost-per-million")

	cfg.OutputCostPerMillion = c.Float64("output-cost-per-million")
	if cfg.MaxRunCost <= 0 {
		return errors.New(
			"evaluation requires positive run-max-cost and conservative input/output token prices",
		)
	}

	engine, err := enhancement.NewEvaluator(cfg, p.Catalogue, http.DefaultClient)
	if err != nil {
		return err
	}

	hashes := make([]protocol.ID, 0)

	for _, h := range c.StringSlice("hash") {
		hash, parseErr := protocol.ParseID(h)
		if parseErr != nil {
			return errors.New("invalid torrent infohash")
		}

		hashes = append(hashes, hash)
	}

	sample := c.Int("sample")
	if len(hashes) > 100 || sample > 100 || sample < 0 || (len(hashes) == 0 && sample == 0) ||
		(len(hashes) > 0 && sample > 0) {
		return errors.New("select either 1-100 hashes or --sample 1-100")
	}

	ctx, cancel := context.WithTimeout(c.Context, cfg.RunTimeout)
	defer cancel()

	connCfg, err := pgx.ParseConfig(p.Postgres.CreateDSN())
	if err != nil {
		return errors.New("invalid PostgreSQL configuration")
	}
	// These settings apply in the startup packet, before any application query.
	connCfg.RuntimeParams["default_transaction_read_only"] = "on"
	connCfg.Tracer = p.Tracer

	conn, err := pgx.ConnectConfig(ctx, connCfg)
	if err != nil {
		return errors.New("cannot open read-only catalogue connection")
	}

	defer func() { _ = conn.Close(context.Background()) }()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return errors.New("cannot begin read-only catalogue transaction")
	}

	evidence, loadErr := enhancement.LoadEvidence(ctx, tx, hashes, sample)
	rollbackErr := tx.Rollback(ctx)

	if loadErr != nil {
		return fmt.Errorf("cannot load visible catalogue evidence: %w", loadErr)
	}

	if rollbackErr != nil {
		return errors.New("cannot close read-only catalogue transaction")
	}

	report := Report{
		Accounting:  "process-local only; no shared database budget ledger or evaluation state is read or written",
		Model:       cfg.Model,
		Evaluations: make([]enhancement.Evaluation, len(evidence)),
	}
	jobs := make(chan int)

	var wg sync.WaitGroup
	for range min(cfg.Concurrency, len(evidence)) {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for i := range jobs {
				report.Evaluations[i] = engine.Evaluate(ctx, evidence[i])
			}
		}()
	}

	for i := range evidence {
		jobs <- i
	}

	close(jobs)
	wg.Wait()

	return json.NewEncoder(c.App.Writer).Encode(report)
}
