package model

import (
	"database/sql"

	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
)

// ClassificationEvaluation belongs to a torrent and stage, never a replaceable search row.
// Accepted state is independent of the last attempt and last completed evaluation.
type ClassificationEvaluation struct {
	AttemptInput      string       `gorm:"column:attempt_input"        json:"attemptInput"`
	AttemptRecipe     string       `gorm:"column:attempt_recipe"       json:"attemptRecipe"`
	InfoHash          protocol.ID  `gorm:"column:info_hash;primaryKey" json:"infoHash"`
	Stage             string       `gorm:"column:stage;primaryKey"     json:"stage"`
	InputFingerprint  string       `gorm:"column:input_fingerprint"    json:"inputFingerprint"`
	RecipeFingerprint string       `gorm:"column:recipe_fingerprint"   json:"recipeFingerprint"`
	Outcome           string       `gorm:"column:outcome"              json:"outcome"`
	CompletedAt       sql.NullTime `gorm:"column:completed_at"         json:"completedAt"`
	AcceptedInput     string       `gorm:"column:accepted_input"       json:"acceptedInput"`
	AcceptedOutput    string       `gorm:"column:accepted_output"      json:"acceptedOutput"`
	OrdinaryOutput    string       `gorm:"column:ordinary_output"      json:"ordinaryOutput"`
	Generation        int64        `gorm:"column:generation"           json:"generation"`
	AttemptOutcome    string       `gorm:"column:attempt_outcome"      json:"attemptOutcome"`
	AttemptedAt       sql.NullTime `gorm:"column:attempted_at"         json:"attemptedAt"`
}
