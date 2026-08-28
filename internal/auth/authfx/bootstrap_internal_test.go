package authfx

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/user"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// stubUserService answers CreateInitialInvitation and nothing else. The embedded
// interface is nil, so any other call panics rather than passing silently.
type stubUserService struct {
	user.Service

	result user.InitialInvitation
}

func (s stubUserService) CreateInitialInvitation(context.Context) (user.InitialInvitation, error) {
	return s.result, nil
}

// runBootstrap starts the bootstrap hook against a fixed invitation and returns
// everything it logged, rendered so a search covers messages and fields alike.
func runBootstrap(t *testing.T, status user.InitialInvitationStatus, code string) string {
	t.Helper()

	core, logs := observer.New(zap.DebugLevel)

	hook := bootstrapHook(bootstrapParams{
		UserService: stubUserService{result: user.InitialInvitation{
			Invitation: model.Invitation{Code: code},
			Status:     status,
		}},
		Logger: zap.New(core).Sugar(),
	})

	require.NoError(t, hook.OnStart(t.Context()))

	var rendered strings.Builder

	for _, entry := range logs.All() {
		fmt.Fprintf(&rendered, "%s %v\n", entry.Message, entry.ContextMap())
	}

	return rendered.String()
}

const bootstrapTestCode = "0123456789abcdef0123456789ab4f2a"

func TestBootstrap_LogsTheCodeWhenItCreatesTheInvitation(t *testing.T) {
	t.Parallel()

	logged := runBootstrap(t, user.InitialInvitationCreated, bootstrapTestCode)

	assert.Contains(t, logged, bootstrapTestCode,
		"the operator has no other way in; the code must be logged when it is created")
}

func TestBootstrap_DoesNotRelogTheCodeWhenTheInvitationIsUnclaimed(t *testing.T) {
	t.Parallel()

	logged := runBootstrap(t, user.InitialInvitationUnclaimed, bootstrapTestCode)

	assert.NotContains(t, logged, bootstrapTestCode,
		"the unclaimed reminder re-logs a live admin credential on every boot")
	assert.Contains(t, logged, "4f2a",
		"the operator still needs to tell which invitation is outstanding")
}

func TestBootstrap_LogsNothingWhenNoInvitationIsNeeded(t *testing.T) {
	t.Parallel()

	assert.Empty(t, runBootstrap(t, user.InitialInvitationNotRequired, bootstrapTestCode))
}
