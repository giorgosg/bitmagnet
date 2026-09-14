package gqlmodel

import (
	"context"
	"errors"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/api_key"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/browser_session"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/user"
	gqlauth "github.com/bitmagnet-io/bitmagnet/internal/gql/auth"
	"github.com/bitmagnet-io/bitmagnet/internal/gql/gqlmodel/gen"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
)

// ErrNotAuthenticated is returned by the fields describing the current identity
// when the request carries none.
var ErrNotAuthenticated = errors.New("not authenticated")

type SelfQuery struct {
	UserService   user.Service
	APIKeyService api_key.Service
}

func (SelfQuery) Identity(ctx context.Context) (gen.Self, error) {
	id, ok := gqlauth.IdentityFromContext(ctx)
	if !ok {
		return gen.Self{}, ErrNotAuthenticated
	}

	self := id.Self()

	permissions, err := id.EffectivePermissions(ctx)
	if err != nil {
		return gen.Self{}, err
	}

	return gen.Self{
		User:        self.User,
		APIKey:      self.APIKey,
		Permissions: ObjectActionsToGql(permissions),
	}, nil
}

func (q SelfQuery) PasswordEntropy(_ context.Context, password string) gen.PasswordEntropyResult {
	result := q.UserService.PasswordEntropy(password)

	return gen.PasswordEntropyResult{
		Entropy:    result.Entropy,
		MinEntropy: result.MinEntropy,
		Valid:      result.Valid,
	}
}

// APIKeys lists the caller's own keys. It requires an interactive session, not
// merely a user, for the same reason creating and deleting one does: the
// inventory is part of key management. A key scoped to Torznab alone could
// otherwise enumerate its owner's other credentials — their IDs, names, creation
// times and expirations — which is exactly the reconnaissance that scoping a key
// narrowly is meant to prevent.
func (q SelfQuery) APIKeys(ctx context.Context) ([]model.APIKey, error) {
	currentUser, err := gqlauth.UserSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}

	result, err := q.APIKeyService.List(ctx, api_key.ListRequest{UserID: currentUser.ID})
	if err != nil {
		return nil, err
	}

	return result.APIKeys, nil
}

type SelfMutation struct {
	UserService   user.Service
	APIKeyService api_key.Service
	RBACService   rbac.Service
	BrowserCookie browser_session.Cookie
}

func (m SelfMutation) Register(ctx context.Context, input gen.RegisterInput) (gen.RegisterResult, error) {
	req := user.RegisterRequest{
		Username: input.Username,
		Password: input.Password,
	}
	req.InvitationCode = optionalOr(input.InvitationCode)
	req.Email = optionalOr(input.Email)

	registered, err := m.UserService.Register(ctx, req)
	if err != nil {
		return gen.RegisterResult{}, err
	}

	return gen.RegisterResult{User: registered}, nil
}

func (m SelfMutation) Login(ctx context.Context, username, password string) (gen.LoginResult, error) {
	result, err := m.UserService.Login(ctx, username, password)
	if err != nil {
		return gen.LoginResult{}, err
	}

	// The schema returns the permissions the session confers; the login result
	// itself carries only the token and user, so they are read from the role.
	roleInfo, err := m.RBACService.GetRole(ctx, rbac.Role(result.User.RoleName))
	if err != nil {
		return gen.LoginResult{}, err
	}

	return gen.LoginResult{
		Token:       result.Token,
		User:        result.User,
		Permissions: PermissionsToGql(roleInfo.Permissions),
	}, nil
}

func (m SelfMutation) LoginBrowser(ctx context.Context, username, password string) (*string, error) {
	result, err := m.UserService.Login(ctx, username, password)
	if err != nil {
		return nil, err
	}

	return nil, m.BrowserCookie.Issue(ctx, result.Token)
}

// LogoutBrowser clears the cookie and revokes the session it carried. Clearing
// the cookie alone only drops the client's copy: the JWT inside it stays valid
// for its full lifetime, so anything that captured it before the logout kept the
// account until the token expired.
//
// Revoking ends every session for that user, not only this one — see
// user.RevokeSessions. Anonymous callers reach this by design, and revoke
// nothing; an API-key identity is refused a revocation for the same reason it is
// refused key management, so a narrowly scoped key cannot log its owner out.
func (m SelfMutation) LogoutBrowser(ctx context.Context) (*string, error) {
	if currentUser, err := gqlauth.UserSessionFromContext(ctx); err == nil {
		if revokeErr := m.UserService.RevokeSessions(ctx, currentUser.ID); revokeErr != nil {
			return nil, revokeErr
		}
	}

	return nil, m.BrowserCookie.Expire(ctx)
}

// UpdatePassword rotates the caller's own password, and with it every session
// the old password opened — including the caller's, which has to log in again.
//
// Re-issuing a token here instead would keep the caller signed in, and is what a
// larger contract would do: it means answering with a credential, and for the
// cookie transport also writing a new one. Ending the session is the simpler
// promise, and the safer default for the case this exists to serve, which is a
// password rotated because something was leaked.
func (m SelfMutation) UpdatePassword(ctx context.Context, input gen.UpdatePasswordInput) (*string, error) {
	currentUser, err := gqlauth.UserSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}

	return nil, m.UserService.UpdatePassword(ctx, currentUser.ID, input.CurrentPassword, input.NewPassword)
}

