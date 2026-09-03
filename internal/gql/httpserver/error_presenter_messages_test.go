package httpserver

import (
	"errors"
	"fmt"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/api_key"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/user"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The integration suite asserts the error *codes* but never the messages, so
// the wording was free to drift. These cases pin it, written out literally
// rather than derived, so that a change to either the sentinel or the table has
// to be made here too and cannot happen by accident.
var wantPresentations = []struct {
	target  error
	code    string
	message string
}{
	{user.ErrCredentialsInvalid, ErrorCodeInvalidCredentials, "invalid username or password"},
	{user.ErrDisabled, ErrorCodeUserDisabled, "account is disabled"},
	{user.ErrLoginRequestLimiter, ErrorCodeLoginThrottled, "too many login requests"},
	{user.ErrAlreadyExists, ErrorCodeUserAlreadyExists, "user already exists"},
	{user.ErrUsernameInvalid, ErrorCodeUsernameInvalid, "invalid username"},
	{user.ErrInvitationCodeMissing, ErrorCodeInvitationRequired, "invitation code is required"},
	{user.ErrInvitationNotFound, ErrorCodeInvitationInvalid, "invitation is invalid"},
	{user.ErrInvitationExpired, ErrorCodeInvitationExpired, "invitation expired"},
	{user.ErrInvitationClaimed, ErrorCodeInvitationClaimed, "invitation already claimed"},
	{user.ErrEmailMissing, ErrorCodeEmailRequired, "email is required"},
	{user.ErrEmailInvalid, ErrorCodeEmailInvalid, "invalid email"},
	{
		user.ErrPasswordInsufficientEntropy,
		ErrorCodePasswordInsufficientEntropy,
		"password has insufficient entropy",
	},
	{user.ErrRoleNotFound, ErrorCodeRoleNotFound, "role not found"},
	{api_key.ErrPermissionInvalid, ErrorCodePermissionInvalid, "invalid api key permission"},
}

func TestServiceErrorPresentations(t *testing.T) {
	t.Parallel()

	require.Len(t, serviceErrorPresentations, len(wantPresentations),
		"a sentinel was added to or removed from the table without pinning its wording here")

	for _, want := range wantPresentations {
		t.Run(want.code, func(t *testing.T) {
			t.Parallel()

			// Wrapped, because that is how the services return them.
			got, ok := classifyError(fmt.Errorf("%w: %w", user.Err, want.target))
			require.True(t, ok, "sentinel is not classified")
			assert.Equal(t, want.code, got.code)
			assert.Equal(t, want.message, got.message)
		})
	}
}

// Every message but one repeats its sentinel's own text. Stating which one
// diverges, and asserting the rest do not, is what stops the table drifting
// away from the errors it describes.
func TestServiceErrorMessagesMatchTheirSentinel(t *testing.T) {
	t.Parallel()

	// user.ErrInvitationNotFound reads "invitation not found" internally and is
	// presented as "invitation is invalid" deliberately: the public wording must
	// not tell a caller whether a code they guessed exists.
	deliberatelyDifferent := map[string]bool{
		user.ErrInvitationNotFound.Error(): true,
	}

	for _, candidate := range serviceErrorPresentations {
		if deliberatelyDifferent[candidate.target.Error()] {
			assert.NotEqual(t, candidate.target.Error(), candidate.presentation().message,
				"this sentinel is listed as deliberately reworded, but no longer is")

			continue
		}

		assert.Equal(t, candidate.target.Error(), candidate.presentation().message,
			"presented message has drifted from its sentinel")
	}
}

// classifyError must not claim an error it was never given.
func TestClassifyErrorIgnoresUnknownErrors(t *testing.T) {
	t.Parallel()

	_, ok := classifyError(errors.New("something else entirely"))
	assert.False(t, ok)
}
