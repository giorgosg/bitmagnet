package user

import (
	"context"
	"sync"

	"github.com/bitmagnet-io/bitmagnet/internal/atomic"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/jwt"
	"github.com/bitmagnet-io/bitmagnet/internal/database"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
)

type Service interface {
	// CreateInitialInvitation checks if there is any admin user or admin invitation.
	// If none is found, it creates an admin invitation.
	CreateInitialInvitation(ctx context.Context) (InitialInvitation, error)
	// GetInitialInvitation reports the outstanding admin invitation and never
	// creates one. Callers answering "what is the code?" want this rather than
	// CreateInitialInvitation, which would issue an administrator invitation on
	// an instance deliberately left without one.
	GetInitialInvitation(ctx context.Context) (InitialInvitation, error)
	Invite(ctx context.Context, request InviteRequest) (model.Invitation, error)
	Register(ctx context.Context, request RegisterRequest) (model.User, error)
	Login(ctx context.Context, username, password string) (LoginResult, error)
	SetRole(ctx context.Context, userID int, roleName string) (model.User, error)
	UpdatePassword(ctx context.Context, userID int, currentPassword, newPassword string) error
	Get(ctx context.Context, userID int) (model.User, error)
	GetByUsername(ctx context.Context, username string) (model.User, error)
	List(ctx context.Context, params ListUsersParams) (ListUsersResult, error)
	Delete(ctx context.Context, userID int) error
	SetEnabled(ctx context.Context, userID int, enabled bool) (model.User, error)
	ListInvitations(ctx context.Context, params ListInvitationsParams) (ListInvitationsResult, error)
	DeleteInvitation(ctx context.Context, code string) error
	PasswordEntropy(password string) PasswordEntropyResult
}

type service struct {
	decoyOnce sync.Once
	decoyHash []byte

	database.DaoTransactionProvider
	jwtService          jwt.Service
	invitationRequired  *atomic.Value[InvitationRequired]
	emailRequired       *atomic.Value[EmailRequired]
	emailVerification   *atomic.Value[EmailVerification]
	passwordMinEntropy  *atomic.Value[PasswordMinEntropy]
	passwordHashingCost *atomic.Value[PasswordHashingCost]
	loginLimiter        *loginLimiter
}

// ConfigValues is the service's live configuration. The seven settings always
// travel together -- every caller built them from one authconfig.Config and then
// exploded them across an argument list long enough to need
// revive:disable:argument-limit -- so they are passed as the group they are.
//
// It lives here rather than in authconfig because authconfig imports this
// package; authconfig.UserConfigValues is an alias for it.
type ConfigValues struct {
	InvitationRequired     *atomic.Value[InvitationRequired]
	EmailRequired          *atomic.Value[EmailRequired]
	EmailVerification      *atomic.Value[EmailVerification]
	PasswordMinEntropy     *atomic.Value[PasswordMinEntropy]
	PasswordHashingCost    *atomic.Value[PasswordHashingCost]
	LoginRequestsPerMinute *atomic.Value[LoginRequestsPerMinute]
	LoginRequestBurst      *atomic.Value[LoginRequestBurst]
}

func NewService(
	daoProvider database.DaoTransactionProvider,
	jwtService jwt.Service,
	values ConfigValues,
) Service {
	return &service{
		DaoTransactionProvider: daoProvider,
		jwtService:             jwtService,
		invitationRequired:     values.InvitationRequired,
		emailRequired:          values.EmailRequired,
		emailVerification:      values.EmailVerification,
		passwordMinEntropy:     values.PasswordMinEntropy,
		passwordHashingCost:    values.PasswordHashingCost,
		loginLimiter: newLoginLimiter(
			values.LoginRequestsPerMinute,
			values.LoginRequestBurst,
		),
	}
}
