package database

import (
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"go.uber.org/fx"
)

// DaoProvider and DaoTransactionProvider are the database seams that the ported
// upstream/next auth packages depend on.
//
// On `next` these are part of a larger provider abstraction in this package, built
// on its worker-runner lifecycle. The `main` lineage resolves the dao lazily through
// fx instead, so rather than porting that architecture this adapts the lineage's own
// lazy.Lazy[*dao.Query] to the same two-method surface the auth code consumes.
type DaoProvider interface {
	Dao() (*dao.Query, error)
}

type DaoTransactionProvider interface {
	DaoProvider
	DaoTransaction(func(tx *dao.Query) error) error
}

type DaoProviderParams struct {
	fx.In
	Dao lazy.Lazy[*dao.Query]
}

type DaoProviderResult struct {
	fx.Out
	DaoTransactionProvider DaoTransactionProvider
}

func NewDaoTransactionProvider(p DaoProviderParams) DaoProviderResult {
	return DaoProviderResult{
		DaoTransactionProvider: daoTransactionProvider{dao: p.Dao},
	}
}

type daoTransactionProvider struct {
	dao lazy.Lazy[*dao.Query]
}

func (p daoTransactionProvider) Dao() (*dao.Query, error) {
	return p.dao.Get()
}

func (p daoTransactionProvider) DaoTransaction(fn func(tx *dao.Query) error) error {
	q, err := p.dao.Get()
	if err != nil {
		return err
	}

	return q.Transaction(fn)
}
