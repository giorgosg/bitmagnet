package gqlmodel

import (
	"database/sql"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/gql/gqlmodel/gen"
	"github.com/bitmagnet-io/bitmagnet/internal/slice"
)

// Role is the GraphQL projection of rbac.RoleInfo. The schema's Role carries
// resolved permissions, whereas model.Role carries raw join rows, so the two are
// deliberately not the same type.
type Role struct {
	Name        string
	Core        bool
	Permissions []gen.Permission
}

func RoleFromInfo(info rbac.RoleInfo) Role {
	return Role{
		Name:        string(info.Role),
		Core:        info.Core,
		Permissions: PermissionsToGql(info.Permissions),
	}
}

func RolesFromInfo(infos []rbac.RoleInfo) []Role {
	return slice.Map(infos, RoleFromInfo)
}

func ObjectActionToGql(objAct rbac.ObjectAction) gen.AuthObjectAction {
	return gen.AuthObjectAction{
		Namespace: objAct.Namespace,
		Object:    objAct.Object,
		Action:    objAct.Action,
	}
}

func ObjectActionsToGql(objActs []rbac.ObjectAction) []gen.AuthObjectAction {
	return slice.Map(objActs, ObjectActionToGql)
}

func ObjectActionsFromGql(inputs []gen.AuthObjectActionInput) []rbac.ObjectAction {
	return slice.Map(inputs, func(in gen.AuthObjectActionInput) rbac.ObjectAction {
		return rbac.NewObjectAction(in.Namespace, in.Object, in.Action)
	})
}

func PermissionsToGql(permissions []rbac.Permission) []gen.Permission {
	return slice.Map(permissions, func(p rbac.Permission) gen.Permission {
		return gen.Permission{
			Subject: gen.AuthSubject{
				Type: gen.AuthSubjectType(p.SubjectType()),
				Name: p.SubjectName(),
			},
			ObjectAction: ObjectActionToGql(p.ObjectAction()),
			Core:         p.Core(),
		}
	})
}

// NullTimePtr converts the nullable timestamps the generated models use into the
// pointers gqlgen expects for a nullable DateTime.
func NullTimePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}

	return &t.Time
}

func TimePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}

	return &t
}

// paginationParams flattens the schema's PaginationInput onto the limit/page/offset
// triple the services take.
func paginationParams(o graphql.Omittable[*gen.PaginationInput]) (limit, page, offset int) {
	input, ok := optional(o)
	if !ok {
		return 0, 0, 0
	}

	return optionalOr(input.Limit), optionalOr(input.Page), optionalOr(input.Offset)
}

// optional unwraps a gqlgen Omittable nullable input field. The schema config
// sets nullable_input_omittable, so every optional field arrives wrapped.
func optional[T any](o graphql.Omittable[*T]) (T, bool) {
	if v, ok := o.ValueOK(); ok && v != nil {
		return *v, true
	}

	var zero T

	return zero, false
}

func optionalOr[T any](o graphql.Omittable[*T]) T {
	v, _ := optional(o)

	return v
}
