package httpserver

import (
	"net/http"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/http_auth"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type prometheusBuilder struct {
	registry lazy.Lazy[*prometheus.Registry]
	guard    http_auth.Guard
}

func (prometheusBuilder) Key() string {
	return "prometheus"
}

func (b prometheusBuilder) Apply(e *gin.Engine) error {
	r, err := b.registry.Get()
	if err != nil {
		return err
	}

	h := promhttp.HandlerFor(r, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})

	e.Any("/metrics", func(c *gin.Context) {
		if !b.guard.Allow(c, http_auth.ObjectActionMetrics) {
			c.AbortWithStatus(http.StatusUnauthorized)

			return
		}

		h.ServeHTTP(c.Writer, c.Request)
	})

	return nil
}
