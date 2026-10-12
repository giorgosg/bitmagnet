package moderation

// Config has no operator settings while the ledger is an inactive foundation.
// Ingestion and classifier activation are separate changes.
type Config struct{}

func NewDefaultConfig() Config { return Config{} }
