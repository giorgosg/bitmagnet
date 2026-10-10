package auth

import (
	"context"
	"errors"

	"github.com/99designs/gqlgen/graphql"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
)

var ErrUnauthorized = errors.New("unauthorized")

type unauthorizedError struct {
	objAct    rbac.ObjectAction
	anonymous bool
}

func IsAnonymousRefusal(err error) bool {
	var refusal unauthorizedError
	return errors.As(err, &refusal) && refusal.anonymous
}

func (unauthorizedError) Error() string {
	return ErrUnauthorized.Error()
}

func (unauthorizedError) Unwrap() error {
	return ErrUnauthorized
}

// RefusedObjectAction extracts the Object action attached to an authorization
// refusal. Keeping this detail on the typed error lets the HTTP presenter add
// stable GraphQL extensions without making gqlgen-specific behavior part of
// the authorization directive.
func RefusedObjectAction(err error) (rbac.ObjectAction, bool) {
	var refusal unauthorizedError
	if !errors.As(err, &refusal) {
		return rbac.ObjectAction{}, false
	}

	return refusal.objAct, true
}

type Directive func(
	ctx context.Context,
	obj any,
	next graphql.Resolver,
	object string,
	action string,
) (res any, err error)

const Namespace = "graphql"

func NewDirective() Directive {
	return func(
		ctx context.Context,
		_ any,
		next graphql.Resolver,
		object string,
		action string,
	) (res any, err error) {
		if err := AuthenticationErrorFromContext(ctx); err != nil {
			return nil, err
		}

		allow := false
		anonymous := true

		objAct := rbac.ObjectAction{
			Namespace: Namespace,
			Object:    object,
			Action:    action,
		}

		identity, ok := IdentityFromContext(ctx)
		if ok {
			self := identity.Self()
			anonymous = self.User == nil && self.APIKey == nil

			var err error

			allow, err = identity.Enforce(ctx, objAct)
			if err != nil {
				return nil, err
			}
		}

		if !allow {
			return nil, unauthorizedError{
				objAct:    objAct,
				anonymous: anonymous,
			}
		}

		return next(ctx)
	}
}
