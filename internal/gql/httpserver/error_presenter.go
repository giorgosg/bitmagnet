package httpserver

import (
	"context"
	"errors"

	"github.com/99designs/gqlgen/graphql"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/api_key"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/user"
	gqlauth "github.com/bitmagnet-io/bitmagnet/internal/gql/auth"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

const (
	ErrorCodeInvalidCredentials                  = "INVALID_CREDENTIALS"
	ErrorCodeUserDisabled                        = "USER_DISABLED"
	ErrorCodeLoginThrottled                      = "LOGIN_THROTTLED"
	ErrorCodeUserAlreadyExists                   = "USER_ALREADY_EXISTS"
	ErrorCodeUsernameInvalid                     = "USERNAME_INVALID"
	ErrorCodeInvitationRequired                  = "INVITATION_REQUIRED"
	ErrorCodeInvitationInvalid                   = "INVITATION_INVALID"
	ErrorCodeInvitationExpired                   = "INVITATION_EXPIRED"
	ErrorCodeInvitationClaimed                   = "INVITATION_CLAIMED"
	ErrorCodeEmailRequired                       = "EMAIL_REQUIRED"
	ErrorCodeEmailInvalid                        = "EMAIL_INVALID"
	ErrorCodePasswordInsufficientEntropy         = "PASSWORD_INSUFFICIENT_ENTROPY"
	ErrorCodeRoleNotFound                        = "ROLE_NOT_FOUND"
	ErrorCodePermissionInvalid                   = "PERMISSION_INVALID"
	ErrorCodeUnauthorized                        = "UNAUTHORIZED"
	ErrorCodeAuthenticationInfrastructureFailure = "AUTHENTICATION_INFRASTRUCTURE_FAILURE"
	ErrorCodeUserSessionRequired                 = "USER_SESSION_REQUIRED"
	ErrorCodeAPIKeyManagementForbidden           = "API_KEY_MANAGEMENT_FORBIDDEN"
	ErrorCodeInternalServer                      = "INTERNAL_SERVER_ERROR"
)

type errorPresentation struct {
	code       string
	message    string
	extensions map[string]any
}

type errorClassification struct {
	target       error
	presentation errorPresentation
}

// serviceErrorPresentations maps the auth services' sentinel errors to their
// public code. It covers more than package user since createAPIKey started
// refusing unregistered object actions.
var serviceErrorPresentations = []errorClassification{
	{
		target: user.ErrCredentialsInvalid,
		presentation: errorPresentation{
			code:    ErrorCodeInvalidCredentials,
			message: "invalid username or password",
		},
	},
	{
		target: user.ErrDisabled,
		presentation: errorPresentation{
			code:    ErrorCodeUserDisabled,
			message: "account is disabled",
		},
	},
	{
		target: user.ErrLoginRequestLimiter,
		presentation: errorPresentation{
			code:    ErrorCodeLoginThrottled,
			message: "too many login requests",
		},
	},
	{
		target: user.ErrAlreadyExists,
		presentation: errorPresentation{
			code:    ErrorCodeUserAlreadyExists,
			message: "user already exists",
		},
	},
	{
		target: user.ErrUsernameInvalid,
		presentation: errorPresentation{
			code:    ErrorCodeUsernameInvalid,
			message: "invalid username",
		},
	},
	{
		target: user.ErrInvitationCodeMissing,
		presentation: errorPresentation{
			code:    ErrorCodeInvitationRequired,
			message: "invitation code is required",
		},
	},
	{
		target: user.ErrInvitationNotFound,
		presentation: errorPresentation{
			code:    ErrorCodeInvitationInvalid,
			message: "invitation is invalid",
		},
	},
	{
		target: user.ErrInvitationExpired,
		presentation: errorPresentation{
			code:    ErrorCodeInvitationExpired,
			message: "invitation expired",
		},
	},
	{
		target: user.ErrInvitationClaimed,
		presentation: errorPresentation{
			code:    ErrorCodeInvitationClaimed,
			message: "invitation already claimed",
		},
	},
	{
		target: user.ErrEmailMissing,
		presentation: errorPresentation{
			code:    ErrorCodeEmailRequired,
			message: "email is required",
		},
	},
	{
		target: user.ErrEmailInvalid,
		presentation: errorPresentation{
			code:    ErrorCodeEmailInvalid,
			message: "invalid email",
		},
	},
	{
		target: user.ErrPasswordInsufficientEntropy,
		presentation: errorPresentation{
			code:    ErrorCodePasswordInsufficientEntropy,
			message: "password has insufficient entropy",
		},
	},
	{
		target: user.ErrRoleNotFound,
		presentation: errorPresentation{
			code:    ErrorCodeRoleNotFound,
			message: "role not found",
		},
	},
	{
		target: api_key.ErrPermissionInvalid,
		presentation: errorPresentation{
			code:    ErrorCodePermissionInvalid,
			message: "invalid api key permission",
		},
	},
}

func errorPresenter(ctx context.Context, err error) *gqlerror.Error {
	presented := graphql.DefaultErrorPresenter(ctx, err)
	if presented == nil {
		return nil
	}

	presentation, ok := classifyError(err)
	if !ok {
		if !hasInternalCause(err) {
			return presented
		}

		presentation = errorPresentation{
			code:    ErrorCodeInternalServer,
			message: "internal server error",
		}
	}

	presented.Message = presentation.message
	if presented.Extensions == nil {
		presented.Extensions = make(map[string]any, len(presentation.extensions)+1)
	}

	presented.Extensions["code"] = presentation.code
	for key, value := range presentation.extensions {
		presented.Extensions[key] = value
	}

	return presented
}

func classifyError(err error) (errorPresentation, bool) {
	if errors.Is(err, gqlauth.ErrAuthenticationInfrastructure) {
		return errorPresentation{
			code:    ErrorCodeAuthenticationInfrastructureFailure,
			message: "authentication service unavailable",
		}, true
	}

	if errors.Is(err, gqlauth.ErrUnauthorized) {
		objAct, ok := gqlauth.RefusedObjectAction(err)
		if !ok {
			return errorPresentation{}, false
		}

		return errorPresentation{
			code:    ErrorCodeUnauthorized,
			message: gqlauth.ErrUnauthorized.Error(),
			extensions: map[string]any{
				"namespace": objAct.Namespace,
				"object":    objAct.Object,
				"action":    objAct.Action,
			},
		}, true
	}

	if errors.Is(err, gqlauth.ErrNotAuthenticated) {
		return errorPresentation{
			code:    ErrorCodeUserSessionRequired,
			message: gqlauth.ErrNotAuthenticated.Error(),
		}, true
	}

	if errors.Is(err, gqlauth.ErrAPIKeyMayNotManageKeys) {
		return errorPresentation{
			code:    ErrorCodeAPIKeyManagementForbidden,
			message: gqlauth.ErrAPIKeyMayNotManageKeys.Error(),
		}, true
	}

	for _, candidate := range serviceErrorPresentations {
		if errors.Is(err, candidate.target) {
			return candidate.presentation, true
		}
	}

	return errorPresentation{}, false
}

func hasInternalCause(err error) bool {
	var gqlErr *gqlerror.Error
	if errors.As(err, &gqlErr) {
		return gqlErr.Err != nil
	}

	return true
}
