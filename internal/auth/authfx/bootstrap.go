package authfx

import (
	"context"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/user"
	"github.com/bitmagnet-io/bitmagnet/internal/worker"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type bootstrapParams struct {
	fx.In
	UserService user.Service
	Logger      *zap.SugaredLogger
}

type bootstrapResult struct {
	fx.Out
	Worker worker.Worker `group:"workers"`
}

// newBootstrapWorker creates the invitation that lets the first administrator
// register, and logs its code. Without it an installation that enables
// authentication has no way in.
//
// next runs this as a short-lived autostart worker in its plugin; the equivalent
// here is a startup hook in the same worker group the rest of the app uses.
func newBootstrapWorker(p bootstrapParams) bootstrapResult {
	logger := p.Logger.Named("auth")

	return bootstrapResult{
		Worker: worker.NewWorker("auth_initial_invitation", fx.Hook{
			OnStart: func(ctx context.Context) error {
				result, err := p.UserService.CreateInitialInvitation(ctx)
				if err != nil {
					return err
				}

				switch result.Status {
				case user.InitialInvitationCreated:
					logger.Warnw("initial invitation created", "code", result.Code)
				case user.InitialInvitationUnclaimed:
					logger.Warnw("initial invitation unclaimed", "code", result.Code)
				case user.InitialInvitationNotRequired:
				}

				return nil
			},
		}),
	}
}
