package dao_test

import (
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/stretchr/testify/require"
)

func TestBudgetedCountKeepsSearchValuesBound(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	tx := db.Gorm.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { _ = tx.Rollback().Error })
	require.NoError(t, tx.Exec("SET LOCAL standard_conforming_strings = off").Error)

	// Under this server setting, rendering the bound value into a SQL literal
	// lets the backslash swallow the first of GORM's doubled quote characters.
	query := tx.Table("(SELECT 'safe' AS name) AS names").Where("name = ?", `\'`)

	for _, budget := range []float64{0, 1000} {
		result, err := dao.BudgetedCount(query, budget)
		require.NoError(t, err, "budget %g", budget)
		require.Zero(t, result.Count, "budget %g", budget)
		require.False(t, result.BudgetExceeded, "budget %g", budget)
	}
}
