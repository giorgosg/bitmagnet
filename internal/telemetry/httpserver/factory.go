package httpserver

import (
	"github.com/bitmagnet-io/bitmagnet/internal/auth/http_auth"
	"github.com/bitmagnet-io/bitmagnet/internal/httpserver"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/fx"
)

type Params struct {
	fx.In
	PrometheusRegistry lazy.Lazy[*prometheus.Registry]
	Guard              http_auth.Guard
}

type Result struct {
	fx.Out
	PprofOption      httpserver.Option `group:"http_server_options"`
	PrometheusOption httpserver.Option `group:"http_server_options"`
}

func New(p Params) Result {
	return Result{
		PprofOption: pprofBuilder{guard: p.Guard},
		PrometheusOption: prometheusBuilder{
			registry: p.PrometheusRegistry,
			guard:    p.Guard,
		},
	}
}
