package resolvers_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/dev/fixtureserver"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func moderatorServer(t *testing.T) (server *httptest.Server, db *dbtest.DB, admin, moderator string) {
	t.Helper()
	db = dbtest.New(t)
	stack, err := fixtureserver.Build(
		t.Context(),
		fixtureserver.Options{
			Config:              authconfig.NewDefaultConfig(),
			Provider:            daoProvider{db.Query},
			JWTSecret:           "moderator-test",
			PasswordHashingCost: bcrypt.MinCost,
		},
	)
	require.NoError(t, err)
	invitation, err := stack.UserService.CreateInitialInvitation(t.Context())
	require.NoError(t, err)

	server = httptest.NewServer(stack.Engine)
	t.Cleanup(server.Close)
	admin = loginAsAdmin(t, server, invitation.Code)
	requireNoGqlErrors(
		t,
		query(
			t,
			server,
			admin,
			`mutation {
			  auth {
			    putRole(role:"moderator",objectActions:[{
			      namespace:"graphql",object:"moderation",action:"query"
			    },{
			      namespace:"graphql",object:"moderation",action:"mutate"
			    }]){
			      name
			    }
			  }
			}`,
		),
	)
	moderator = registerAndLoginAsRole(t, server, admin, "moderator", "reviewer")

	return server, db, admin, moderator
}

func TestModeratorPrivateReads(t *testing.T) {
	t.Parallel()
	server, db, admin, moderator := moderatorServer(t)
	hashes := dbtest.ModerationCatalogue(t, db)
	id := moderation.IdentifierForInfoHash(hashes[1]).String()
	request := fmt.Sprintf(
		`{
		  moderation {
		    record(identifier:%q){
		      identifier status evidence{
		        name files infoHash
		      } history(input:{
		        limit:1
		      }){
		        items{
		          action actor
		        } next
		      }
		    } search(input:{
		      status:pending,limit:1
		    }){
		      items{
		        identifier name
		      } next
		    }
		  }
		}`,
		id,
	)
	response := query(t, server, moderator, request)
	requireNoGqlErrors(t, response)
	require.Contains(t, string(response.Data), "Pending/harmless.txt")
	require.Contains(t, string(response.Data), `"action":"flag"`)

	for _, role := range []string{"user", "editor"} {
		token := registerAndLoginAsRole(t, server, admin, role, "ordinary-"+role)
		denied := query(t, server, token, request)
		gqlErr := requireGraphQLErrorCode(t, denied, "UNAUTHORIZED")
		require.Equal(t, "moderation", gqlErr.Extensions["object"])
		require.NotContains(t, string(denied.Data), "harmless.txt")
	}
}

func TestModeratorAllowsAndReopens(t *testing.T) {
	t.Parallel()
	server, db, admin, moderator := moderatorServer(t)
	hashes := dbtest.ModerationCatalogue(t, db)
	// Quarantine prevented normal classification: remove that synthetic pre-seeded row.
	_, err := db.Pool.Exec(t.Context(), `DELETE FROM torrent_contents WHERE info_hash=$1`, hashes[1][:])
	require.NoError(t, err)

	id := moderation.IdentifierForInfoHash(hashes[1])
	record, err := moderation.NewService(db.Gorm).Get(t.Context(), id)
	require.NoError(t, err)

	mutation := fmt.Sprintf(
		`mutation{
		  moderation{
		    allow(input:{
		      identifier:%q,expectedRevision:%q
		    }){
		      record{
		        status updatedAt
		      } classificationQueued
		    }
		  }
		}`,
		id.String(),
		record.UpdatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
	)

	for _, role := range []string{"user", "editor"} {
		token := registerAndLoginAsRole(t, server, admin, role, "write-"+role)
		requireGraphQLErrorCode(t, query(t, server, token, mutation), "UNAUTHORIZED")
	}

	allowed := query(t, server, moderator, mutation)
	requireNoGqlErrors(t, allowed)
	require.Contains(t, string(allowed.Data), `"status":"allowed"`)
	require.Contains(t, string(allowed.Data), `"classificationQueued":true`)
	visible := query(
		t,
		server,
		admin,
		fmt.Sprintf(
			`{
			  torrent{
			    files(input:{
			      infoHashes:[%q]
			    }){
			      items{
			        path
			      }
			    }
			  }queue{
			    jobs(input:{
			      queues:["process_torrent"],limit:10
			    }){
			      items{
			        queue payload maxRetries
			      }
			    }
			  }
			}`,
			hashes[1].String(),
		),
	)
	requireNoGqlErrors(t, visible)
	require.Contains(t, string(visible.Data), "Pending/harmless.txt")
	require.Contains(t, string(visible.Data), "process_torrent")
	require.Contains(t, string(visible.Data), hashes[1].String())
	requireGraphQLErrorCode(t, query(t, server, moderator, mutation), "MODERATION_CONFLICT")
	record, err = moderation.NewService(db.Gorm).Get(t.Context(), id)
	require.NoError(t, err)
	reopened := query(
		t,
		server,
		moderator,
		fmt.Sprintf(
			`mutation{
			  moderation{
			    reopen(input:{
			      identifier:%q,expectedRevision:%q
			    }){
			      status history(input:{
			      }){
			        items{
			          action actor
			        }
			      }
			    }
			  }
			}`,
			id.String(),
			record.UpdatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		),
	)
	requireNoGqlErrors(t, reopened)
	require.Contains(t, string(reopened.Data), `"status":"pending"`)
	require.Contains(t, string(reopened.Data), `"actor":"user:`)
}

