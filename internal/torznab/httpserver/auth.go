package httpserver

import (
	"net/http"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/http_auth"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/identity"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/torznab"
	"github.com/gin-gonic/gin"
)

// The Torznab protocol carries its credential in the query string, so the
// approach here follows the kawaii-not-kawaii fork (172a784d3, "feat(auth):
// username/password sessions, first-run setup, torznab apikey"), the only
// implementation in the fork landscape that wired auth into Torznab at all:
// accept `apikey` or `X-Api-Key`, and answer a failure with a Torznab XML error
// rather than a bare status code, because *arr clients parse the error code.
//
// Two deliberate departures from that fork:
//
//   - Its trusted-network bypass explicitly does not extend to Torznab, and that
//     decision is kept. A key is always required once anonymous access is off;
//     being on the LAN is not a credential for machine access.
//   - It gates on a global on/off flag because it has no permission model. This
//     lineage has one, so authorization goes through rbac instead, which makes
//     the anonymous default fall out naturally and gives per-key scoping.
const (
	authNamespace = "torznab"
	authObject    = "torznab"
	authAction    = "query"
)

// ObjectAction is the permission a caller needs to use the Torznab endpoint.
// It is contributed to the auth object action group, so while anonymous access
// is enabled the anon role holds it and the endpoint stays open.
var ObjectAction = rbac.NewObjectAction(authNamespace, authObject, authAction)

func ObjectActionProvider() rbac.ObjectActionProvider {
	return func() []rbac.ObjectAction {
		return []rbac.ObjectAction{ObjectAction}
	}
}

// errUnauthorized is the response *arr clients expect: Newznab error code 100,
// as an XML body, not an empty 401.
var errUnauthorized = torznab.Error{
	Code:        100,
	Description: "Incorrect user credentials",
}

// authorize resolves the identity for a Torznab request and checks it may query.
//
// A credential in the query string is accepted only here, where the protocol
// requires it. Everywhere else the bearer header remains the only accepted form,
// because query strings leak into access logs, referrers and browser history.
func (h handler) authorize(ctx *gin.Context) bool {
	if h.authenticator == nil {
		// Auth is not wired in; preserve the endpoint's open behaviour.
		return true
	}

	id, ok := h.resolveIdentity(ctx)
	if !ok {
		return false
	}

	allow, err := id.Enforce(ctx.Request.Context(), ObjectAction)

	return err == nil && allow
}

func (h handler) resolveIdentity(ctx *gin.Context) (identity.Identity, bool) {
	// An explicit key on the request takes precedence over whatever the global
	// middleware resolved, so a client can always present its own credential.
	if key := torznabAPIKey(ctx); key != "" {
		id, matched, err := h.authenticator.Authenticate(ctx.Request.Context(), key)
		if err != nil || !matched {
			return nil, false
		}

		return id, true
	}

	if id, ok := http_auth.GetIdentity(ctx); ok {
		return id, true
	}

	// No credential and no identity from the middleware — which also covers the
	// middleware not being mounted at all. Resolve the anonymous identity
	// directly rather than refusing, so authorization here depends only on the
	// permission model and not on how the server happens to be assembled.
	id, matched, err := h.authenticator.Authenticate(ctx.Request.Context(), "")
	if err != nil || !matched {
		return nil, false
	}

	return id, true
}

func torznabAPIKey(ctx *gin.Context) string {
	if key := ctx.Query("apikey"); key != "" {
		return key
	}

	return ctx.GetHeader("X-Api-Key")
}

func (h handler) writeUnauthorized(ctx *gin.Context) {
	h.writeXMLStatus(ctx, http.StatusUnauthorized, errUnauthorized)
}
