package dhtcrawler

import (
	"testing"

	mi "github.com/anacrolix/torrent/metainfo"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol/metainfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// paddedInfo builds a multi-file torrent with realFiles real entries and padFiles
// BEP-47 padding entries interleaved, each one byte long.
func paddedInfo(realFiles, padFiles int) metainfo.Info {
	info := metainfo.Info{
		Name:        "padded",
		PieceLength: 16384,
	}

	for i := range realFiles {
		info.Files = append(info.Files, mi.FileInfo{
			Path:   []string{"real", string(rune('a' + i%26)), "file"},
			Length: 1,
		})

		if i < padFiles {
			info.Files = append(info.Files, mi.FileInfo{
				Path:   []string{".pad", string(rune('a' + i%26))},
				Length: 1,
			})
		}
	}

	return info
}

func TestCreateTorrentModel_FilesCountExcludesPadding(t *testing.T) {
	t.Parallel()

	info := paddedInfo(50, 60)

	torrent, err := createTorrentModel(protocol.ID{}, info, false, 100)
	require.NoError(t, err)

	require.True(t, torrent.FilesCount.Valid)
	assert.Equal(t, uint(50), torrent.FilesCount.Uint,
		"files_count must not count padding entries that are never stored")
	assert.Len(t, torrent.Files, 50, "only the real files are persisted")
	assert.Equal(t, model.FilesStatusMulti, torrent.FilesStatus)
}

// The threshold logic in infohash_triage re-fetches metainfo whenever
// files_status is over_threshold and files_count <= saveFilesThreshold. So above
// the threshold files_count must remain the torrent's full non-padding count,
// not the number of rows actually written.
func TestCreateTorrentModel_FilesCountIsTheFullCountOverThreshold(t *testing.T) {
	t.Parallel()

	const threshold = 10

	info := paddedInfo(50, 60)

	torrent, err := createTorrentModel(protocol.ID{}, info, false, threshold)
	require.NoError(t, err)

	assert.Equal(t, model.FilesStatusOverThreshold, torrent.FilesStatus)
	assert.Len(t, torrent.Files, threshold, "only the threshold is persisted")
	require.True(t, torrent.FilesCount.Valid)
	assert.Greater(t, torrent.FilesCount.Uint, uint(threshold),
		"a capped files_count would make triage re-fetch this torrent forever")
	assert.Equal(t, uint(50), torrent.FilesCount.Uint)
}

func TestCreateTorrentModel_SingleFileTorrentUnchanged(t *testing.T) {
	t.Parallel()

	torrent, err := createTorrentModel(protocol.ID{}, metainfo.Info{
		Name:        "single",
		PieceLength: 16384,
		Length:      1234,
	}, false, 100)
	require.NoError(t, err)

	assert.Equal(t, model.FilesStatusSingle, torrent.FilesStatus)
	assert.False(t, torrent.FilesCount.Valid)
	assert.Empty(t, torrent.Files)
}

func TestCreateTorrentModel_NoPaddingIsUnaffected(t *testing.T) {
	t.Parallel()

	info := paddedInfo(5, 0)

	torrent, err := createTorrentModel(protocol.ID{}, info, false, 100)
	require.NoError(t, err)

	require.True(t, torrent.FilesCount.Valid)
	assert.Equal(t, uint(5), torrent.FilesCount.Uint)
	assert.Len(t, torrent.Files, 5)
}
