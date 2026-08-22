package exclause

import "gorm.io/gorm"

// ExtraClausePlugin support plugin that not supported clause by gorm
type ExtraClausePlugin struct{}

// Name return plugin name
func (*ExtraClausePlugin) Name() string {
	return "ExtraClausePlugin"
}

// Clause names GORM builds in order. Named because each is also returned by the
// corresponding clause type's Name method.
const (
	ClauseWith      = "WITH"
	ClauseUnion     = "UNION"
	ClauseIntersect = "INTERSECT"
	ClauseExcept    = "EXCEPT"
)

// buildClauses is the clause order GORM applies to both Query and Row callbacks.
var buildClauses = []string{
	ClauseWith,
	"SELECT",
	"FROM",
	"WHERE",
	"GROUP BY",
	ClauseUnion,
	ClauseIntersect,
	ClauseExcept,
	"ORDER BY",
	"LIMIT",
	"FOR",
}

// Initialize register BuildClauses
func (*ExtraClausePlugin) Initialize(db *gorm.DB) error {
	db.Callback().Query().Clauses = buildClauses
	db.Callback().Row().Clauses = buildClauses

	return nil
}

// New create new ExtraClausePlugin
//
//	// example
//	db.Use(extraClausePlugin.New())
func New() *ExtraClausePlugin {
	return &ExtraClausePlugin{}
}
