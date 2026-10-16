package enhancementcmd

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/enhancement"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/processor"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/jackc/pgx/v5"
	"github.com/urfave/cli/v2"
)

func resume(ctx context.Context, c *cli.Context, p Params, cfg enhancement.Config) error {
	d, err := p.Dao.Get()
	if err != nil {
		return err
	}

	if err = enhancement.NewScheduler(d.UnderlyingDB(), cfg, nil, nil).Resume(ctx); err != nil {
		return err
	}

	return json.NewEncoder(c.App.Writer).Encode(map[string]any{"resumed": true, "automatic": cfg.Automatic})
}

func process(parent context.Context, c *cli.Context, p Params, cfg enhancement.Config, hashes []protocol.ID) error {
	ctx, cancel := context.WithTimeout(parent, cfg.RunTimeout)
	defer cancel()
	// One evaluator shares whole-command caps across all explicitly selected hashes.
	engine, err := enhancement.NewEvaluator(cfg, p.Catalogue, http.DefaultClient)
	if err != nil {
		return err
	}

	d, err := p.Dao.Get()
	if err != nil {
		return err
	}

	cat, err := p.Catalogue.Get()
	if err != nil {
		return err
	}

	proc, err := p.Processor.Get()
	if err != nil {
		return err
	}

	app := enhancement.NewApplication(d.UnderlyingDB(), engine, cat, cfg)
	scheduler := enhancement.NewScheduler(
		d.UnderlyingDB(),
		cfg,
		app,
		func(ctx context.Context, h protocol.ID) error {
			return proc.Process(ctx, processor.MessageParams{InfoHashes: []protocol.ID{h}})
		},
	)
	report := Report{
		Accounting: "shared UTC daily ledger and claims; additional process-local command caps",
		Model:      cfg.Model,
	}

	for _, h := range hashes {
		claim, claimErr := scheduler.Request(ctx, h)
		if claimErr != nil {
			report.Evaluations = append(
				report.Evaluations,
				enhancement.Evaluation{
					InfoHash: h.String(),
					Outcome:  "not_admitted",
					Error:    safeError(claimErr),
				},
			)

			continue
		}

		result, runErr := scheduler.ProcessRequested(ctx, claim, c.Bool("force"), c.Bool("rematch"))

		result.InfoHash = h.String()
		if runErr != nil {
			result.Error = safeError(runErr)
			if result.Outcome == "" {
				result.Outcome = "not_completed"
			}
		}

		report.Evaluations = append(report.Evaluations, result)
	}

	return json.NewEncoder(c.App.Writer).Encode(report)
}

func safeError(err error) string {
	for _, known := range []error{
		enhancement.ErrCapacity, enhancement.ErrPaused, enhancement.ErrBudget, enhancement.ErrEvaluated,
		enhancement.ErrHidden, enhancement.ErrStale, enhancement.ErrNotClassified, enhancement.ErrLease,
		context.Canceled, context.DeadlineExceeded,
	} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}

	return "enhancement operation failed; inspect configuration and database availability"
}

// Inspection uses the independent read-only connection, never migration-decorated DAO.
func inspect(
	ctx context.Context,
	c *cli.Context,
	conn *pgx.Conn,
	cfg enhancement.Config,
	evidence []enhancement.Evidence,
) error {
	items := make([]any, 0, len(evidence))
	for _, e := range evidence {
		var state json.RawMessage

		err := conn.QueryRow(ctx, `SELECT coalesce(jsonb_agg(jsonb_build_object(
   'stage',stage,'outcome',outcome,'completedAt',completed_at,'attemptOutcome',attempt_outcome,
   'attemptedAt',attempted_at,'attemptInput',attempt_input,'attemptRecipe',attempt_recipe,
 'inputFingerprint',input_fingerprint,'recipeFingerprint',recipe_fingerprint,
   'acceptedInput',accepted_input,'acceptedOutput',nullif(accepted_output,'')::jsonb)), '[]'::jsonb)
   FROM classification_evaluations WHERE info_hash=$1`, protocol.MustParseID(e.InfoHash).Bytes()).Scan(&state)
		if err != nil {
			return errors.New("cannot read evaluation state")
		}

		var rows []struct {
			Stage             string     `json:"stage"`
			Outcome           string     `json:"outcome"`
			CompletedAt       *time.Time `json:"completedAt"`
			InputFingerprint  string     `json:"inputFingerprint"`
			RecipeFingerprint string     `json:"recipeFingerprint"`
			AttemptInput      string     `json:"attemptInput"`
			AttemptRecipe     string     `json:"attemptRecipe"`
			AttemptOutcome    string     `json:"attemptOutcome"`
		}
		if err := json.Unmarshal(state, &rows); err != nil {
			return errors.New("invalid evaluation state")
		}

		states := make([]model.ClassificationEvaluation, 0, len(rows))
		for _, r := range rows {
			states = append(
				states,
				model.ClassificationEvaluation{
					Stage:             r.Stage,
					Outcome:           r.Outcome,
					CompletedAt:       sql.NullTime{Valid: r.CompletedAt != nil},
					InputFingerprint:  r.InputFingerprint,
					RecipeFingerprint: r.RecipeFingerprint,
					AttemptInput:      r.AttemptInput,
					AttemptRecipe:     r.AttemptRecipe,
					AttemptOutcome:    r.AttemptOutcome,
				},
			)
		}

		items = append(
			items,
			map[string]any{
				"evidence":    e,
				"evaluations": state,
				"eligibility": enhancement.PreviewEligibility(e, states, cfg),
			},
		)
	}

	var paused bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM key_values WHERE key='enhancement/paused')").
		Scan(&paused); err != nil {
		return errors.New("cannot read pause state")
	}

	var spending json.RawMessage
	if err := conn.QueryRow(ctx, `SELECT coalesce(jsonb_agg(d),'[]'::jsonb) FROM
 (SELECT day,charged FROM enhancement_daily_spending ORDER BY day DESC LIMIT 7) d`).Scan(&spending); err != nil {
		return errors.New("cannot read spending")
	}

	return json.NewEncoder(c.App.Writer).
		Encode(map[string]any{
			"automatic": cfg.Automatic, "paused": paused, "dailyBudget": cfg.DailyBudget,
			"dailySpending": spending, "items": items,
			"accounting": "read-only inspection; charged totals include conservative unknown usage",
		})
}
