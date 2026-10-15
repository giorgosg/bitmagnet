package enhancement

import (
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func workID(w Work) string { return strconv.FormatInt(w.ID, 10) }

// OrdinarySnapshot removes previous enhancement from the classifier's reusable
// sourced-match inputs. Absence of a record leaves legacy classifications intact.
func OrdinarySnapshot(tx *gorm.DB, t *model.Torrent) error {
	var state model.ClassificationEvaluation

	err := tx.Where("info_hash = ? AND stage = 'ordinary'", t.InfoHash).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}

	if err != nil {
		return err
	}

	tc, err := decodeOrdinary(state.OrdinaryOutput)
	if err != nil {
		return err
	}

	// The snapshot owns classification, not a stale copy of shared work metadata.
	// Reuse the freshly hydrated row when its work is unchanged; otherwise load
	// the ordinary work separately (the effective row may now reference another work).
	if ref := tc.ContentRef(); ref.Valid {
		found := false

		for _, current := range t.Contents {
			if current.ContentRef() == ref && current.Content.ID != "" {
				tc.Content = current.Content
				found = true

				break
			}
		}

		if !found {
			var content model.Content
			if err := tx.Preload("Attributes").Preload("Collections").
				Where("type = ? AND source = ? AND id = ?", ref.Val.Type, ref.Val.Source, ref.Val.ID).
				First(&content).Error; err != nil {
				return err
			}

			tc.Content = content
		}
	}

	t.Contents = []model.TorrentContent{tc}

	return nil
}

// RecordOrdinary records a real completed classification and reconstructs its
// effective row with still-valid accepted enhancement in the caller's transaction.
// Call after LockTorrent and before writing the search row. No scheduling occurs.
func RecordOrdinary(tx *gorm.DB, t model.Torrent, tc *model.TorrentContent, recipe string) error {
	current, err := loadTorrent(tx, t.InfoHash)
	if err != nil {
		return err
	}

	observed, err := ObservationFingerprint(t)
	if err != nil {
		return err
	}

	fresh, err := ObservationFingerprint(current)
	if err != nil {
		return err
	}

	if observed != fresh {
		return ErrStale
	}

	snapshot := *tc
	snapshot.Torrent = model.Torrent{}
	snapshot.ID = ""

	// The internal snapshot intentionally retains model.Date default JSON fields.
	//nolint:musttag
	b, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}

	var fields map[string]json.RawMessage
	if err = json.Unmarshal(b, &fields); err != nil {
		return err
	}

	delete(fields, "torrent")

	if !snapshot.ContentID.Valid {
		delete(fields, "content")
	}

	b, err = json.Marshal(fields)
	if err != nil {
		return err
	}

	state := model.ClassificationEvaluation{
		InfoHash:          t.InfoHash,
		Stage:             "ordinary",
		InputFingerprint:  observed,
		RecipeFingerprint: fingerprint([]byte(recipe)),
		Outcome:           "completed",
		OrdinaryOutput:    string(b),
	}
	state.CompletedAt.Time = time.Now().UTC()

	state.CompletedAt.Valid = true
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
		Create(&model.KeyValue{Key: "classifier-recipe/" + state.RecipeFingerprint, Value: recipe}).
		Error; err != nil {
		return err
	}

	if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&state).Error; err != nil {
		return err
	}

	var accepted model.ClassificationEvaluation

	err = tx.Where("info_hash = ? AND stage = 'enhancement'", t.InfoHash).First(&accepted).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}

	if err != nil {
		return err
	}

	_, input, err := inputEvidence(current, snapshot)
	if err != nil {
		return err
	}

	if accepted.AcceptedOutput == "" {
		return nil
	}

	if accepted.AcceptedInput != input {
		var output acceptedOutput
		if err := json.Unmarshal([]byte(accepted.AcceptedOutput), &output); err != nil {
			return err
		}

		if len(output.Tags) == 0 {
			return nil
		}

		return tx.Where("info_hash = ? AND name IN ?", t.InfoHash, output.Tags).
			Delete(&model.TorrentTag{}).
			Error
	}

	return applyAccepted(tx, current, tc, accepted)
}

