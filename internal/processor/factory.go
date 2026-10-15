package processor

import (
	"github.com/bitmagnet-io/bitmagnet/internal/classifier"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/search"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type Params struct {
	fx.In
	Moderation       lazy.Lazy[*moderation.Service]
	ClassifierConfig classifier.Config
	ClassifierSource lazy.Lazy[classifier.Source]
	Search           lazy.Lazy[search.Search]
	Workflow         lazy.Lazy[classifier.Runner]
	Dao              lazy.Lazy[*dao.Query]
	Logger           *zap.SugaredLogger
}

type Result struct {
	fx.Out
	Processor lazy.Lazy[Processor]
}

func New(p Params) Result {
	return Result{
		Processor: lazy.New(func() (Processor, error) {
			s, err := p.Search.Get()
			if err != nil {
				return nil, err
			}

			d, err := p.Dao.Get()
			if err != nil {
				return nil, err
			}

			w, err := p.Workflow.Get()
			if err != nil {
				return nil, err
			}

			moderator, err := p.Moderation.Get()
			if err != nil {
				return nil, err
			}

			source, err := p.ClassifierSource.Get()
			if err != nil {
				return nil, err
			}

			return processor{
				recipeSource:    source,
				moderation:      moderator,
				dao:             d,
				search:          s,
				runner:          w,
				defaultWorkflow: p.ClassifierConfig.Workflow,
			}, nil
		}),
	}
}
