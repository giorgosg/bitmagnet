package moderation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/cache"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/stretchr/testify/require"
)

// The fixed harmless fixture is also the compatibility input for ban-file import.
func TestBanFileRoundTripFixture(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("testdata/ban-file-v1.json")
	require.NoError(t, err)

	var file moderation.BanFile
	require.NoError(t, json.Unmarshal(data, &file))
	require.Equal(t, moderation.BanFileVersion, file.Version)
	require.Equal(t, moderation.IdentifierFormatV1, file.IdentifierFormat)
	require.Equal(t, "Harmless compatibility fixture", file.Source)
	require.Equal(t, []moderation.BanEntry{
		{
			Digest: "20fed20512528a3e12b7bcda59b0c84eff6bbe33007773edc0ecc785d2c450b6",
			Origin: moderation.Moderator,
		},
		{
			Digest: "9afaa45890d2acf0f8760eb8c7bcb8f55193d8eadf816f1165a02410b68912be",
			Origin: moderation.Automatic,
		},
	}, file.Entries)
	encoded, err := json.Marshal(file)
	require.NoError(t, err)
	require.JSONEq(t, string(data), string(encoded))
}

type exportWriter struct {
	bytes.Buffer
	onWrite func()
}

func (w *exportWriter) Write(data []byte) (int, error) {
	if w.onWrite != nil {
		next := w.onWrite
		w.onWrite = nil

		next()
	}

	return w.Buffer.Write(data)
}

func TestBanExportKeepsOneSnapshotAcrossPagesAndIgnoresCache(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	service := moderation.NewService(db.Gorm)
	ids := make([]moderation.Identifier, 0, 102)

	for i := range 102 {
		id := moderation.IdentifierForInfoHash(protocol.ID{0xdd, byte(i)})
		ids = append(ids, id)
		require.NoError(t, service.Ban(t.Context(), id, "private-reviewer"))
	}

	w := &exportWriter{onWrite: func() {
		// Committed before the next page, but neither withdrawal nor addition may
		// splice a different decision snapshot into this file.
		for _, id := range ids {
			require.NoError(t, service.Allow(t.Context(), id, "private-reviewer"))
		}

		require.NoError(
			t,
			service.Ban(
				t.Context(),
				moderation.IdentifierForInfoHash(protocol.ID{0xdd, 0xff}),
				"private-reviewer",
			),
		)
	}}
	require.NoError(t, service.Export(t.Context(), w, moderation.ExportOptions{}))

	var file moderation.BanFile
	require.NoError(t, json.Unmarshal(w.Bytes(), &file))
	require.Len(t, file.Entries, 102)

	var next bytes.Buffer

	ctx := context.WithValue(t.Context(), cache.ModeKey, cache.ModeCached)
	require.NoError(t, service.Export(ctx, &next, moderation.ExportOptions{}))
	require.NoError(t, json.Unmarshal(next.Bytes(), &file))
	require.Equal(
		t,
		[]moderation.BanEntry{
			{
				Digest: moderation.IdentifierForInfoHash(protocol.ID{0xdd, 0xff}).String(),
				Origin: moderation.Moderator,
			},
		},
		file.Entries,
	)
}

type failedExportWriter struct{ err error }

func (w failedExportWriter) Write([]byte) (int, error) { return 0, w.err }
func TestBanExportReturnsWriteFailureAndEmptyFile(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	service := moderation.NewService(db.Gorm)

	var output bytes.Buffer
	require.NoError(t, service.Export(t.Context(), &output, moderation.ExportOptions{}))
	require.JSONEq(t, `{"version":1,"identifierFormat":"sha256-btih-v1","entries":[]}`, output.String())

	failure := errors.New("download disconnected")
	require.ErrorIs(
		t,
		service.Export(t.Context(), failedExportWriter{err: failure}, moderation.ExportOptions{}),
		failure,
	)
	require.ErrorIs(
		t,
		service.Export(t.Context(), failedExportWriter{}, moderation.ExportOptions{}),
		io.ErrShortWrite,
	)
}
