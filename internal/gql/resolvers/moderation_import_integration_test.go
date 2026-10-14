package resolvers_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/dev/fixtureserver"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestModerationBanUpload(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	hash := protocol.ID{}
	require.NoError(
		t,
		db.Gorm.Create(
			&model.Torrent{
				InfoHash:    hash,
				Name:        "Harmless upload catalogue",
				Size:        42,
				FilesStatus: model.FilesStatusSingle,
			},
		).Error,
	)
	require.NoError(
		t,
		db.Gorm.Create(
			&model.TorrentContent{
				InfoHash:    hash,
				ContentType: model.NewNullContentType(model.ContentTypeEbook),
			},
		).Error,
	)
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
	data, err := os.ReadFile("../../moderation/testdata/ban-file-v1.json")
	require.NoError(t, err)

	upload := func(credential string, data []byte) int {
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/graphql/moderation/import",
			bytes.NewReader(data),
		)
		req.Header.Set("Content-Type", "application/json")

		if credential != "" {
			req.Header.Set("Authorization", "Bearer "+credential)
		}

		response := httptest.NewRecorder()
		stack.Engine.ServeHTTP(response, req)

		return response.Code
	}
	read := func() []byte {
		response := query(
			t,
			server,
			"",
			fmt.Sprintf(
				`{torrentContent{search(input:{infoHashes:[%q],cached:true}){items{infoHash torrent{name}}}}}`,
				hash.String(),
			),
		)
		requireNoGqlErrors(t, response)

		return response.Data
	}
	before := read()
	require.Contains(t, string(before), "Harmless upload catalogue")
	require.Equal(t, http.StatusOK, upload(token, data), "a permissioned file upload must apply the frozen fixture")

	service := moderation.NewService(db.Gorm)
	record, err := service.Get(t.Context(), moderation.IdentifierForInfoHash(protocol.ID{}))
	require.NoError(t, err)

	after := read()
	require.JSONEq(t, `{"torrentContent":{"search":{"items":[]}}}`, string(after))
	require.NotNil(t, record.Evidence)
	require.Equal(t, "Harmless upload catalogue", record.Evidence.Name)
	require.Equal(t, moderation.Banned, record.Status)
	require.Equal(t, moderation.Imported, record.Origin)
	require.Equal(t, moderation.Moderator, record.ImportedOrigin)
	require.Equal(t, "Harmless compatibility fixture", record.Source)
	require.Equal(t, http.StatusOK, upload(token, data))
	history, err := service.History(t.Context(), record.Identifier, moderation.Page{})
	require.NoError(t, err)
	require.Len(t, history.Items, 1)
	require.Equal(t, http.StatusForbidden, upload("", data))
	reader := registerAndLoginAsRole(t, server, token, "user", "import-reader")
	require.Equal(t, http.StatusForbidden, upload(reader, data))
}

func TestModerationBanUploadBrowserMutationBoundary(t *testing.T) {
	t.Parallel()
	server, code := newAuthTestServer(t)
	token := loginAsAdmin(t, server, code)
	cookie := loginBrowserCookie(t, server, "admin", "correct-horse-battery-staple-99")
	empty := `{"version":1,"identifierFormat":"sha256-btih-v1","entries":[]}`

	for _, tc := range []struct {
		name, origin, bearer string
		want                 int
	}{
		{"same origin browser", "https://" + server.Listener.Addr().String(), "", http.StatusOK},
		{"missing origin browser", "", "", http.StatusForbidden},
		{"cross origin browser", "https://another.invalid", "", http.StatusForbidden},
		{"non https origin", "http://" + server.Listener.Addr().String(), "", http.StatusForbidden},
		{
			"invalid bearer wins", "https://" + server.Listener.Addr().String(), "invalid", http.StatusForbidden,
		},
		{"explicit bearer needs no browser origin", "", token, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req, err := http.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				server.URL+"/graphql/moderation/import",
				strings.NewReader(empty),
			)
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")

			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}

			if tc.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tc.bearer)
			}

			req.AddCookie(cookie)
			response, err := server.Client().Do(req)
			require.NoError(t, err)

			defer response.Body.Close()

			require.Equal(t, tc.want, response.StatusCode)
		})
	}
}

func TestModerationBanUploadRequiresSelectedImportPermissionAndBounds(t *testing.T) {
	t.Parallel()
	server, code := newAuthTestServer(t)
	token := loginAsAdmin(t, server, code)
	file := `{"version":1,"identifierFormat":"sha256-btih-v1","entries":[]}`
	upload := func(credential, contentType, body string) int {
		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			server.URL+"/graphql/moderation/import",
			strings.NewReader(body),
		)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+credential)
		req.Header.Set("Content-Type", contentType)
		response, err := server.Client().Do(req)
		require.NoError(t, err)

		defer response.Body.Close()

		return response.StatusCode
	}

	for _, action := range []string{"export", "import"} {
		response := query(
			t,
			server,
			token,
			fmt.Sprintf(
				`mutation{self{createAPIKey(input:{name:"synthetic ban sharing",
 permissions:[{namespace:"http",object:"moderation",action:%q}]}){apiKey}}}`,
				action,
			),
		)
		requireNoGqlErrors(t, response)

		var created createAPIKeyResponse
		require.NoError(t, json.Unmarshal(response.Data, &created))

		expected := http.StatusForbidden
		if action == "import" {
			expected = http.StatusOK
		}

		require.Equal(t, expected, upload(created.Self.CreateAPIKey.APIKey, "application/json", file))
	}

	require.Equal(
		t,
		http.StatusRequestEntityTooLarge,
		upload(token, "application/json", strings.Repeat(" ", moderation.MaxBanFileBytes+1)),
	)
	require.Equal(t, http.StatusUnsupportedMediaType, upload(token, "text/plain", file))
	require.Equal(
		t,
		http.StatusBadRequest,
		upload(token, "application/json", `{"version":2,"identifierFormat":"sha256-btih-v1","entries":[]}`),
	)
}
