package enhancement

import (
	"encoding/json"
	"path"
	"sort"
	"strings"

	"github.com/bitmagnet-io/bitmagnet/internal/model"
)

// BuildEvidence selects at most twenty stored paths deterministically. It first
// keeps the largest relevant file from each directory, then fills by relevance/size.
// It never treats absent or representative paths as a complete layout.
func BuildEvidence(t model.Torrent) (Evidence, error) {
	e := Evidence{InfoHash: t.InfoHash.String(), Name: clip(t.Name, 500), Size: t.Size}
	if t.FilesCount.Valid {
		count := t.FilesCount.Uint
		e.FileCount = &count
	}

	e.IncompleteFiles = t.FilesStatus == model.FilesStatusNoInfo ||
		t.FilesStatus == model.FilesStatusOverThreshold ||
		!t.FilesCount.Valid ||
		uint(len(t.Files)) != t.FilesCount.Uint
	files := append([]model.TorrentFile(nil), t.Files...)
	sort.Slice(files, func(i, j int) bool {
		a, b := relevant(files[i].Path), relevant(files[j].Path)
		if a != b {
			return a
		}

		if files[i].Size != files[j].Size {
			return files[i].Size > files[j].Size
		}

		return files[i].Path < files[j].Path
	})

	chosen := map[string]bool{}
	dirs := map[string]bool{}

	add := func(f model.TorrentFile) {
		chosen[f.Path] = true
		e.Files = append(e.Files, File{Path: clip(f.Path, 180), Size: f.Size})
	}
	for _, f := range files {
		dir := path.Dir(f.Path)
		if !dirs[dir] {
			dirs[dir] = true

			add(f)

			if len(e.Files) == 20 {
				break
			}
		}
	}

	for _, f := range files {
		if len(e.Files) == 20 {
			break
		}

		if !chosen[f.Path] {
			add(f)
		}
	}

	if len(e.Files) < len(t.Files) {
		e.IncompleteFiles = true
	}

	type classification struct {
		Title       string                    `json:"title,omitempty"`
		Year        model.Year                `json:"year,omitempty"`
		Type        model.NullContentType     `json:"type,omitempty"`
		Source      model.NullString          `json:"source,omitempty"`
		ID          model.NullString          `json:"id,omitempty"`
		Languages   model.Languages           `json:"languages,omitempty"`
		Episodes    model.Episodes            `json:"episodes,omitempty"`
		Resolution  model.NullVideoResolution `json:"videoResolution,omitempty"`
		SourceVideo model.NullVideoSource     `json:"videoSource,omitempty"`
		Codec       model.NullVideoCodec      `json:"videoCodec,omitempty"`
		ThreeD      model.NullVideo3D         `json:"video3D,omitempty"`
		Modifier    model.NullVideoModifier   `json:"videoModifier,omitempty"`
		Group       model.NullString          `json:"releaseGroup,omitempty"`
	}

	ordinary := make([]classification, 0, len(t.Contents))
	for _, c := range t.Contents {
		ordinary = append(
			ordinary,
			classification{
				c.Content.Title,
				c.Content.ReleaseYear,
				c.ContentType,
				c.ContentSource,
				c.ContentID,
				c.Languages,
				c.Episodes,
				c.VideoResolution,
				c.VideoSource,
				c.VideoCodec,
				c.Video3D,
				c.VideoModifier,
				c.ReleaseGroup,
			},
		)
	}

	data, err := json.Marshal(ordinary)
	if err != nil {
		return e, err
	}

	e.Classification = data
	// Hint is supplied evidence, never an enhancement result. Exclude incidental timestamps.
	hint, err := json.Marshal(t.Hint)
	if err != nil {
		return e, err
	}

	var h map[string]json.RawMessage
	if err = json.Unmarshal(hint, &h); err != nil {
		return e, err
	}

	delete(h, "createdAt")
	delete(h, "updatedAt")
	delete(h, "infoHash")
	e.Hint, err = json.Marshal(h)

	return e, err
}

func relevant(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".mkv",
		".mp4",
		".avi",
		".m4v",
		".iso",
		".epub",
		".pdf",
		".flac",
		".mp3",
		".exe",
		".apk",
		".dmg",
		".zip",
		".rar",
		".cbz",
		".cbr":
		return true
	}

	return false
}
