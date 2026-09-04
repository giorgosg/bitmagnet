// Package authcmd carries the operator-facing authentication commands.
//
// It is part of the shipped command surface rather than internal/dev because
// its subject is a real deployment: the first administrator of an instance
// somebody is running.
package authcmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/user"
	"github.com/urfave/cli/v2"
	"go.uber.org/fx"
)

type Params struct {
	fx.In
	UserService user.Service
	Config      authconfig.Config
}

type Result struct {
	fx.Out
	Command *cli.Command `group:"commands"`
}

func New(p Params) (Result, error) {
	cmd := &cli.Command{
		Name:  "auth",
		Usage: "Authentication tasks that require access to the machine",
		Subcommands: []*cli.Command{
			{
				Name:  "initial-invitation",
				Usage: "Prints the outstanding first-administrator invitation code",
				Description: "The invitation that lets the first administrator register is " +
					"created at startup while no administrator exists, and its code is logged " +
					"once. This prints it again for an operator who did not capture that line, " +
					"or who inherited a running instance.\n\n" +
					"The code is never exposed over the API: reaching it requires access to " +
					"the machine, which is the same boundary the log line had. Nothing is " +
					"created — an instance deliberately left without an administrator " +
					"invitation still has none afterwards.\n\n" +
					"The code alone is written to stdout, so it can be piped; everything " +
					"else goes to stderr.",
				Action: func(ctx *cli.Context) error {
					result, err := p.UserService.GetInitialInvitation(ctx.Context)
					if err != nil {
						return err
					}

					writeInitialInvitation(
						ctx.App.Writer,
						ctx.App.ErrWriter,
						result,
						p.Config.InvitationRequired,
					)

					return nil
				},
			},
		},
	}

	return Result{Command: cmd}, nil
}

// writeInitialInvitation reports one of the states an operator actually hits.
// None of them is reported by printing nothing: "no output" is
// indistinguishable from a command that failed to run.
//
// The split between the streams is load-bearing. The code is a credential that
// will be piped or copied, so stdout carries it and nothing else; prose on the
// same stream ends up pasted into the registration form along with it.
//
// No URL is offered, deliberately. bitmagnet does not know its own external
// scheme, host, port, or the path a UI is mounted under behind a reverse proxy,
// so any link it built would be wrong exactly when the operator could least
// afford it. The code alone is the deliverable; docs/auth.md says where to
// paste it.
func writeInitialInvitation(
	out, errOut io.Writer,
	result user.InitialInvitation,
	invitationRequired bool,
) {
	switch result.Status {
	case user.InitialInvitationUnclaimed:
		_, _ = fmt.Fprintln(out, result.Code)
		_, _ = fmt.Fprintln(errOut,
			"An unclaimed first-administrator invitation is outstanding.")
		_, _ = fmt.Fprintln(errOut,
			"Paste the code into the Invitation field of the registration form;",
			"registering with it creates the first administrator.")

		writeInvitationsOffNote(errOut, invitationRequired,
			"only this one grants the administrator role.")

	case user.InitialInvitationNotRequired:
		// Deliberately says nothing about the invitations table. The lookup
		// stops at the first administrator without reading it, so an
		// unattributed admin invitation can still be live -- promote a user to
		// admin while the bootstrap one is unclaimed and it is.
		_, _ = fmt.Fprintln(errOut,
			"An administrator already exists, so no bootstrap invitation is needed.")
		writeInvitationsOffNote(errOut, invitationRequired,
			"registering without an invitation creates an ordinary user,",
			"not an administrator.")

	case user.InitialInvitationNone:
		_, _ = fmt.Fprintln(errOut,
			"No invitation is outstanding, and this instance has no administrator.")
		_, _ = fmt.Fprintln(errOut,
			"One is created while no administrator exists, when the workers start.",
			"Run `bitmagnet worker run --all`, then run this command again.")

		writeInvitationsOffNote(errOut, invitationRequired,
			"registering without an invitation creates an ordinary user,",
			"not an administrator.")

	case user.InitialInvitationCreated:
		// Unreachable: GetInitialInvitation does not create, so it never
		// reports this. Handled rather than defaulted so that the one status
		// this command must never see says so out loud instead of leaving a
		// silent empty result behind.
		_, _ = fmt.Fprintln(errOut,
			"Unexpected status: this command does not create invitations.")
	}
}

// writeInvitationsOffNote says that registration needs no code, in every state.
// An operator who has turned invitations off and is hunting for one needs to be
// told regardless of which state they land in, so this is not attached to any
// single branch. The trailing clause says what registering without one gets you
// in that state, because "not needed" alone reads as "you are already an admin".
func writeInvitationsOffNote(errOut io.Writer, invitationRequired bool, because ...string) {
	if invitationRequired {
		return
	}

	_, _ = fmt.Fprintln(errOut, strings.Join(append(
		[]string{"auth.invitation_required is off, so a code is not needed to register;"},
		because...,
	), " "))
}
