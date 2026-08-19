package gqlfx

import (
	"github.com/99designs/gqlgen/graphql"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/api_key"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/user"
	"github.com/bitmagnet-io/bitmagnet/internal/blocking"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/search"
	"github.com/bitmagnet-io/bitmagnet/internal/gql"
	gqlauth "github.com/bitmagnet-io/bitmagnet/internal/gql/auth"
	"github.com/bitmagnet-io/bitmagnet/internal/gql/config"
	"github.com/bitmagnet-io/bitmagnet/internal/gql/directive"
	"github.com/bitmagnet-io/bitmagnet/internal/gql/httpserver"
	"github.com/bitmagnet-io/bitmagnet/internal/gql/resolvers"
	"github.com/bitmagnet-io/bitmagnet/internal/health"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/metrics/queuemetrics"
	"github.com/bitmagnet-io/bitmagnet/internal/metrics/torrentmetrics"
	"github.com/bitmagnet-io/bitmagnet/internal/processor"
	"github.com/bitmagnet-io/bitmagnet/internal/queue/manager"
	"github.com/bitmagnet-io/bitmagnet/internal/worker"
	"go.uber.org/fx"
)

func New() fx.Option {
	return fx.Module(
		"graphql",
		fx.Provide(
			config.New,
			httpserver.New,

			// The @auth directives in the schema are the authoritative list of
			// GraphQL object actions, so they are extracted rather than restated.
			func(lcfg lazy.Lazy[graphql.ExecutableSchema]) (directive.AuthDirectives, error) {
				schema, err := lcfg.Get()
				if err != nil {
					return nil, err
				}

				return directive.ExtractAuthDirectives(
					directive.ExtractSchemaDirectives(schema.Schema()),
				), nil
			},
			fx.Annotate(
				func(directives directive.AuthDirectives) rbac.ObjectActionProvider {
					return func() []rbac.ObjectAction {
						return gqlauth.ObjectActions(directives)
					}
				},
				fx.ResultTags(`group:"auth_object_actions"`),
			),
			fx.Annotate(
				func() rbac.PermissionProvider { return gqlauth.Permissions },
				fx.ResultTags(`group:"auth_permissions"`),
			),
			func(
				lcfg lazy.Lazy[gql.Config],
			) lazy.Lazy[graphql.ExecutableSchema] {
				return lazy.New(func() (graphql.ExecutableSchema, error) {
					cfg, err := lcfg.Get()
					if err != nil {
						return nil, err
					}

					return gql.NewExecutableSchema(cfg), nil
				})
			},
		),
		fx.Provide(
			func(p Params) Result {
				return Result{
					Resolver: lazy.New(func() (*resolvers.Resolver, error) {
						ch, err := p.Checker.Get()
						if err != nil {
							return nil, err
						}
						s, err := p.Search.Get()
						if err != nil {
							return nil, err
						}
						d, err := p.Dao.Get()
						if err != nil {
							return nil, err
						}
						qmc, err := p.QueueMetricsClient.Get()
						if err != nil {
							return nil, err
						}
						qm, err := p.QueueManager.Get()
						if err != nil {
							return nil, err
						}
						tm, err := p.TorrentMetricsClient.Get()
						if err != nil {
							return nil, err
						}
						pr, err := p.Processor.Get()
						if err != nil {
							return nil, err
						}
						bm, err := p.BlockingManager.Get()
						if err != nil {
							return nil, err
						}
						return &resolvers.Resolver{
							Dao:                  d,
							Search:               s,
							Checker:              ch,
							QueueMetricsClient:   qmc,
							QueueManager:         qm,
							TorrentMetricsClient: tm,
							Processor:            pr,
							BlockingManager:      bm,
							UserService:          p.UserService,
							APIKeyService:        p.APIKeyService,
							RBACService:          p.RBACService,
						}, nil
					}),
				}
			},
		),
		// inject resolver dependencies avoiding a circular dependency:
		fx.Invoke(func(
			resolver lazy.Lazy[*resolvers.Resolver],
			workers worker.Registry,
		) {
			resolver.Decorate(func(r *resolvers.Resolver) (*resolvers.Resolver, error) {
				r.Workers = workers
				return r, nil
			})
		}),
	)
}

type Params struct {
	fx.In
	Search               lazy.Lazy[search.Search]
	Dao                  lazy.Lazy[*dao.Query]
	Checker              lazy.Lazy[health.Checker]
	QueueMetricsClient   lazy.Lazy[queuemetrics.Client]
	QueueManager         lazy.Lazy[manager.Manager]
	TorrentMetricsClient lazy.Lazy[torrentmetrics.Client]
	Processor            lazy.Lazy[processor.Processor]
	BlockingManager      lazy.Lazy[blocking.Manager]
	UserService          user.Service
	APIKeyService        api_key.Service
	RBACService          rbac.Service
}

type Result struct {
	fx.Out
	Resolver lazy.Lazy[*resolvers.Resolver]
}
