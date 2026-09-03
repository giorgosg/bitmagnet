package identity

import (
	"context"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/slice"
)

type APIKey struct {
	model.APIKey
	anon     rbac.RoleInfo
	enforcer rbac.Enforcer
}

func (a APIKey) Self() Self {
	return Self{
		User:   &a.User,
		APIKey: &a.APIKey,
	}
}

// EffectivePermissions reports what this key can currently reach, which is not
// the same as what it was scoped to.
//
// Enforce requires both the owning User's Role and (the key's own selection or
// anon). This reported the selection concatenated with anon's and never
// intersected it with the Role, so the two disagreed: a key selected for
// torrent:delete whose owner is moved from admin to user kept listing
// torrent:delete while the enforcer refused it. Presenting a set the server
// will not honour is the whole purpose of the field defeated.
//
// The selection itself stays visible - it is a property of the key, reported by
// APIKey.Permissions - so a client can show what was selected alongside what is
// currently effective.
func (a APIKey) EffectivePermissions(ctx context.Context) ([]rbac.ObjectAction, error) {
	// Candidates are what the second gate accepts: the key's own selection, or
	// the anonymous role's permissions.
	candidates := append(
		a.selectedObjectActions(),
		slice.Map(a.anon.Permissions, func(perm rbac.Permission) rbac.ObjectAction {
			return perm.ObjectAction()
		})...,
	)

	// The first gate then narrows them, named exactly as Enforce names it, so the
	// report and the decision cannot disagree about the subject.
	//
	// Asking the enforcer rather than intersecting the two lists here is
	// deliberate: role permissions are stored as glob patterns - admin's is
	// "**::**::**" - so equality would report that an admin's key holds nothing.
	return a.enforcer.FilterAllowed(
		ctx,
		[]rbac.Subject{a.roleSubject()},
		candidates,
	)
}

// Enforce gates on two things: the User's role must allow the action, and the key's
// own permissions or the anonymous role must allow it too. A key can therefore
// never exceed its User.
//
// Both gates go in one EnforceEvery call. Asked separately they cost two
// acquisitions of the rbac service's process-global semaphore for one decision,
// and the @auth directive fires per field - so an N-field query serialised 2N
// times where N would do.
func (a APIKey) Enforce(ctx context.Context, objectAction rbac.ObjectAction) (bool, error) {
	return a.enforcer.EnforceEvery(
		ctx,
		[][]rbac.Subject{
			{a.roleSubject()},
			append(slice.Map(
				a.selectedObjectActions(),
				func(objectAction rbac.ObjectAction) rbac.Subject {
					return rbac.SubjectPermission{ObjectAction: objectAction}
				},
			), rbac.SubjectRole{Role: a.anon.Role}),
		},
		objectAction,
	)
}

// roleSubject names the first gate's subject: the role of the user who owns the
// key. Enforce and EffectivePermissions must derive it identically or the report
// and the decision disagree, which is exactly the defect this method exists to
// make impossible to reintroduce.
func (a APIKey) roleSubject() rbac.Subject {
	return rbac.SubjectRole{Role: rbac.Role(a.User.RoleName)}
}

// selectedObjectActions is the key's own permission selection, as object
// actions. Both gates read the same stored rows, so they convert them the same
// way.
func (a APIKey) selectedObjectActions() []rbac.ObjectAction {
	return rbac.ObjectActionsFromAPIKeyPermissions(a.Permissions)
}
