package authcmd_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/app/cmd/authcmd"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/user"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

// stubUserService answers the retrieval and records whether the create-or-return
// was reached. The embedded interface is nil, so any other call panics rather
// than passing silently.
type stubUserService struct {
	user.Service

	result  user.InitialInvitation
	created bool
}

func (s *stubUserService) GetInitialInvitation(context.Context) (user.InitialInvitation, error) {
	return s.result, nil
}

func (s *stubUserService) CreateInitialInvitation(context.Context) (user.InitialInvitation, error) {
	s.created = true

	return s.result, nil
}

const testCode = "0123456789abcdef0123456789ab4f2a"

func unclaimed(code string) user.InitialInvitation {
	return user.InitialInvitation{
		Invitation: model.Invitation{Code: code},
		Status:     user.InitialInvitationUnclaimed,
	}
}

// run drives the real command through the real CLI, so the writers under test
// are the ones the app supplies in production.
func run(
	t *testing.T,
	result user.InitialInvitation,
	invitationRequired bool,
) (stdout, stderr string, svc *stubUserService) {
	t.Helper()

	svc = &stubUserService{result: result}

	config := authconfig.NewDefaultConfig()
	config.InvitationRequired = invitationRequired

	built, err := authcmd.New(authcmd.Params{UserService: svc, Config: config})
	require.NoError(t, err)

	var out, errOut bytes.Buffer

	app := &cli.App{
		Name:      "bitmagnet",
		Commands:  []*cli.Command{built.Command},
		Writer:    &out,
		ErrWriter: &errOut,
	}

	require.NoError(t, app.RunContext(
		t.Context(),
		[]string{"bitmagnet", "auth", "initial-invitation"},
	))

	return out.String(), errOut.String(), svc
}

// The command's output is a credential an operator will pipe or copy. Anything
// else on stdout ends up pasted into the registration form along with it.
func TestPrintsTheCodeAloneOnStdout(t *testing.T) {
	t.Parallel()

	stdout, _, _ := run(t, unclaimed(testCode), true)

	assert.Equal(t, testCode+"\n", stdout,
		"stdout carries the code and nothing else, so it can be piped")
}

// The explanation still has to reach a human, on the stream that does not
// corrupt the pipe.
func TestExplainsOnStderrWhereTheCodeGoes(t *testing.T) {
	t.Parallel()

	_, stderr, _ := run(t, unclaimed(testCode), true)

	assert.Contains(t, strings.ToLower(stderr), "invitation",
		"the operator has to be told which field the code goes into")
	assert.NotContains(t, stderr, testCode,
		"the code belongs on stdout; duplicating it invites the wrong copy")
}

// The spec forbids building a registration URL: bitmagnet does not know its own
// external scheme, host, port or mount path, so any link it emitted would point
// somewhere wrong.
func TestDoesNotInventARegistrationURL(t *testing.T) {
	t.Parallel()

	stdout, stderr, _ := run(t, unclaimed(testCode), true)

	assert.NotContains(t, stdout+stderr, "http://")
	assert.NotContains(t, stdout+stderr, "https://")
}

// Reading must never be the same act as issuing. This is the whole reason the
// command exists as a read.
func TestNeverCreatesAnInvitation(t *testing.T) {
	t.Parallel()

	for name, result := range map[string]user.InitialInvitation{
		"invitation outstanding": unclaimed(testCode),
		"administrator exists":   {Status: user.InitialInvitationNotRequired},
		"nothing outstanding":    {Status: user.InitialInvitationNone},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, _, svc := run(t, result, true)

			assert.False(t, svc.created,
				"the retrieval path must not reach the create-or-return")
		})
	}
}

// An instance that already has an administrator says so, rather than printing
// nothing and leaving the operator to guess whether the command worked.
func TestReportsAnExistingAdministrator(t *testing.T) {
	t.Parallel()

	stdout, stderr, _ := run(t, user.InitialInvitation{
		Status: user.InitialInvitationNotRequired,
	}, true)

	assert.Empty(t, stdout, "there is no code to pipe")
	assert.Contains(t, strings.ToLower(stderr), "administrator already exists")
}

// Neither an administrator nor an invitation: the state on an instance whose
// workers have never run.
func TestReportsNothingOutstanding(t *testing.T) {
	t.Parallel()

	stdout, stderr, _ := run(t, user.InitialInvitation{
		Status: user.InitialInvitationNone,
	}, true)

	assert.Empty(t, stdout)
	assert.Contains(t, strings.ToLower(stderr), "no invitation is outstanding")
}

// With invitations off, registration needs no code at all, and the operator has
// to be told that rather than left hunting for one. The acceptance criterion is
// not scoped to one state, so every state has to carry it.
func TestSaysACodeIsNotNeededWhenInvitationsAreOff(t *testing.T) {
	t.Parallel()

	for name, status := range map[string]user.InitialInvitationStatus{
		"nothing outstanding":    user.InitialInvitationNone,
		"administrator exists":   user.InitialInvitationNotRequired,
		"invitation outstanding": user.InitialInvitationUnclaimed,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, stderr, _ := run(t, user.InitialInvitation{Status: status}, false)

			assert.Contains(t, stderr,
				"auth.invitation_required is off, so a code is not needed to register;",
				"named, and rendered as a sentence rather than a formatted slice")
			assert.NotContains(t, stderr, "[auth.",
				"a []string handed to Fprintln prints its brackets")
		})
	}
}

// The report must claim only what it looked at. GetInitialInvitation returns as
// soon as it finds an administrator, without ever querying invitations, so an
// unattributed admin invitation can still be live in the table -- promote a user
// to admin while the bootstrap invitation is unclaimed and it is. Saying
// "no invitation is outstanding" there states something never checked.
func TestDoesNotClaimAnythingAboutInvitationsWhenAnAdministratorExists(t *testing.T) {
	t.Parallel()

	_, stderr, _ := run(t, user.InitialInvitation{
		Status: user.InitialInvitationNotRequired,
	}, true)

	assert.NotContains(t, strings.ToLower(stderr), "outstanding",
		"the admin check never reads the invitations table; it cannot report on it")
}
