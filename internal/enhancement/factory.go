package enhancement

import (
	"net/http"

	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/tmdb"
	"go.uber.org/fx"
)

type Params struct {
	fx.In
	Config    Config
	Catalogue lazy.Lazy[tmdb.Client]
}
type Result struct {
	fx.Out
	Evaluator lazy.Lazy[*Evaluator]
}

func New(p Params) Result {
	return Result{
		Evaluator: lazy.New(
			func() (*Evaluator, error) { return NewEvaluator(p.Config, p.Catalogue, http.DefaultClient) },
		),
	}
}
