package database_test

import (
	"errors"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
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

// The transaction must actually be a transaction: a body returning an error
// rolls back, and the error reaches the caller.
func TestDaoTransactionRollsBackOnError(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	p := newProvider(db.Query, nil)

	var seen *dao.Query

	err := p.DaoTransaction(func(tx *dao.Query) error {
		seen = tx

		return errBoom
	})

	require.ErrorIs(t, err, errBoom)
	assert.NotNil(t, seen, "transaction body should receive a query handle")
}

func TestDaoTransactionCommits(t *testing.T) {
	t.Parallel()

	db := dbtest.New(t)
	p := newProvider(db.Query, nil)

	require.NoError(t, p.DaoTransaction(func(tx *dao.Query) error {
		_, err := tx.KeyValue.Where(tx.KeyValue.Key.Eq("provider_test")).Count()

		return err
	}))
}
