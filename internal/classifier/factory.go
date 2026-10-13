package classifier

import (
	"fmt"

	"github.com/bitmagnet-io/bitmagnet/internal/database/search"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/tmdb"
	"go.uber.org/fx"
)

type Params struct {
	fx.In
	ModerationConfig moderation.Config
	Moderation       lazy.Lazy[*moderation.Service]
	Config           Config
	TmdbConfig       tmdb.Config
	MetadataSearch   lazy.Lazy[search.MetadataSearch]
	TmdbClient       lazy.Lazy[tmdb.Client]
}

type Result struct {
	fx.Out
	Compiler lazy.Lazy[Compiler]
	Source   lazy.Lazy[Source]
	Runner   lazy.Lazy[Runner]
}

func New(params Params) Result {
	lc := lazy.New(func() (Compiler, error) {
		s, err := params.MetadataSearch.Get()
		if err != nil {
			return nil, err
		}

		tmdbClient, err := params.TmdbClient.Get()
		if err != nil {
			return nil, err
		}

		moderator, err := params.Moderation.Get()
		if err != nil {
			return nil, err
		}

		return compiler{
			options: []compilerOption{
				compilerFeatures(defaultFeatures),
				celEnvOption,
			},
			dependencies: dependencies{
				moderation:       moderator,
				moderationConfig: params.ModerationConfig,
				search: newLocalSearchSemaphore(
					localSearch{s},
					params.Config.SearchConcurrency,
				),
				tmdbClient: tmdbClient,
			},
		}, nil
	})
	lsrc := lazy.New[Source](func() (Source, error) {
		src, err := newSourceProvider(params.Config, params.TmdbConfig).source()
		if err != nil {
			return Source{}, err
		}

		if _, ok := src.Workflows[params.Config.Workflow]; !ok {
			return Source{}, fmt.Errorf("default workflow '%s' not found", params.Config.Workflow)
		}

		return src, nil
	})

	return Result{
		Compiler: lc,
		Source:   lsrc,
		Runner: lazy.New(func() (Runner, error) {
			src, err := lsrc.Get()
			if err != nil {
				return nil, err
			}

			c, err := lc.Get()
			if err != nil {
				return nil, err
			}

			r, err := c.Compile(src)
			if err != nil {
				return nil, err
			}

			return runnerSemaphore{
				runner:    r,
				semaphore: make(chan struct{}, params.Config.Concurrency),
			}, nil
		}),
	}
}
