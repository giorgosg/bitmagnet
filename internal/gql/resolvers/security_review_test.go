package resolvers_test

import (
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/stretchr/testify/assert"
)

// A revoked JWT is still syntactically valid. It must be rejected as a user
// credential while allowing the request to continue as anonymous; otherwise
// the UI cannot poll self.identity, clear the token, or reach login after a
// reload without the operator manually editing browser storage.
func TestDisabledSessionFallsBackToAnonymousIdentity(t *testing.T) {
	t.Parallel()

	cfg := authconfig.NewDefaultConfig()
	cfg.AnonymousAccess = false

	server, code := newAuthTestServerWithConfig(t, cfg)
	token := loginAsAdmin(t, server, code)

	disabled := query(t, server, token, `mutation {
		auth { setUserEnabled(userId: 1, enabled: false) { id } }
	}`)
	requireNoGqlErrors(t, disabled)

	recovered := query(t, server, token, `{ self { identity { user { username } } } }`)
	requireNoGqlErrors(t, recovered)
	assert.JSONEq(t, `{"self":{"identity":{"user":null}}}`, string(recovered.Data))
}
