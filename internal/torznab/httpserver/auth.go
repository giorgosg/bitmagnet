package httpserver

import (
	"net/http"

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
	// Only the credential Torznab defines counts here: the apikey query
	// parameter or the X-Api-Key header. Whatever the global bearer middleware
	// resolved is deliberately ignored.
	//
	// Reading that identity back made a browser session a third credential type
	// for this endpoint. An operator with the web UI open had their JWT attached
	// by the middleware, so the endpoint answered 200 to a request carrying no
	// apikey at all — which means any page that could make the browser issue the
	// request got Torznab access on the operator's behalf.
	//
	// An empty token resolves the anonymous identity rather than refusing
	// outright, so whether the endpoint is open depends only on the permission
	// model and not on how the server happens to be assembled — including the
	// middleware not being mounted.
	id, matched, err := h.authenticator.Authenticate(ctx.Request.Context(), torznabAPIKey(ctx))
	if err != nil || !matched {
		return nil, false
	}

	// A key presented in the apikey slot is still resolved by the shared
	// authenticator chain, which would happily accept a JWT there too. Machine
	// credentials only: an identity carrying a user but no API key is an
	// interactive session, whichever slot it arrived in.
	if self := id.Self(); self.User != nil && self.APIKey == nil {
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