func applyAccepted(
	tx *gorm.DB,
	t model.Torrent,
	tc *model.TorrentContent,
	state model.ClassificationEvaluation,
) error {
	var output acceptedOutput
	if err := json.Unmarshal([]byte(state.AcceptedOutput), &output); err != nil {
		return err
	}

	m := output.Metadata
	h := t.Hint
	protect := !output.Rematch
	// Catalog identity and supplied type/title/year are authority, not evidence that
	// the torrent should never be evaluated. Unsourced rule metadata is correctable.
	identityProtected := protect && (tc.ContentID.Valid || h.ContentID.Valid)

	typeProtected := protect && (!h.IsNil() || tc.ContentSource.Valid)
	if !identityProtected {
		if m.Type != "" && !typeProtected {
			v, err := model.ParseContentType(m.Type)
			if err != nil {
				return err
			}

			if tc.ContentType.ContentType != v {
				tc.ContentSource = model.NullString{}
				tc.ContentID = model.NullString{}
				tc.Content = model.Content{}
			}

			tc.ContentType = model.NewNullContentType(v)
		}

		if m.MixedWorks && (!protect || !h.ContentSource.Valid) {
			tc.ContentSource = model.NullString{}
			tc.ContentID = model.NullString{}
			tc.Content = model.Content{}
		}

		workCompatible := m.Work != nil &&
			(!typeProtected || m.Work.Type == tc.ContentType.ContentType.String()) &&
			(!protect || !h.ContentSource.Valid || h.ContentSource.String == model.SourceTmdb) &&
			(!protect || !tc.ContentSource.Valid || tc.ContentSource.String == model.SourceTmdb)
		if workCompatible {
			typ, err := model.ParseContentType(m.Work.Type)
			if err != nil {
				return err
			}

			var content model.Content
			if err := tx.Preload("Attributes").
				Preload("Collections").
				Where("type = ? AND source = ? AND id = ?", typ, model.SourceTmdb, workID(*m.Work)).
				First(&content).
				Error; err != nil {
				return err
			}

			tc.ContentType = model.NewNullContentType(typ)
			tc.ContentSource = model.NewNullString(model.SourceTmdb)
			tc.ContentID = model.NewNullString(content.ID)
			tc.Content = content
		}
	}

	if m.Title != "" && (!protect || !h.Title.Valid) {
		tc.InferredTitle = m.Title
	}

	if m.Year != 0 && (!protect || h.ReleaseYear.IsNil()) {
		tc.InferredYear = model.Year(m.Year)
	}

	if len(m.Languages) > 0 && (!protect || len(h.Languages) == 0) {
		tc.Languages = make(model.Languages)
		for _, l := range m.Languages {
			tc.Languages[model.Language(l)] = struct{}{}
		}
	}

	if len(m.Episodes) > 0 && (!protect || len(h.Episodes) == 0) {
		tc.Episodes = make(model.Episodes)
		for season, eps := range m.Episodes {
			tc.Episodes[season] = make(map[int]struct{})
			for _, ep := range eps {
				tc.Episodes[season][ep] = struct{}{}
			}
		}
	}

	if m.VideoResolution != "" && (!protect || !h.VideoResolution.Valid) {
		v, err := model.ParseVideoResolution(m.VideoResolution)
		if err != nil {
			return err
		}

		tc.VideoResolution = model.NewNullVideoResolution(v)
	}

	if m.VideoSource != "" && (!protect || !h.VideoSource.Valid) {
		v, err := model.ParseVideoSource(m.VideoSource)
		if err != nil {
			return err
		}

		tc.VideoSource = model.NewNullVideoSource(v)
	}

	if m.VideoCodec != "" && (!protect || !h.VideoCodec.Valid) {
		v, err := model.ParseVideoCodec(m.VideoCodec)
		if err != nil {
			return err
		}

		tc.VideoCodec = model.NewNullVideoCodec(v)
	}

	if m.Video3D != "" && (!protect || !h.Video3D.Valid) {
		v, err := model.ParseVideo3D(m.Video3D)
		if err != nil {
			return err
		}

		tc.Video3D = model.NewNullVideo3D(v)
	}

	if m.VideoModifier != "" && (!protect || !h.VideoModifier.Valid) {
		v, err := model.ParseVideoModifier(m.VideoModifier)
		if err != nil {
			return err
		}

		tc.VideoModifier = model.NewNullVideoModifier(v)
	}

	if m.ReleaseGroup != "" && (!protect || !h.ReleaseGroup.Valid) {
		tc.ReleaseGroup = model.NewNullString(m.ReleaseGroup)
	}

	if len(output.Tags) > 0 {
		if err := tx.Where("info_hash = ? AND name IN ?", t.InfoHash, output.Tags).
			Delete(&model.TorrentTag{}).
			Error; err != nil {
			return err
		}
	}

	for _, tag := range m.Tags {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
			Create(&model.TorrentTag{InfoHash: t.InfoHash, Name: tag}).
			Error; err != nil {
			return err
		}
	}

	tc.Torrent = t
	tc.UpdateTsv()

	return nil
}

func decodeOrdinary(data string) (model.TorrentContent, error) {
	var tc model.TorrentContent
	// The private generated-model snapshot retains model.Date's default JSON fields.
	//nolint:musttag
	err := json.Unmarshal([]byte(data), &tc)

	return tc, err
}
