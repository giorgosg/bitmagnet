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
		Permissions: append(slice.Map(a.Permissions, func(perm model.APIKeyPermission) rbac.ObjectAction {
			return rbac.ObjectAction{
				Namespace: perm.Namespace,
				Object:    perm.Object,
				Action:    perm.Action,
			}
		}), slice.Map(a.anon.Permissions, func(perm rbac.Permission) rbac.ObjectAction {
			return perm.ObjectAction()
		})...),
	}
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
			{rbac.SubjectRole{Role: rbac.Role(a.User.RoleName)}},
			append(slice.Map(a.Permissions, func(perm model.APIKeyPermission) rbac.Subject {
				return rbac.SubjectPermission{
					ObjectAction: rbac.ObjectAction{
						Namespace: perm.Namespace,
						Object:    perm.Object,
						Action:    perm.Action,
					},
				}
			}), rbac.SubjectRole{Role: a.anon.Role}),
		},
		objectAction,
	)
}
