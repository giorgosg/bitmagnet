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
// Call after LockTorrent and before writing the search row. Readiness commits with classification.
func RecordOrdinary(tx *gorm.DB, t model.Torrent, tc *model.TorrentContent, recipe string, rematch bool) error {
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

	if err := tx.Exec(
		"UPDATE torrents SET enhancement_ready = true WHERE info_hash = ?",
		t.InfoHash,
	).Error; err != nil {
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

	var output acceptedOutput
	if err := json.Unmarshal([]byte(accepted.AcceptedOutput), &output); err != nil {
		return err
	}

	priorOutput := accepted.AcceptedOutput

	mode := preserveCatalog
	if rematch {
		// Rematch authority belongs to this ordinary result even when its evidence is
		// unchanged. Retire the older identity proposal, retaining valid release fields.
		mode = ordinaryCatalog
		output.Metadata.Type = ""
		output.Metadata.Work = nil
		output.Metadata.MixedWorks = false
		output.Catalog = catalogIdentityFor(*tc)
		output.CatalogHint = catalogHintFor(current.Hint)

		data, err := json.Marshal(output)
		if err != nil {
			return err
		}

		accepted.AcceptedOutput = string(data)
	}

	if accepted.AcceptedInput == input {
		if err := applyAccepted(tx, current, tc, &accepted, mode); err != nil {
			return err
		}
	} else {
		if !rematch {
			catalog := output.Catalog
			if catalog != nil && catalog.ID == "" {
				catalog = nil
			}

			if err := retainCatalog(tx, current, tc, catalog, output.CatalogHint); err != nil {
				return err
			}
		}

		tc.Torrent = current
		tc.UpdateTsv()
		output.Catalog = catalogIdentityFor(*tc)
		output.CatalogHint = catalogHintFor(current.Hint)

		data, err := json.Marshal(output)
		if err != nil {
			return err
		}

		accepted.AcceptedOutput = string(data)

		if len(output.Tags) > 0 {
			if err := tx.Where("info_hash = ? AND name IN ?", t.InfoHash, output.Tags).
				Delete(&model.TorrentTag{}).
				Error; err != nil {
				return err
			}
		}
	}
	// Persist authority changes without marking a new enhancement evaluation.
	if accepted.AcceptedOutput != priorOutput {
		return tx.Model(&accepted).Update("accepted_output", accepted.AcceptedOutput).Error
	}

	return nil
}

type catalogApplication int

const (
	preserveCatalog catalogApplication = iota
	replaceCatalog
	ordinaryCatalog
)

func catalogIdentityFor(tc model.TorrentContent) *catalogIdentity {
	return &catalogIdentity{
		Type:   tc.ContentType.ContentType.String(),
		Source: tc.ContentSource.String,
		ID:     tc.ContentID.String,
	}
}

func applyAccepted(
	tx *gorm.DB,
	t model.Torrent,
	tc *model.TorrentContent,
	state *model.ClassificationEvaluation,
	mode catalogApplication,
) error {
	var output acceptedOutput
	if err := json.Unmarshal([]byte(state.AcceptedOutput), &output); err != nil {
		return err
	}

	m := output.Metadata
	h := t.Hint

	protect := !output.Rematch
	if mode == preserveCatalog {
		if err := retainCatalog(tx, t, tc, output.Catalog, output.CatalogHint); err != nil {
			return err
		}
	}
	// Catalog identity and supplied type/title/year are authority, not evidence that
	// the torrent should never be evaluated. Unsourced rule metadata is correctable.
	identityProtected := mode == ordinaryCatalog || (mode != replaceCatalog && tc.ContentID.Valid) ||
		(protect && h.ContentID.Valid)

	typeProtected := (protect && !h.IsNil()) || (mode != replaceCatalog && tc.ContentSource.Valid)
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
	output.Catalog = catalogIdentityFor(*tc)
	output.CatalogHint = catalogHintFor(t.Hint)

	b, err := json.Marshal(output)
	if err != nil {
		return err
	}

	state.AcceptedOutput = string(b)

	return nil
}

func decodeOrdinary(data string) (model.TorrentContent, error) {
	var tc model.TorrentContent
	// The private generated-model snapshot retains model.Date's default JSON fields.
	//nolint:musttag
	err := json.Unmarshal([]byte(data), &tc)

	return tc, err
}

// Supplied identity fields veto incompatible retained matches. An unchanged hint
// already superseded by explicit rematch stays superseded until those fields change.
func retainCatalog(tx *gorm.DB, t model.Torrent, tc *model.TorrentContent, fallback, priorHint *catalogIdentity) error {
	var current model.TorrentContent

	err := tx.Where("info_hash = ?", t.InfoHash).Order("id").First(&current).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	hint := catalogHintFor(t.Hint)
	overridden := priorHint != nil && *priorHint == *hint && fallback != nil &&
		!catalogCompatibleWithHint(fallback, t.Hint)

	candidates := []*catalogIdentity{fallback, catalogIdentityFor(*tc), hint}
	if err == nil && current.ContentID.Valid {
		candidates = append([]*catalogIdentity{catalogIdentityFor(current)}, candidates...)
	}

	var identity *catalogIdentity

	for _, candidate := range candidates {
		if candidate != nil && (overridden || catalogCompatibleWithHint(candidate, t.Hint)) {
			identity = candidate
			break
		}
	}

	if identity == nil {
		return nil
	}

	var content model.Content
	if identity.ID != "" {
		err := tx.Preload("Attributes").
			Preload("Collections").
			Where("type = ? AND source = ? AND id = ?", identity.Type, identity.Source, identity.ID).
			First(&content).
			Error
		if errors.Is(err, gorm.ErrRecordNotFound) && tc.Content.ID == identity.ID &&
			tc.Content.Source == identity.Source &&
			tc.Content.Type.String() == identity.Type {
			// A freshly classified provider work is persisted later in this transaction.
			content = tc.Content
		} else if err != nil {
			return err
		}
	}

	tc.ContentType = model.NewNullContentType(identity.Type)
	tc.ContentSource = model.NullString{}

	tc.ContentID = model.NullString{}
	if identity.Source != "" {
		tc.ContentSource = model.NewNullString(identity.Source)
	}

	if identity.ID != "" {
		tc.ContentID = model.NewNullString(identity.ID)
	}

	tc.Content = content

	return nil
}

func catalogHintFor(h model.TorrentHint) *catalogIdentity {
	hint := &catalogIdentity{Type: h.ContentType.String()}
	if h.ContentSource.Valid {
		hint.Source = h.ContentSource.String
	}

	if h.ContentID.Valid {
		hint.ID = h.ContentID.String
	}

	return hint
}

func catalogCompatibleWithHint(identity *catalogIdentity, h model.TorrentHint) bool {
	return (h.IsNil() || identity.Type == h.ContentType.String()) &&
		(!h.ContentSource.Valid || identity.Source == h.ContentSource.String) &&
		(!h.ContentID.Valid || identity.ID == h.ContentID.String)
}
