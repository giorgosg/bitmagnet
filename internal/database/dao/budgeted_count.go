package dao

import (
	"encoding/json"
	"fmt"

	"gorm.io/gorm"
)

type costPlan struct {
	TotalCost float64 `json:"Total Cost"`
	PlanRows  int64   `json:"Plan Rows"`
}

type explainPlan struct {
	Plan costPlan `json:"Plan"`
}

// WithinCostBudget checks whether an exact grouped aggregate is affordable.
// A costly grouped query can fall back to the per-value budgeted counts, which
// can still return estimates instead of scanning every matching row.
func WithinCostBudget(db *gorm.DB, budget float64) (bool, error) {
	if budget <= 0 {
		return true, nil
	}

	plan, err := explain(db)
	if err != nil {
		return false, err
	}

	return plan.TotalCost <= budget, nil
}

func explain(db *gorm.DB) (costPlan, error) {
	// EXPLAIN accepts parameters in the query it plans, so the values never
	// need to be rendered into the SQL text.
	statement := db.Session(&gorm.Session{DryRun: true}).Find(&[]interface{}{}).Statement
	if statement.Error != nil {
		return costPlan{}, statement.Error
	}

	var planJSON []byte
	// DryRun already used the PostgreSQL dialector to number placeholders ($1,
	// $2, ...). Run that SQL on the connection directly: Raw only binds its own
	// question-mark placeholders and would silently drop these arguments.
	if err := statement.ConnPool.QueryRowContext(
		statement.Context, "EXPLAIN (FORMAT JSON) "+statement.SQL.String(), statement.Vars...,
	).Scan(&planJSON); err != nil {
		return costPlan{}, err
	}

	var plans []explainPlan
	if err := json.Unmarshal(planJSON, &plans); err != nil {
		return costPlan{}, err
	}

	if len(plans) != 1 {
		return costPlan{}, fmt.Errorf("unexpected EXPLAIN plan count: %d", len(plans))
	}

	return plans[0].Plan, nil
}

type BudgetedCountResult struct {
	Count          int64
	Cost           float64
	BudgetExceeded bool
}

func BudgetedCount(db *gorm.DB, budget float64) (BudgetedCountResult, error) {
	result := BudgetedCountResult{}

	if budget > 0 {
		plan, err := explain(db)
		if err != nil {
			return result, err
		}

		result.Cost = plan.TotalCost

		if plan.TotalCost > budget {
			result.Count = plan.PlanRows
			result.BudgetExceeded = true

			return result, nil
		}
	}

	err := db.Session(&gorm.Session{NewDB: true}).
		Raw("SELECT count(*) FROM (?) AS subquery", db).
		Row().Scan(&result.Count)

	return result, err
}
