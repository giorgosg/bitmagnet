package resolvers

import (
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/gql/gqlmodel"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/stretchr/testify/require"
)

func TestOrdinaryDeleteDoesNotCreateHarmfulBan(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	hash := protocol.ID{89}
	require.NoError(
		t,
		db.Query.Torrent.WithContext(t.Context()).
			Create(&model.Torrent{
				InfoHash:    hash,
				Name:        "Synthetic handbook",
				Size:        1024,
				FilesStatus: model.FilesStatusSingle,
			}),
	)
	r := Resolver{Dao: db.Query}
	_, err := r.TorrentMutation().Delete(t.Context(), &gqlmodel.TorrentMutation{}, []protocol.ID{hash})
	require.NoError(t, err)
	count, err := db.Query.Torrent.WithContext(t.Context()).Where(db.Query.Torrent.InfoHash.Eq(hash)).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	_, err = moderation.NewService(db.Gorm).Get(t.Context(), moderation.IdentifierForInfoHash(hash))
	require.ErrorIs(t, err, moderation.ErrNotFound)

	var legacyCount int64
	require.NoError(
		t,
		db.Gorm.Table("bloom_filters").Where("key = ?", "blocked_torrents").Count(&legacyCount).Error,
	)
	require.Zero(t, legacyCount, "catalogue deletion must not persist a legacy block")
}
