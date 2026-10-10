package dao

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"
)

type costPlan struct {
	TotalCost float64 `json:"Total Cost"`
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

	// EXPLAIN accepts parameters in the query it plans. Keep them bound here;
	// ToSQL inlines values and would extend the older count path's risk.
	statement := db.Session(&gorm.Session{DryRun: true}).Find(&[]interface{}{}).Statement
	if statement.Error != nil {
		return false, statement.Error
	}

	var planJSON []byte
	if err := db.Raw("EXPLAIN (FORMAT JSON) "+statement.SQL.String(), statement.Vars...).
		Row().Scan(&planJSON); err != nil {
		return false, err
	}

	var plans []explainPlan
	if err := json.Unmarshal(planJSON, &plans); err != nil {
		return false, err
	}

	if len(plans) != 1 {
		return false, fmt.Errorf("unexpected EXPLAIN plan count: %d", len(plans))
	}

	return plans[0].Plan.TotalCost <= budget, nil
}

func ToSQL(db *gorm.DB) string {
	return db.ToSQL(func(tx *gorm.DB) *gorm.DB {
		return tx.Find(&[]interface{}{})
	})
}

type BudgetedCountResult struct {
	Count          int64
	Cost           float64
	BudgetExceeded bool
}

func BudgetedCount(db *gorm.DB, budget float64) (BudgetedCountResult, error) {
	var row *sql.Row

	q := ToSQL(db)
	if budget > 0 {
		row = db.
			Raw("SELECT count, cost, budget_exceeded from budgeted_count(?, ?)", q, budget).
			Row()
	} else {
		row = db.
			Raw("SELECT count(*) as count, 0 as cost, false as budget_exceeded from (" + q + ") t").
			Row()
	}

	result := BudgetedCountResult{}
	err := row.Scan(&result.Count, &result.Cost, &result.BudgetExceeded)

	return result, err
}
