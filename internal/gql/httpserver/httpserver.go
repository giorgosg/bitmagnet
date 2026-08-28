package httpserver

import (
	"context"
	"net/http"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/browser_session"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/http_auth"
	"github.com/bitmagnet-io/bitmagnet/internal/httpserver"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/gin-gonic/gin"
	"github.com/vektah/gqlparser/v2/ast"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type Params struct {
	fx.In
	Schema        lazy.Lazy[graphql.ExecutableSchema]
	Logger        *zap.SugaredLogger
	BrowserCookie browser_session.Cookie
	Config        Config
}

type Result struct {
	fx.Out
	Option httpserver.Option `group:"http_server_options"`
}

func New(p Params) Result {
	return Result{
		Option: &builder{
			schema:        p.Schema,
			browserCookie: p.BrowserCookie,
			config:        p.Config,
		},
	}
}

type builder struct {
	schema        lazy.Lazy[graphql.ExecutableSchema]
	browserCookie browser_session.Cookie
	config        Config
}

func (builder) Key() string {
	return "graphql"
}

func (b builder) Apply(e *gin.Engine) error {
	schema, err := b.schema.Get()
	if err != nil {
		return err
	}

	gql := newServer(schema, b.browserCookie, b.config)

	e.POST("/graphql", func(c *gin.Context) {
		gql.ServeHTTP(c.Writer, c.Request)
	})

	// The playground is a separate route from the API, sharing only the path, and
	// it carries no auth guard. When it is off the route is not registered at all,
	// so GET /graphql 404s rather than serving an unauthenticated page.
	if b.config.Playground {
		pg := playground.Handler("GraphQL playground", "/graphql")

		e.GET("/graphql", func(c *gin.Context) {
			if c.GetHeader("Upgrade") != "" {
				c.AbortWithStatus(http.StatusBadRequest)
				return
			}

			pg.ServeHTTP(c.Writer, c.Request)
		})
	}

	return nil
}

func newServer(
	es graphql.ExecutableSchema,
	browserCookie browser_session.Cookie,
	cfg Config,
) *handler.Server {
	srv := handler.New(es)
	srv.SetErrorPresenter(errorPresenter)

	srv.AddTransport(transport.POST{})

	srv.SetQueryCache(lru.New[*ast.QueryDocument](1000))

	// Off unless an operator asks for it: the schema is reconnaissance for anyone
	// who can reach the POST endpoint, and nothing shipped reads it at runtime -
	// the web UI's client is generated from the schema files at build time.
	if cfg.Introspection {
		srv.Use(extension.Introspection{})
	}

	srv.Use(extension.AutomaticPersistedQuery{
		Cache: lru.New[string](100),
	})
	srv.AroundOperations(requireSameOriginForBrowserMutation(browserCookie))

	return srv
}

func requireSameOriginForBrowserMutation(browserCookie browser_session.Cookie) graphql.OperationMiddleware {
	return func(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
		operation := graphql.GetOperationContext(ctx).Operation
		if operation == nil || operation.Operation != ast.Mutation {
			return next(ctx)
		}

		ginCtx, ok := httpserver.GinContextFromContext(ctx)
		if !ok {
			return next(ctx)
		}

		resolution, ok := http_auth.GetResolution(ginCtx)
		if !ok || !resolution.UsesBrowserAuthority() {
			return next(ctx)
		}

		if err := browserCookie.RequireSameOrigin(ctx); err != nil {
			ginCtx.Status(http.StatusForbidden)
			return graphql.OneShot(graphql.ErrorResponse(ctx, "%s", err.Error()))
		}

		return next(ctx)
	}
}