func TestFixtureCatalogueGrantDoesNotExposeModeration(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	hashes := dbtest.ModerationCatalogue(t, db)
	stack, err := fixtureserver.Build(
		t.Context(),
		fixtureserver.Options{
			Config:                    authconfig.NewDefaultConfig(),
			Provider:                  daoProvider{db.Query},
			GrantAnonymousReadSurface: true,
		},
	)
	require.NoError(t, err)

	server := httptest.NewServer(stack.Engine)
	t.Cleanup(server.Close)
	response := query(
		t,
		server,
		"",
		fmt.Sprintf(
			`{moderation{record(identifier:%q){evidence{name files}}}}`,
			moderation.IdentifierForInfoHash(hashes[1]).String(),
		),
	)
	requireGraphQLErrorCode(t, response, "AUTHENTICATION_REQUIRED")
	require.NotContains(t, string(response.Data), "harmless.txt")
}

func TestModeratorFlagBanPurgeAndDigestOnly(t *testing.T) {
	t.Parallel()
	server, db, admin, moderator := moderatorServer(t)
	hashes := dbtest.ModerationCatalogue(t, db)
	flagged := query(
		t,
		server,
		moderator,
		fmt.Sprintf(
			`mutation{moderation{flag(infoHash:%q){identifier status evidence{name infoHash}}}}`,
			hashes[0].String(),
		),
	)
	requireNoGqlErrors(t, flagged)
	require.Contains(t, string(flagged.Data), `"name":"Visible"`)
	require.Contains(t, string(flagged.Data), hashes[0].String())
	requireGraphQLErrorCode(
		t,
		query(
			t,
			server,
			moderator,
			fmt.Sprintf(`mutation{moderation{flag(infoHash:%q){status}}}`, hashes[0].String()),
		),
		"MODERATION_INVALID_TRANSITION",
	)

	service := moderation.NewService(db.Gorm)
	id := moderation.IdentifierForInfoHash(hashes[0])
	record, err := service.Get(t.Context(), id)
	require.NoError(t, err)
	banned := query(
		t,
		server,
		moderator,
		fmt.Sprintf(
			`mutation{moderation{ban(input:{identifier:%q,expectedRevision:%q}){status origin}}}`,
			id.String(),
			record.UpdatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		),
	)
	requireNoGqlErrors(t, banned)
	require.Contains(t, string(banned.Data), `"origin":"moderator"`)
	record, err = service.Get(t.Context(), id)
	require.NoError(t, err)
	purged := query(
		t,
		server,
		moderator,
		fmt.Sprintf(
			`mutation{
			  moderation{
			    purgeEvidence(input:{
			      identifier:%q,expectedRevision:%q
			    }){
			      status evidence{
			        name
			      } history(input:{
			      }){
			        items{
			          action
			        }
			      }
			    }
			  }
			}`,
			id.String(),
			record.UpdatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		),
	)
	requireNoGqlErrors(t, purged)
	require.JSONEq(
		t,
		`{
		  "moderation":{
		    "purgeEvidence":{
		      "status":"banned","evidence":null,"history":{
		        "items":[{
		          "action":"purge"
		        }]
		      }
		    }
		  }
		}`,
		string(purged.Data),
	)
	ordinary := query(
		t,
		server,
		admin,
		fmt.Sprintf(`{torrent{files(input:{infoHashes:[%q]}){items{path}}}}`, hashes[0].String()),
	)
	requireNoGqlErrors(t, ordinary)
	require.NotContains(t, string(ordinary.Data), "harmless")

	imported := moderation.IdentifierForInfoHash([20]byte{99})
	require.NoError(t, service.ApplyImport(t.Context(), imported, moderation.Automatic, "synthetic-source"))
	read := query(
		t,
		server,
		moderator,
		fmt.Sprintf(
			`{moderation{record(identifier:%q){status origin importedOrigin source evidence{name}}}}`,
			imported.String(),
		),
	)
	requireNoGqlErrors(t, read)
	require.JSONEq(
		t,
		`{
		  "moderation":{
		    "record":{
		      "status":"banned","origin":"import","importedOrigin":"automatic",
		      "source":"synthetic-source","evidence":null
		    }
		  }
		}`,
		string(read.Data),
	)
	record, err = service.Get(t.Context(), imported)
	require.NoError(t, err)
	allowed := query(
		t,
		server,
		moderator,
		fmt.Sprintf(
			`mutation{
			  moderation{
			    allow(input:{
			      identifier:%q,expectedRevision:%q
			    }){
			      record{
			        status evidence{
			          name
			        }
			      } classificationQueued
			    }
			  }
			}`,
			imported.String(),
			record.UpdatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		),
	)
	requireNoGqlErrors(t, allowed)
	require.Contains(t, string(allowed.Data), `"status":"allowed"`)
	require.Contains(t, string(allowed.Data), `"classificationQueued":false`)
}

