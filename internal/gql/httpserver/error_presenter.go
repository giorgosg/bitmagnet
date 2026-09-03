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
	target error
	code   string
	// message overrides the sentinel's own text. Leave it empty -- which all but
	// one of these do -- and target.Error() is presented, so the public wording
	// cannot drift away from the error it describes. Set it only where the two
	// must differ, and say why.
	message string
}

// presentation resolves the classification into what the client is told.
func (c errorClassification) presentation() errorPresentation {
	message := c.message
	if message == "" {
		message = c.target.Error()
	}

	return errorPresentation{
		code:    c.code,
		message: message,
	}
}

// serviceErrorPresentations maps the auth services' sentinel errors to their
// public code. It covers more than package user since createAPIKey started
// refusing unregistered object actions.
var serviceErrorPresentations = []errorClassification{
	{target: user.ErrCredentialsInvalid, code: ErrorCodeInvalidCredentials},
	{target: user.ErrDisabled, code: ErrorCodeUserDisabled},
	{target: user.ErrLoginRequestLimiter, code: ErrorCodeLoginThrottled},
	{target: user.ErrAlreadyExists, code: ErrorCodeUserAlreadyExists},
	{target: user.ErrUsernameInvalid, code: ErrorCodeUsernameInvalid},
	{target: user.ErrInvitationCodeMissing, code: ErrorCodeInvitationRequired},
	{
		target: user.ErrInvitationNotFound,
		code:   ErrorCodeInvitationInvalid,
		// The one deliberate divergence. The sentinel reads "invitation not
		// found"; the public wording must not distinguish a code that does not
		// exist from one that does but cannot be used, or registration becomes an
		// oracle for guessing invitation codes.
		message: "invitation is invalid",
	},
	{target: user.ErrInvitationExpired, code: ErrorCodeInvitationExpired},
	{target: user.ErrInvitationClaimed, code: ErrorCodeInvitationClaimed},
	{target: user.ErrEmailMissing, code: ErrorCodeEmailRequired},
	{target: user.ErrEmailInvalid, code: ErrorCodeEmailInvalid},
	{target: user.ErrPasswordInsufficientEntropy, code: ErrorCodePasswordInsufficientEntropy},
	{target: user.ErrRoleNotFound, code: ErrorCodeRoleNotFound},
	{target: api_key.ErrPermissionInvalid, code: ErrorCodePermissionInvalid},
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
			return candidate.presentation(), true
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
