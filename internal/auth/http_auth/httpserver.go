package http_auth

import (
	"github.com/bitmagnet-io/bitmagnet/internal/httpserver"
	"github.com/gin-gonic/gin"
	"go.uber.org/fx"
)

type Params struct {
	fx.In
	Middleware Middleware
}

type Result struct {
	fx.Out
	Option httpserver.Option `group:"http_server_options"`
}

// New registers AttachAuth as a gin middleware. It only resolves an identity and
// attaches it to the request context — it never rejects, so mounting it changes
// nothing for unauthenticated clients. Enforcement is the job of whatever reads
// the identity back out.
func New(p Params) Result {
	return Result{Option: option{middleware: p.Middleware}}
}

type option struct {
	middleware Middleware
}

// The server applies options sorted by key, so "auth" is mounted before the
// handlers that need to read the identity it attaches.
func (option) Key() string {
	return "auth"
}

func (o option) Apply(g *gin.Engine) error {
	// The identity is attached to the gin context, but resolvers and handlers
	// downstream only see the request context, so bridge the two first.
	g.Use(httpserver.GinContextToContextMiddleware())
	g.Use(o.middleware.AttachAuth())

	return nil
}
