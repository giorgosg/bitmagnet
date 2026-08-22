package http_auth

import "github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"

// Object actions for the HTTP endpoints that are not GraphQL. They live here
// rather than in each package so the set is visible in one place, and so those
// packages need not each depend on the rbac vocabulary.
//
// The namespace is "http" to keep them distinct from the "graphql" actions
// derived from schema directives.
const Namespace = "http"

var (
	// ObjectActionImport guards POST /import, which writes torrents.
	ObjectActionImport = rbac.NewObjectAction(Namespace, "import", "mutate")
	// ObjectActionPprof guards /debug/pprof, which exposes process internals
	// including the command line.
	ObjectActionPprof = rbac.NewObjectAction(Namespace, "pprof", "query")
	// ObjectActionMetrics guards the Prometheus scrape endpoint.
	ObjectActionMetrics = rbac.NewObjectAction(Namespace, "metrics", "query")
)

// ObjectActionProvider registers the above, so that anonymous access grants them
// while it is enabled and withdraws them when it is not.
func ObjectActionProvider() rbac.ObjectActionProvider {
	return func() []rbac.ObjectAction {
		return []rbac.ObjectAction{
			ObjectActionImport,
			ObjectActionPprof,
			ObjectActionMetrics,
		}
	}
}
