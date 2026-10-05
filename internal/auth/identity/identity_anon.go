package identity

import (
	"context"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/slice"
)

type Anon struct {
	rbac.RoleInfo
	enforcer rbac.Enforcer
}

func (Anon) Self() Self {
	return Self{}
}

// EffectivePermissions reports what an anonymous caller can currently reach,
// which is not the same as what the anon Role is configured with.
//
// The Role arrives here through GetRole, which reports stored grants truthfully
// whether or not `auth.anonymous_access` currently honours them - that is
// deliberate, because role administration has to show what is configured. This
// field is the other question, and the answer has to agree with Enforce: a
// closed instance whose administrator has granted the anon role something must
// not advertise it as effective while refusing every request for it.
//
// Asking the enforcer rather than filtering here is the same choice APIKey's
// EffectivePermissions makes, and for the same reason: role permissions are
// stored as glob patterns, so an intersection computed by equality would be a
// second source of truth that can drift from the decision it describes.
func (a Anon) EffectivePermissions(ctx context.Context) ([]rbac.ObjectAction, error) {
	return a.enforcer.FilterAllowed(
		ctx,
		[]rbac.Subject{rbac.SubjectRole{Role: a.Role}},
		slice.Map(a.Permissions, func(perm rbac.Permission) rbac.ObjectAction {
			return perm.ObjectAction()
		}),
	)
}

func (a Anon) Enforce(ctx context.Context, objectAction rbac.ObjectAction) (bool, error) {
	return a.enforcer.Enforce(
		ctx,
		rbac.SubjectRole{Role: a.Role},
		objectAction,
	)
}
