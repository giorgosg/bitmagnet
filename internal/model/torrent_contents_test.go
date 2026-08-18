package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTorrentContentUpdateTsvIndexesStandaloneEpisodeLexemes(t *testing.T) {
	t.Parallel()

	torrentContent := TorrentContent{
		Torrent:  Torrent{Name: "Hana Kimi S02E01 1080p"},
		Episodes: Episodes{}.AddEpisode(2, 1),
	}

	torrentContent.UpdateTsv()

	for _, lexeme := range []string{"s2", "s02", "e01", "01", "s02e01"} {
		require.Contains(t, torrentContent.Tsv, lexeme)
	}
}
