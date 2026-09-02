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

// EffectivePermissions is the anonymous Role, verbatim, for the same reason as
// User's.
func (a Anon) EffectivePermissions(_ context.Context) ([]rbac.ObjectAction, error) {
	return slice.Map(a.Permissions, func(perm rbac.Permission) rbac.ObjectAction {
		return perm.ObjectAction()
	}), nil
}

func (a Anon) Enforce(ctx context.Context, objectAction rbac.ObjectAction) (bool, error) {
	return a.enforcer.Enforce(
		ctx,
		rbac.SubjectRole{Role: a.Role},
		objectAction,
	)
}
