package rbac

import (
	"cmp"

	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/slice"
)

// type ObjectAction interface {
// 	Namespace() string
// 	Object() string
// 	Action() string
// 	Compare(other ObjectAction) int
// }

type ObjectAction struct {
	Namespace string
	Object    string
	Action    string
}

// func (oa objectAction) Namespace() string {
// 	return oa.namespace
// }

// func (oa objectAction) Object() string {
// 	return oa.object
// }

// func (oa objectAction) Action() string {
// 	return oa.action
// }

func (oa ObjectAction) Compare(other ObjectAction) int {
	r := cmp.Compare(oa.Namespace, other.Namespace)
	if r != 0 {
		return r
	}

	r = cmp.Compare(oa.Object, other.Object)
	if r != 0 {
		return r
	}

	return cmp.Compare(oa.Action, other.Action)
}

func NewObjectAction(namespace string, object string, action string) ObjectAction {
	return ObjectAction{
		Namespace: namespace,
		Object:    object,
		Action:    action,
	}
}

// ObjectActionsFromAPIKeyPermissions reads an API key's stored rows as the
// object actions they name. It lives here rather than on model.APIKeyPermission
// because rbac imports model, not the other way round, and the row type is
// generated.
func ObjectActionsFromAPIKeyPermissions(permissions []model.APIKeyPermission) []ObjectAction {
	return slice.Map(permissions, func(perm model.APIKeyPermission) ObjectAction {
		return NewObjectAction(perm.Namespace, perm.Object, perm.Action)
	})
}
