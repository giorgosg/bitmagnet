package httpserver

import (
	"github.com/bitmagnet-io/bitmagnet/internal/auth/identity"
	"github.com/bitmagnet-io/bitmagnet/internal/httpserver"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/torznab"
	"github.com/gin-gonic/gin"
)

func New(
	lazyClient lazy.Lazy[torznab.Client],
	config torznab.Config,
	authenticator identity.Authenticator,
) httpserver.Option {
	return builder{
		lazyClient:    lazyClient,
		config:        config,
		authenticator: authenticator,
	}
}

type builder struct {
	lazyClient    lazy.Lazy[torznab.Client]
	config        torznab.Config
	authenticator identity.Authenticator
}

func (builder) Key() string {
	return "torznab"
}

func (b builder) Apply(e *gin.Engine) error {
	client, err := b.lazyClient.Get()
	if err != nil {
		return err
	}

	h := handler{
		config:        b.config,
		client:        client,
		authenticator: b.authenticator,
	}
	e.GET("/torznab/*any", h.handleRequest)

	return nil
}
