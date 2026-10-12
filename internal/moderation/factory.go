package moderation

import (
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"go.uber.org/fx"
	"gorm.io/gorm"
)

type Params struct {
	fx.In
	DB lazy.Lazy[*gorm.DB]
}
type Result struct {
	fx.Out
	Service lazy.Lazy[*Service]
}

// New defers opening the database until a consumer actually requests the ledger.
func New(p Params) Result {
	return Result{Service: lazy.New(func() (*Service, error) {
		db, err := p.DB.Get()
		if err != nil {
			return nil, err
		}

		return NewService(db), nil
	})}
}
