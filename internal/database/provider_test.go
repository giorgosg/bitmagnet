package database_test

import (
	"errors"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errBoom = errors.New("boom")

func newProvider(q *dao.Query, err error) database.DaoTransactionProvider {
	return database.NewDaoTransactionProvider(database.DaoProviderParams{
		Dao: lazy.New(func() (*dao.Query, error) {
			return q, err
		}),
	}).DaoTransactionProvider
}

// A failure resolving the dao must surface, not be swallowed into a nil query
// that panics at the point of use.
func TestDaoTransactionProviderPropagatesResolutionError(t *testing.T) {
	t.Parallel()

	p := newProvider(nil, errBoom)

	_, err := p.Dao()
	require.ErrorIs(t, err, errBoom)

	called := false
	err = p.DaoTransaction(func(*dao.Query) error {
		called = true

		return nil
	})

	require.ErrorIs(t, err, errBoom)
	assert.False(t, called, "transaction body must not run when the dao is unavailable")
}

func TestDaoTransactionCommitsAndRollsBack(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	p := newProvider(db.Query, nil)
	ctx := t.Context()

	require.NoError(t, p.DaoTransaction(func(tx *dao.Query) error {
		return tx.KeyValue.WithContext(ctx).Create(&model.KeyValue{Key: "committed", Value: "yes"})
	}))

	committed, err := db.Query.KeyValue.WithContext(ctx).
		Where(db.Query.KeyValue.Key.Eq("committed")).Count()
	require.NoError(t, err)
	assert.Equal(t, int64(1), committed)

	err = p.DaoTransaction(func(tx *dao.Query) error {
		require.NoError(t, tx.KeyValue.WithContext(ctx).
			Create(&model.KeyValue{Key: "rolled_back", Value: "no"}))

		return errBoom
	})

	require.ErrorIs(t, err, errBoom)

	rolledBack, err := db.Query.KeyValue.WithContext(ctx).
		Where(db.Query.KeyValue.Key.Eq("rolled_back")).Count()
	require.NoError(t, err)
	assert.Zero(t, rolledBack)
}
