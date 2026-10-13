package moderation

// Config controls harmful-keyword matches. The default retains them pending review.
type Config struct {
	AutomaticBan bool
}

func NewDefaultConfig() Config { return Config{} }