func (m SelfMutation) CreateAPIKey(ctx context.Context, input gen.CreateAPIKeyInput) (gen.CreateAPIKeyResult, error) {
	currentUser, err := gqlauth.UserSessionFromContext(ctx)
	if err != nil {
		return gen.CreateAPIKeyResult{}, err
	}

	req := api_key.CreateRequest{
		UserID:      currentUser.ID,
		Name:        input.Name,
		Permissions: ObjectActionsFromGql(input.Permissions),
	}
	req.Expiry = optionalOr(input.Expiry)

	result, err := m.APIKeyService.Create(ctx, req)
	if err != nil {
		return gen.CreateAPIKeyResult{}, err
	}

	return gen.CreateAPIKeyResult{
		ID:        result.ID,
		APIKey:    result.APIKey,
		Name:      result.Name,
		ExpiresAt: TimePtr(result.ExpiresAt),
	}, nil
}

func (m SelfMutation) DeleteAPIKey(ctx context.Context, id int) (*string, error) {
	currentUser, err := gqlauth.UserSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}

	return nil, m.APIKeyService.Delete(ctx, api_key.DeleteRequest{
		APIKeyID: id,
		UserID:   currentUser.ID,
	})
}

type AuthQuery struct {
	UserService user.Service
	RBACService rbac.Service
}

func (q AuthQuery) ListUsers(ctx context.Context, input *gen.ListUsersInput) (gen.ListUsersResult, error) {
	params := user.ListUsersParams{}
	if input != nil {
		params.UsernameLike = optionalOr(input.UsernameLike)
		params.Limit, params.Page, params.Offset = paginationParams(input.Pagination)
	}

	result, err := q.UserService.List(ctx, params)
	if err != nil {
		return gen.ListUsersResult{}, err
	}

	return gen.ListUsersResult{Users: result.Users, TotalCount: result.TotalCount}, nil
}

func (q AuthQuery) ListRoles(ctx context.Context) ([]Role, error) {
	roles, err := q.RBACService.GetAllRoles(ctx)
	if err != nil {
		return nil, err
	}

	return RolesFromInfo(roles), nil
}

func (q AuthQuery) ListObjectActions() []gen.AuthObjectAction {
	return ObjectActionsToGql(q.RBACService.GetObjectActions())
}

func (q AuthQuery) ListInvitations(
	ctx context.Context,
	input *gen.ListInvitationsInput,
) (gen.ListInvitationsResult, error) {
	params := user.ListInvitationsParams{}
	if input != nil {
		params.Limit, params.Page, params.Offset = paginationParams(input.Pagination)
	}

	result, err := q.UserService.ListInvitations(ctx, params)
	if err != nil {
		return gen.ListInvitationsResult{}, err
	}

	return gen.ListInvitationsResult{Invitations: result.Invitations, TotalCount: result.TotalCount}, nil
}

type AuthMutation struct {
	UserService user.Service
	RBACService rbac.Service
}

func (m AuthMutation) SetUserRole(ctx context.Context, userID int, roleName string) (model.User, error) {
	return m.UserService.SetRole(ctx, userID, roleName)
}

func (m AuthMutation) SetUserEnabled(ctx context.Context, userID int, enabled bool) (model.User, error) {
	return m.UserService.SetEnabled(ctx, userID, enabled)
}

func (m AuthMutation) DeleteUser(ctx context.Context, userID int) (*string, error) {
	return nil, m.UserService.Delete(ctx, userID)
}

func (m AuthMutation) PutRole(
	ctx context.Context,
	role string,
	objectActions []gen.AuthObjectActionInput,
) (Role, error) {
	info, err := m.RBACService.PutRole(ctx, rbac.Role(role), ObjectActionsFromGql(objectActions))
	if err != nil {
		return Role{}, err
	}

	return RoleFromInfo(info), nil
}

func (m AuthMutation) DeleteRole(ctx context.Context, role string) (*string, error) {
	return nil, m.RBACService.DeleteRole(ctx, rbac.Role(role))
}

func (m AuthMutation) Invite(ctx context.Context, input gen.InviteInput) (model.Invitation, error) {
	req := user.InviteRequest{}
	req.Email = optionalOr(input.Email)
	req.Role = optionalOr(input.Role)
	req.Expiry = optionalOr(input.Expiry)

	// The invitation records who issued it; anonymous callers cannot.
	currentUser, ok := gqlauth.UserFromContext(ctx)
	if !ok {
		return model.Invitation{}, ErrNotAuthenticated
	}

	req.CreatedBy = currentUser.ID

	return m.UserService.Invite(ctx, req)
}

func (m AuthMutation) DeleteInvitation(ctx context.Context, code string) (*string, error) {
	return nil, m.UserService.DeleteInvitation(ctx, code)
}
