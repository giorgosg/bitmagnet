package classifier

import (
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/tmdb"
)

type dependencies struct {
	moderation       *moderation.Service
	moderationConfig moderation.Config
	search           LocalSearch
	tmdbClient       tmdb.Client
}
