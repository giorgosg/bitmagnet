package httpserver_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/identity"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/torznab"
	"github.com/bitmagnet-io/bitmagnet/internal/torznab/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The case structure here follows the kawaii-not-kawaii fork's Torznab auth
// tests (172a784d3), the only prior art for this endpoint: accept the key by
// query parameter and by header, reject it when missing or wrong, and assert the
// Torznab XML error body rather than only the status code.
//
// Its trusted-network case is preserved in spirit by TestTorznabAuthNoNetworkBypass:
// that fork deliberately does not extend its LAN bypass to Torznab, and neither
// does this.

const testAPIKey = "machine-key"

// stubIdentity grants exactly the object actions it is given.
type stubIdentity struct {
	permissions []rbac.ObjectAction
}

func (stubIdentity) Self() identity.Self {
	return identity.Self{}
}

func (s stubIdentity) EffectivePermissions(context.Context) ([]rbac.ObjectAction, error) {
	return s.permissions, nil
}

func (s stubIdentity) Enforce(_ context.Context, objectAction rbac.ObjectAction) (bool, error) {
	for _, p := range s.permissions {
		if p == objectAction {
			return true, nil
		}
	}

	return false, nil
}

// stubAuthenticator resolves one known key, and nothing else. An empty token is
// the anonymous case, which is granted or denied per the test.
type stubAuthenticator struct {
	key            string
	keyPermissions []rbac.ObjectAction
	anonymous      []rbac.ObjectAction
}

func (s stubAuthenticator) Authenticate(_ context.Context, token string) (identity.Identity, bool, error) {
	if token == "" {
		return stubIdentity{permissions: s.anonymous}, true, nil
	}

	if token != s.key {
		return nil, false, nil
	}

	return stubIdentity{permissions: s.keyPermissions}, true, nil
}

func newAuthenticator(anonymous ...rbac.ObjectAction) identity.Authenticator {
	return stubAuthenticator{
		key:            testAPIKey,
		keyPermissions: []rbac.ObjectAction{httpserver.ObjectAction},
		anonymous:      anonymous,
	}
}

func unauthorizedXML(t *testing.T) string {
	t.Helper()

	body, err := (torznab.Error{Code: 100, Description: "Incorrect user credentials"}).XML()
	require.NoError(t, err)

	return string(body)
}

func requestCaps(t *testing.T, h *testHarness, path, header, remoteAddr string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	require.NoError(t, err)

	if header != "" {
		req.Header.Set("X-Api-Key", header)
	}

	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}

	h.engine.ServeHTTP(h.responseRecorder, req)
}

func TestTorznabAuthAcceptsQueryAndHeaderKeys(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		path   string
		header string
	}{
		{name: "query parameter", path: "/torznab/?t=caps&apikey=" + testAPIKey},
		{name: "header", path: "/torznab/?t=caps", header: testAPIKey},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			h := newTestHarnessWithAuth(t, newAuthenticator())
			requestCaps(t, h, testCase.path, testCase.header, "")

			assert.Equal(t, http.StatusOK, h.responseRecorder.Code)
			assert.Contains(t, h.responseRecorder.Body.String(), "<caps>")
		})
	}
}

func TestTorznabAuthRejectsMissingAndWrongKeys(t *testing.T) {
	t.Parallel()

	expected := unauthorizedXML(t)

	for _, testCase := range []struct {
		name   string
		path   string
		header string
	}{
		{name: "missing", path: "/torznab/?t=caps"},
		{name: "wrong query parameter", path: "/torznab/?t=caps&apikey=wrong"},
		{name: "wrong header", path: "/torznab/?t=caps", header: "wrong"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			// No anonymous permissions: authentication is required.
			h := newTestHarnessWithAuth(t, newAuthenticator())
			requestCaps(t, h, testCase.path, testCase.header, "")

			assert.Equal(t, http.StatusUnauthorized, h.responseRecorder.Code)
			assert.Equal(
				t,
				"application/xml; charset=utf-8",
				h.responseRecorder.Header().Get("Content-Type"),
			)
			assert.Equal(t, expected, h.responseRecorder.Body.String())
		})
	}
}

// Being on a private network is not a credential for machine access. The
// kawaii-not-kawaii fork makes the same call for the same reason.
func TestTorznabAuthNoNetworkBypass(t *testing.T) {
	t.Parallel()

	for _, remoteAddr := range []string{"10.1.2.3:1234", "127.0.0.1:1234", "192.168.1.5:1234"} {
		t.Run(remoteAddr, func(t *testing.T) {
			t.Parallel()

			h := newTestHarnessWithAuth(t, newAuthenticator())
			requestCaps(t, h, "/torznab/?t=caps", "", remoteAddr)

			assert.Equal(t, http.StatusUnauthorized, h.responseRecorder.Code)
		})
	}
}

// While anonymous access is enabled the anon role holds the Torznab object
// action, so existing unauthenticated clients keep working. This is the default,
// and the reason enabling the auth stack changes nothing on upgrade.
func TestTorznabAuthAnonymousAccessKeepsEndpointOpen(t *testing.T) {
	t.Parallel()

	h := newTestHarnessWithAuth(t, newAuthenticator(httpserver.ObjectAction))
	requestCaps(t, h, "/torznab/?t=caps", "", "")

	assert.Equal(t, http.StatusOK, h.responseRecorder.Code)
	assert.Contains(t, h.responseRecorder.Body.String(), "<caps>")
}

// A valid key that was not granted the Torznab permission must be refused. The
// fork this is modelled on cannot express this case; per-key scoping is what the
// permission model buys.
func TestTorznabAuthRejectsKeyWithoutPermission(t *testing.T) {
	t.Parallel()

	authenticator := stubAuthenticator{
		key:            testAPIKey,
		keyPermissions: []rbac.ObjectAction{rbac.NewObjectAction("other", "other", "query")},
	}

	h := newTestHarnessWithAuth(t, authenticator)
	requestCaps(t, h, "/torznab/?t=caps&apikey="+testAPIKey, "", "")

	assert.Equal(t, http.StatusUnauthorized, h.responseRecorder.Code)
	assert.Equal(t, unauthorizedXML(t), h.responseRecorder.Body.String())
}
