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
// register, and logs its code once. Without it an installation that enables
// authentication has no way in.
//
// next runs this as a short-lived autostart worker in its plugin; the equivalent
// here is a startup hook in the same worker group the rest of the app uses.
func newBootstrapWorker(p bootstrapParams) bootstrapResult {
	return bootstrapResult{
		Worker: worker.NewWorker("auth_initial_invitation", bootstrapHook(p)),
	}
}

// invitationCodeSuffixLength is how much of an invitation code identifies it in a
// log. Codes are 32 hex characters, so four leave 112 bits unrevealed.
const invitationCodeSuffixLength = 4

// invitationCodeSuffix fingerprints an outstanding invitation without reprinting
// it. A code too short for four characters to be a small fraction of it gets no
// fingerprint at all, rather than most of itself.
func invitationCodeSuffix(code string) string {
	if len(code) < 2*invitationCodeSuffixLength {
		return ""
	}

	return code[len(code)-invitationCodeSuffixLength:]
}

func bootstrapHook(p bootstrapParams) fx.Hook {
	logger := p.Logger.Named("auth")

	return fx.Hook{
		OnStart: func(ctx context.Context) error {
			result, err := p.UserService.CreateInitialInvitation(ctx)
			if err != nil {
				return err
			}

			switch result.Status {
			case user.InitialInvitationCreated:
				logger.Warnw("initial invitation created", "code", result.Code)
			case user.InitialInvitationUnclaimed:
				// Deliberately not the code. This branch runs on every boot until
				// somebody claims it, so logging the credential here turns any log
				// file, aggregator, support bundle or screenshot into a path to the
				// first administrator account. The suffix is enough to match the
				// reminder against the code that was issued.
				logger.Warnw("initial invitation unclaimed",
					"code_suffix", invitationCodeSuffix(result.Code))
			case user.InitialInvitationNotRequired:
			}

			return nil
		},
	}
}
