package moderation_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/stretchr/testify/require"
)

func TestBanImportValidatesWholeFileBeforeAnyChange(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("testdata/ban-file-v1.json")
	require.NoError(t, err)

	for label, invalid := range map[string]string{
		"unsupported version": strings.Replace(string(data), `"version": 1`, `"version": 2`, 1),
		"unsupported format":  strings.Replace(string(data), "sha256-btih-v1", "sha256-btih-v2", 1),
		"unsupported origin":  strings.Replace(string(data), "automatic", "import", 1),
		"bad second digest":   strings.Replace(string(data), "9afaa458", "Zafaa458", 1),
		"truncated":           string(data[:len(data)-4]),
		"trailing document":   string(data) + `{}`,
		"unknown private field": strings.Replace(
			string(data), `"origin": "automatic"`, `"origin": "automatic", "name": "private"`, 1,
		),
		"duplicate property": strings.Replace(string(data), `"version": 1`, `"version": 1, "version": 1`, 1),
		"null entries":       `{"version":1,"identifierFormat":"sha256-btih-v1","entries":null}`,
		"missing entries":    `{"version":1,"identifierFormat":"sha256-btih-v1"}`,
		"null source":        strings.Replace(string(data), `"Harmless compatibility fixture"`, "null", 1),
		"uppercase key":      strings.Replace(string(data), `"version"`, `"Version"`, 1),
	} {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			db := dbtest.New(t)
			service := moderation.NewService(db.Gorm)
			_, err := service.Import(t.Context(), strings.NewReader(invalid))
			require.Error(t, err, "invalid entire file must be rejected")
			page, err := service.Search(t.Context(), moderation.SearchRequest{})
			require.NoError(t, err)
			require.Empty(t, page.Items, "no prefix of an invalid file may be applied")
		})
	}
}

func TestBanImportFixtureIsAdditiveAndLocalAllowIsFinal(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	service := moderation.NewService(db.Gorm)
	data, err := os.ReadFile("testdata/ban-file-v1.json")
	require.NoError(t, err)

	zero := moderation.IdentifierForInfoHash(protocol.ID{})

	var ones protocol.ID
	for i := range ones {
		ones[i] = 255
	}

	one := moderation.IdentifierForInfoHash(ones)

	require.NoError(t, service.Allow(t.Context(), zero, "local-reviewer"))

	for range 2 {
		_, err := service.Import(t.Context(), bytes.NewReader(data))
		require.NoError(t, err)
	}

	allowed, err := service.Get(t.Context(), zero)
	require.NoError(t, err)
	require.Equal(t, moderation.Allowed, allowed.Status)
	require.Equal(t, moderation.Moderator, allowed.Origin)
	banned, err := service.Get(t.Context(), one)
	require.NoError(t, err)
	require.Equal(t, moderation.Banned, banned.Status)
	require.Equal(t, moderation.Imported, banned.Origin)
	require.Equal(t, moderation.Automatic, banned.ImportedOrigin)
	require.Nil(t, banned.Evidence)
	history, err := service.History(t.Context(), one, moderation.Page{})
	require.NoError(t, err)
	require.Len(t, history.Items, 1)

	smaller, err := json.Marshal(
		moderation.BanFile{
			Version:          1,
			IdentifierFormat: moderation.IdentifierFormatV1,
			Entries:          []moderation.BanEntry{{Digest: zero.String(), Origin: moderation.Moderator}},
		},
	)
	require.NoError(t, err)
	_, err = service.Import(t.Context(), bytes.NewReader(smaller))
	require.NoError(t, err)
	still, err := service.Get(t.Context(), one)
	require.NoError(t, err)
	require.Equal(t, banned, still)
	available, err := service.Discoverable(t.Context(), []protocol.ID{{}, ones})
	require.NoError(t, err)
	require.Equal(t, []protocol.ID{{}}, available)

	var exported bytes.Buffer
	require.NoError(t, service.Export(t.Context(), &exported, moderation.ExportOptions{IncludeAutomatic: true}))
	require.JSONEq(t, `{"version":1,"identifierFormat":"sha256-btih-v1","entries":[]}`, exported.String())
}

func TestBanImportRollsBackEveryEntryWhenHistoryWriteFails(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	service := moderation.NewService(db.Gorm)
	data, err := os.ReadFile("testdata/ban-file-v1.json")
	require.NoError(t, err)
	_, err = db.Pool.Exec(
		t.Context(),
		`CREATE FUNCTION reject_second_import() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN
 IF NEW.identifier = '9afaa45890d2acf0f8760eb8c7bcb8f55193d8eadf816f1165a02410b68912be' THEN
 RAISE EXCEPTION 'synthetic history failure';
 END IF;
 RETURN NEW;
 END $$;
 CREATE TRIGGER reject_second_import BEFORE INSERT ON moderation_history
 FOR EACH ROW EXECUTE FUNCTION reject_second_import();`,
	)
	require.NoError(t, err)
	_, err = service.Import(t.Context(), bytes.NewReader(data))
	require.Error(t, err)
	page, err := service.Search(t.Context(), moderation.SearchRequest{})
	require.NoError(t, err)
	require.Empty(t, page.Items, "earlier entries roll back with a later failed write")
}

func TestBanImportBoundsBeforeOpeningDatabase(t *testing.T) {
	t.Parallel()

	service := moderation.NewService(nil)
	oversized := strings.Repeat(" ", moderation.MaxBanFileBytes+1)
	_, err := service.Import(t.Context(), strings.NewReader(oversized))
	require.ErrorIs(t, err, moderation.ErrBanFileTooLarge)

	entry := `{"digest":"20fed20512528a3e12b7bcda59b0c84eff6bbe33007773edc0ecc785d2c450b6","origin":"moderator"}`
	many := `{"version":1,"identifierFormat":"sha256-btih-v1","entries":[` + strings.Repeat(
		entry+",",
		moderation.MaxBanFileEntries,
	) + entry + `]}`
	_, err = service.Import(t.Context(), strings.NewReader(many))
	require.ErrorIs(t, err, moderation.ErrBanFileTooLarge)

	conflict := `{"version":1,"identifierFormat":"sha256-btih-v1","entries":[` + entry + "," + strings.Replace(
		entry,
		"moderator",
		"automatic",
		1,
	) + `]}`
	_, err = service.Import(t.Context(), strings.NewReader(conflict))
	require.ErrorIs(t, err, moderation.ErrInvalidBanFile)
}

func TestBanImportRejectsSourceAmplificationBeforeAnyChange(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	service := moderation.NewService(db.Gorm)
	file := moderation.BanFile{
		Version:          1,
		IdentifierFormat: moderation.IdentifierFormatV1,
		Source:           strings.Repeat("s", 257),
		Entries: []moderation.BanEntry{
			{
				Digest: "20fed20512528a3e12b7bcda59b0c84eff6bbe33007773edc0ecc785d2c450b6",
				Origin: moderation.Moderator,
			},
		},
	}
	data, err := json.Marshal(file)
	require.NoError(t, err)
	_, err = service.Import(t.Context(), bytes.NewReader(data))
	require.ErrorIs(
		t,
		err,
		moderation.ErrBanFileTooLarge,
		"a public source label must not multiply a bounded file into unbounded ledger writes",
	)
	page, err := service.Search(t.Context(), moderation.SearchRequest{})
	require.NoError(t, err)
	require.Empty(t, page.Items)
}
