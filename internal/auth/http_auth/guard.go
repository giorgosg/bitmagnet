package http_auth

import (
	"github.com/bitmagnet-io/bitmagnet/internal/auth/identity"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/gin-gonic/gin"
)

// Guard authorizes a request against an object action.
//
// AttachAuth only resolves an identity; something has to act on it. GraphQL does
// that through the @auth directive and Torznab in its own handler, but the
// remaining endpoints — the importer, pprof and the metrics scrape — had nothing
// enforcing anything. Disabling anonymous access therefore left a data-mutating
// import endpoint and the profiling handlers open to anyone who could reach the
// port.
type Guard interface {
	Allow(ctx *gin.Context, objectAction rbac.ObjectAction) bool
}

func NewGuard(authenticator identity.Authenticator) Guard {
	return guard{authenticator: authenticator}
}

type guard struct {
	authenticator identity.Authenticator
}

func (g guard) Allow(ctx *gin.Context, objectAction rbac.ObjectAction) bool {
	if g.authenticator == nil {
		// Auth is not wired in; leave the endpoint as it was.
		return true
	}

	id, ok := g.resolve(ctx)
	if !ok {
		return false
	}

	allow, err := id.Enforce(ctx.Request.Context(), objectAction)

	return err == nil && allow
}

func (g guard) resolve(ctx *gin.Context) (identity.Identity, bool) {
	if id, ok := GetIdentity(ctx); ok {
		return id, true
	}

	// Nothing on the context — which also covers AttachAuth not being mounted.
	// Resolve the anonymous identity directly so authorization follows the
	// permission model rather than how the server happens to be assembled.
	id, matched, err := g.authenticator.Authenticate(ctx.Request.Context(), "")
	if err != nil || !matched {
		return nil, false
	}

	return id, true
}
