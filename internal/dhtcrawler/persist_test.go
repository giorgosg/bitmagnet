package dhtcrawler

import (
	"testing"

	torrentmetainfo "github.com/anacrolix/torrent/metainfo"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol/metainfo"
	"github.com/stretchr/testify/require"
)

func TestCreateTorrentModelDoesNotCountPaddingFilesTowardThreshold(t *testing.T) {
	t.Parallel()

	torrent, err := createTorrentModel(protocol.ID{}, metainfo.Info{
		Name: "release",
		Files: []torrentmetainfo.FileInfo{
			{Path: []string{".pad", "1024"}, Length: 1024},
			{Path: []string{"movie.mkv"}, Length: 10_000},
			{Path: []string{"subs.srt"}, Length: 1_000},
		},
	}, false, 2)
	require.NoError(t, err)
	require.Equal(t, model.FilesStatusMulti, torrent.FilesStatus)
	require.Len(t, torrent.Files, 2)
	require.Equal(t, []uint{1, 2}, []uint{torrent.Files[0].Index, torrent.Files[1].Index})
}