func TestModeratorStatusPagingAndAllowedSearch(t *testing.T) {
	t.Parallel()
	server, db, _, moderator := moderatorServer(t)
	hashes := dbtest.ModerationCatalogue(t, db)
	require.NoError(
		t,
		moderation.NewService(db.Gorm).
			Flag(
				t.Context(),
				moderation.IdentifierForInfoHash(hashes[0]),
				moderation.Evidence{InfoHash: &hashes[0], Name: "Harmless second review"},
				"fixture",
			),
	)
	first := query(
		t,
		server,
		moderator,
		`{moderation{search(input:{status:pending,limit:1}){items{identifier status} next}}}`,
	)
	requireNoGqlErrors(t, first)

	var decoded moderationSearchResponse
	require.NoError(t, json.Unmarshal(first.Data, &decoded))
	require.Len(t, decoded.Moderation.Search.Items, 1)
	require.NotNil(t, decoded.Moderation.Search.Next)
	second := query(
		t,
		server,
		moderator,
		fmt.Sprintf(
			`{moderation{search(input:{status:pending,limit:1,after:%q}){items{identifier status} next}}}`,
			*decoded.Moderation.Search.Next,
		),
	)
	requireNoGqlErrors(t, second)

	var next moderationSearchResponse
	require.NoError(t, json.Unmarshal(second.Data, &next))
	require.Len(t, next.Moderation.Search.Items, 1)
	require.Nil(t, next.Moderation.Search.Next)
	require.NotEqual(t, decoded.Moderation.Search.Items[0].Identifier, next.Moderation.Search.Items[0].Identifier)

	allowedID := moderation.IdentifierForInfoHash(hashes[2]).String()
	found := query(
		t,
		server,
		moderator,
		fmt.Sprintf(
			`{moderation{search(input:{status:allowed,query:%q}){items{identifier status} next}}}`,
			allowedID[:12],
		),
	)
	requireNoGqlErrors(t, found)
	require.Contains(t, string(found.Data), allowedID)

	for _, input := range []string{`{limit:101}`, `{limit:-1}`, `{after:"invalid"}`} {
		requireGraphQLErrorCode(
			t,
			query(t, server, moderator, `{moderation{search(input:`+input+`){items{identifier}}}}`),
			"MODERATION_INVALID_PAGE",
		)
	}

	requireGraphQLErrorCode(
		t,
		query(t, server, moderator, `{moderation{record(identifier:"invalid"){status}}}`),
		"MODERATION_INVALID_IDENTIFIER",
	)
}

