package enhancement_test

import (
	"fmt"
	"testing"

	"github.com/bitmagnet-io/bitmagnet/internal/enhancement"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/stretchr/testify/require"
)

func TestRepresentativeEvidenceKeepsLargeFilesAndPathDiversity(t *testing.T) {
	t.Parallel()

	torrent := model.Torrent{
		Name:        "Series pack",
		FilesStatus: model.FilesStatusMulti,
		FilesCount:  model.NewNullUint(26),
	}
	for i := range 25 {
		torrent.Files = append(
			torrent.Files,
			model.TorrentFile{Path: fmt.Sprintf("Season1/episode%02d.mkv", i), Size: uint(1000 - i)},
		)
	}

	torrent.Files = append(torrent.Files, model.TorrentFile{Path: "Season2/episode1.mkv", Size: 1})
	evidence, err := enhancement.BuildEvidence(torrent)
	require.NoError(t, err)
	require.Len(t, evidence.Files, 20)
	require.True(t, evidence.IncompleteFiles)
	require.Contains(t, evidence.Files, enhancement.File{Path: "Season1/episode00.mkv", Size: 1000})
	require.Contains(t, evidence.Files, enhancement.File{Path: "Season2/episode1.mkv", Size: 1})

	again, err := enhancement.BuildEvidence(torrent)
	require.NoError(t, err)
	require.Equal(t, evidence, again)
}

func TestEvidenceIncludesOrdinaryWorkTitleWithoutInferringReleaseLanguage(t *testing.T) {
	t.Parallel()

	torrent := model.Torrent{
		Name: "A release",
		Contents: []model.TorrentContent{
			{
				ContentType: model.NewNullContentType(model.ContentTypeMovie),
				Content: model.Content{
					Title:            "Translated Title",
					ReleaseYear:      model.Year(1982),
					OriginalLanguage: model.NewNullLanguage(model.Language("fr")),
				},
			},
		},
	}
	evidence, err := enhancement.BuildEvidence(torrent)
	require.NoError(t, err)
	require.Contains(t, string(evidence.Classification), `"title":"Translated Title"`)
	require.Contains(t, string(evidence.Classification), `"year":1982`)
	require.NotContains(t, string(evidence.Classification), `"fr"`)
}
