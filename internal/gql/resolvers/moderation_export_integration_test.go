package resolvers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/dev/fixtureserver"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestModerationBanDownload(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	service := moderation.NewService(db.Gorm)
	expected := map[string]string{}

	for i := range 105 {
		hash := protocol.ID{0xee, byte(i)}
		id := moderation.IdentifierForInfoHash(hash)
		require.NoError(t, service.Flag(t.Context(), id, moderation.Evidence{
			InfoHash: &hash,
			Name:     "Harmless private name",
			Files:    []string{"private/harmless.txt"},
			Rule:     "private-rule",
			Matches:  []string{"private-match"},
		}, "private-reviewer"))
		require.NoError(t, service.Ban(t.Context(), id, "private-reviewer"))
		expected[id.String()] = "moderator"
	}

	auto := moderation.IdentifierForInfoHash(protocol.ID{0xef, 1})
	require.NoError(
		t,
		service.ApplyAutomatic(
			t.Context(),
			auto,
			moderation.Banned,
			moderation.Evidence{Name: "Automatic private name"},
		),
	)

	imported := moderation.IdentifierForInfoHash(protocol.ID{0xef, 2})
	require.NoError(t, service.ApplyImport(t.Context(), imported, moderation.Moderator, "private-import-source"))

	allowed := moderation.IdentifierForInfoHash(protocol.ID{0xef, 3})
	require.NoError(t, service.Allow(t.Context(), allowed, "private-reviewer"))

	pending := moderation.IdentifierForInfoHash(protocol.ID{0xef, 4})
	require.NoError(t, service.Flag(t.Context(), pending, moderation.Evidence{}, "private-reviewer"))
	stack, err := fixtureserver.Build(t.Context(), fixtureserver.Options{
		Config:                    authconfig.NewDefaultConfig(),
		Provider:                  daoProvider{db.Query},
		Logger:                    zap.NewNop().Sugar(),
		GrantAnonymousReadSurface: true,
	})
	require.NoError(t, err)
	invitation, err := stack.UserService.CreateInitialInvitation(t.Context())
	require.NoError(t, err)

	server := httptest.NewServer(stack.Engine)
	t.Cleanup(server.Close)
	token := loginAsAdmin(t, server, invitation.Code)
	download := func(token, suffix string) (int, http.Header, []byte) {
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			"/graphql/moderation/export"+suffix,
			nil,
		)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		response := httptest.NewRecorder()
		stack.Engine.ServeHTTP(response, req)

		return response.Code, response.Header(), response.Body.Bytes()
	}
	status, headers, body := download(token, "")
	require.Equal(t, http.StatusOK, status, string(body))
	require.Equal(t, "application/json", headers.Get("Content-Type"))
	require.Contains(t, headers.Get("Content-Disposition"), "attachment;")
	require.Equal(t, "no-store", headers.Get("Cache-Control"))

	assertEntries := func(body []byte, want map[string]string) {
		var file map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(body, &file))
		require.Len(t, file, 3)
		require.JSONEq(t, `1`, string(file["version"]))
		require.JSONEq(t, `"sha256-btih-v1"`, string(file["identifierFormat"]))

		var entries []map[string]string
		require.NoError(t, json.Unmarshal(file["entries"], &entries))

		actual := map[string]string{}
		last := ""

		for _, entry := range entries {
			require.Len(t, entry, 2)
			require.Greater(t, entry["digest"], last)
			last = entry["digest"]
			actual[entry["digest"]] = entry["origin"]
		}

		require.Equal(t, want, actual)

		privateFields := []string{
			"private",
			"magnet:",
			"infoHash",
			"name",
			"files",
			"actor",
			"evidence",
			"history",
			"reviewer",
			"source",
		}
		for _, private := range privateFields {
			require.NotContains(t, string(body), private)
		}
	}
	assertEntries(body, expected)
	expected[auto.String()] = "automatic"
	status, _, body = download(token, "?includeAutomatic=true")
	require.Equal(t, http.StatusOK, status)
	assertEntries(body, expected)
	status, _, body = download("", "")
	require.Equal(t, http.StatusForbidden, status, string(body))
	require.NotContains(t, string(body), "digest")
	reader := registerAndLoginAsRole(t, server, token, "user", "export-reader")
	status, _, body = download(reader, "")
	require.Equal(t, http.StatusForbidden, status, string(body))
	status, _, _ = download(token, "?includeAutomatic=anything")
	require.Equal(t, http.StatusBadRequest, status)
	// A machine identity must explicitly select export; catalogue access does not grant it.
	response := query(
		t,
		server,
		token,
		`mutation {self{createAPIKey(input:{name:"catalogue",
permissions:[{namespace:"graphql",object:"torrent",action:"query"}]}){apiKey}}}`,
	)
	requireNoGqlErrors(t, response)

	var created createAPIKeyResponse
	require.NoError(t, json.Unmarshal(response.Data, &created))
	status, _, _ = download(created.Self.CreateAPIKey.APIKey, "")
	require.Equal(t, http.StatusForbidden, status)
	requireNoGqlErrors(
		t,
		query(
			t,
			server,
			token,
			`mutation {auth{putRole(role:"exporter",
objectActions:[{namespace:"http",object:"moderation",action:"export"}]){name}}}`,
		),
	)
	exporter := registerAndLoginAsRole(t, server, token, "exporter", "export-only")
	selected := query(
		t,
		server,
		exporter,
		`mutation {self{createAPIKey(input:{name:"ban exporter",
permissions:[{namespace:"http",object:"moderation",action:"export"}]}){apiKey}}}`,
	)
	requireNoGqlErrors(t, selected)
	require.NoError(t, json.Unmarshal(selected.Data, &created))
	status, _, body = download(created.Self.CreateAPIKey.APIKey, "")
	require.Equal(t, http.StatusOK, status)
	delete(expected, auto.String())
	assertEntries(body, expected)
	// Moderation query alone does not export, including after narrowing a key's owner.
	requireNoGqlErrors(
		t,
		query(
			t,
			server,
			token,
			`mutation {auth{putRole(role:"exporter",
objectActions:[{namespace:"graphql",object:"moderation",action:"query"}]){name}}}`,
		),
	)

	status, _, _ = download(exporter, "")
	require.Equal(t, http.StatusForbidden, status)
	status, _, _ = download(created.Self.CreateAPIKey.APIKey, "")
	require.Equal(t, http.StatusForbidden, status)
}

func TestModerationBanDownloadWithBrowserCookie(t *testing.T) {
	t.Parallel()
	server, code := newAuthTestServer(t)
	_ = loginAsAdmin(t, server, code)
	cookie := loginBrowserCookie(t, server, "admin", "correct-horse-battery-staple-99")
	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		server.URL+"/graphql/moderation/export",
		nil,
	)
	require.NoError(t, err)
	req.AddCookie(cookie)
	response, err := server.Client().Do(req)
	require.NoError(t, err)

	defer response.Body.Close()

	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "no-store", response.Header.Get("Cache-Control"))
	req.Header.Set("Authorization", "Bearer invalid-explicit-credential")
	refused, err := server.Client().Do(req)
	require.NoError(t, err)

	defer refused.Body.Close()

	require.Equal(t, http.StatusForbidden, refused.StatusCode)
}