type moderationSearchResponse struct {
	Moderation moderationSearchBody `json:"moderation"`
}
type moderationSearchBody struct {
	Search moderationSearchPage `json:"search"`
}
type moderationSearchPage struct {
	Items []moderationSearchItem `json:"items"`
	Next  *string                `json:"next"`
}
type moderationSearchItem struct {
	Identifier string `json:"identifier"`
	Status     string `json:"status"`
	Revision   string `json:"revision"`
}

func TestModeratorSearchRevisionCanBeUsedUnchanged(t *testing.T) {
	t.Parallel()
	server, db, _, moderator := moderatorServer(t)
	dbtest.ModerationCatalogue(t, db)
	response := query(
		t,
		server,
		moderator,
		`{moderation{search(input:{status:pending,limit:1}){items{identifier revision}}}}`,
	)
	requireNoGqlErrors(t, response)

	var decoded moderationSearchResponse
	require.NoError(t, json.Unmarshal(response.Data, &decoded))
	require.Len(t, decoded.Moderation.Search.Items, 1)
	item := decoded.Moderation.Search.Items[0]
	require.NotEmpty(t, item.Revision)
	allowed := query(
		t,
		server,
		moderator,
		fmt.Sprintf(
			`mutation{
			  moderation{
			    allow(input:{
			      identifier:%q,expectedRevision:%q
			    }){
			      record{
			        status revision
			      } classificationQueued
			    }
			  }
			}`,
			item.Identifier,
			item.Revision,
		),
	)
	requireNoGqlErrors(t, allowed)
	require.Contains(t, string(allowed.Data), `"status":"allowed"`)
}

func TestModeratorAllowPurgedMetadataUsesValidatedHash(t *testing.T) {
	t.Parallel()
	server, db, _, moderator := moderatorServer(t)
	hashes := dbtest.ModerationCatalogue(t, db)
	service := moderation.NewService(db.Gorm)
	id := moderation.IdentifierForInfoHash(hashes[1])
	_, err := db.Pool.Exec(t.Context(), `DELETE FROM torrent_contents WHERE info_hash=$1`, hashes[1][:])
	require.NoError(t, err)
	require.NoError(t, service.PurgeEvidence(t.Context(), id))
	record, err := service.Get(t.Context(), id)
	require.NoError(t, err)

	input := fmt.Sprintf(
		`identifier:%q,expectedRevision:%q`,
		id.String(),
		record.UpdatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
	)
	invalid := query(
		t,
		server,
		moderator,
		fmt.Sprintf(
			`mutation{moderation{allow(input:{%s,infoHash:%q}){classificationQueued}}}`,
			input,
			hashes[0].String(),
		),
	)
	requireGraphQLErrorCode(t, invalid, "MODERATION_INVALID_INFOHASH")
	stillPending := query(
		t,
		server,
		moderator,
		fmt.Sprintf(`{moderation{record(identifier:%q){status}}}`, id.String()),
	)
	requireNoGqlErrors(t, stillPending)
	require.Contains(t, string(stillPending.Data), `"status":"pending"`)
	allowed := query(
		t,
		server,
		moderator,
		fmt.Sprintf(
			`mutation{
			  moderation{
			    allow(input:{
			      %s,infoHash:%q
			    }){
			      record{
			        status evidence{
			          name
			        }
			      } classificationQueued
			    }
			  }
			}`,
			input,
			hashes[1].String(),
		),
	)
	requireNoGqlErrors(t, allowed)
	require.JSONEq(
		t,
		`{"moderation":{"allow":{"record":{"status":"allowed","evidence":null},"classificationQueued":true}}}`,
		string(allowed.Data),
	)
}
