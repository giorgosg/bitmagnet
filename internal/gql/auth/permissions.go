package auth

import (
	"maps"
	"slices"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/gql/directive"
	"github.com/bitmagnet-io/bitmagnet/internal/slice"
)

// Permissions is the baseline granted regardless of the anonymous-access
// setting. Without it, enabling authentication would be a permanent lockout:
// logging in is itself a GraphQL mutation, so `self` has to stay reachable to
// callers who have not authenticated yet.
//
// Adapted from upstream/next's equivalent. Its `index` object has no counterpart
// on this lineage and is dropped; searching stays behind `torrent` and
// `torrentContent`, which anonymous callers do not get unless anonymous access
// is enabled.
func Permissions() []rbac.Permission {
	anon := rbac.SubjectRole{Role: rbac.RoleAnon}
	user := rbac.SubjectRole{Role: rbac.RoleUser}

	return append(
		[]rbac.Permission{
			// Signed-in users may read the catalogue.
			rbac.NewPermission(user, rbac.NewObjectAction(Namespace, "torrent", "query")),
			rbac.NewPermission(user, rbac.NewObjectAction(Namespace, "torrentContent", "query")),
		},
		slice.FlatMap([]rbac.SubjectRole{anon, user}, func(role rbac.SubjectRole) []rbac.Permission {
			return []rbac.Permission{
				// Login and registration.
				rbac.NewPermission(role, rbac.NewObjectAction(Namespace, "self", "mutate")),
				rbac.NewPermission(role, rbac.NewObjectAction(Namespace, "self", "query")),
				// Harmless, and needed by the web UI shell before login.
				rbac.NewPermission(role, rbac.NewObjectAction(Namespace, "health", "query")),
				rbac.NewPermission(role, rbac.NewObjectAction(Namespace, "version", "query")),
			}
		})...,
	)
}

// ObjectActions turns every @auth directive in the schema into a registered
// object action, so the permission model knows the full set without it being
// listed anywhere by hand.
func ObjectActions(directives directive.AuthDirectives) []rbac.ObjectAction {
	return slice.Map(slices.Collect(maps.Keys(directives)), func(dir directive.AuthDirective) rbac.ObjectAction {
		return rbac.NewObjectAction(Namespace, dir.Object, dir.Action)
	})
}
