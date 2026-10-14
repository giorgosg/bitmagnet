package importer

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/stretchr/testify/require"
)

func TestImportCannotResurrectDigestOnlyBan(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	hash := protocol.ID{81}
	ledger := moderation.NewService(db.Gorm)
	data, err := json.Marshal(
		moderation.BanFile{
			Version:          1,
			IdentifierFormat: moderation.IdentifierFormatV1,
			Entries: []moderation.BanEntry{
				{Digest: moderation.IdentifierForInfoHash(hash).String(), Origin: moderation.Moderator},
			},
		},
	)
	require.NoError(t, err)
	_, err = ledger.Import(t.Context(), bytes.NewReader(data))
	require.NoError(t, err)
	imp := importer{dao: db.Query, bufferSize: 1, maxWaitTime: time.Hour}.New(t.Context(), Info{})
	require.NoError(t, imp.Import(Item{Source: "synthetic", InfoHash: hash, Name: "Synthetic handbook"}))
	imp.Drain()
	require.NoError(t, imp.Close())
	count, err := db.Query.Torrent.WithContext(t.Context()).Where(db.Query.Torrent.InfoHash.Eq(hash)).Count()
	require.NoError(t, err)
	require.Zero(t, count, "exact digest-only bans must stop catalogue writes")
	record, err := ledger.Get(t.Context(), moderation.IdentifierForInfoHash(hash))
	require.NoError(t, err)
	require.NotNil(t, record.Evidence)
	require.Equal(t, "Synthetic handbook", record.Evidence.Name)
}
