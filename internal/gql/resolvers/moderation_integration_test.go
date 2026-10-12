package resolvers_test

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/dev/fixtureserver"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestGraphQLModerationVisibility(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	hashes := dbtest.ModerationCatalogue(t, db)
	stack, err := fixtureserver.Build(
		t.Context(),
		fixtureserver.Options{
			Config:                    authconfig.NewDefaultConfig(),
			Provider:                  daoProvider{db.Query},
			Logger:                    zap.NewNop().Sugar(),
			GrantAnonymousReadSurface: true,
		},
	)
	require.NoError(t, err)

	server := httptest.NewServer(stack.Engine)
	t.Cleanup(server.Close)
	// Request each exact hidden hash through ordinary GraphQL, including files and
	// hydrated torrent fields rather than trusting only the search result title.
	for _, hash := range hashes {
		response := query(
			t,
			server,
			"",
			fmt.Sprintf(
				`{torrentContent{search(input:{infoHashes:[%q],cached:true}){items{infoHash torrent{name}}}}
 torrent{files(input:{infoHashes:[%q],cached:true}){items{path}}}}`,
				hash.String(),
				hash.String(),
			),
		)
		requireNoGqlErrors(t, response)

		if hash == hashes[1] || hash == hashes[3] {
			require.JSONEq(
				t,
				`{"torrentContent":{"search":{"items":[]}},"torrent":{"files":{"items":[]}}}`,
				string(response.Data),
			)
		} else {
			require.Contains(t, string(response.Data), "harmless.txt")
		}
	}

	response := query(t, server, "", `{torrent{suggestTags{suggestions{name}} listSources{sources{name key}}}}`)
	requireNoGqlErrors(t, response)
	require.NotContains(t, string(response.Data), "pending")
	require.NotContains(t, string(response.Data), "banned")
	require.NotContains(t, string(response.Data), "Pending source")
	require.Contains(t, string(response.Data), "Visible source")
}
