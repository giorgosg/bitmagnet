package identity

import (
	"context"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
)

type Identity interface {
	Self() Self
	// EffectivePermissions reports the object actions this identity may
	// currently exercise.
	//
	// It is separate from Self because it can cost an authorization decision,
	// and Self is on the hot paths - the HTTP middleware and the Torznab
	// handler call it per request only to ask whether a user or a key is
	// present. Only the self.identity resolver needs the permissions.
	EffectivePermissions(context.Context) ([]rbac.ObjectAction, error)
	Enforce(context.Context, rbac.ObjectAction) (bool, error)
}

type Self struct {
	User   *model.User
	APIKey *model.APIKey
}
