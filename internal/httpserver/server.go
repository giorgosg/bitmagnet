package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/httpserver/ginzap"
	"github.com/bitmagnet-io/bitmagnet/internal/worker"
	"github.com/gin-gonic/gin"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type Params struct {
	fx.In
	Config  Config
	Options []Option `group:"http_server_options"`
	Logger  *zap.Logger
}

type Result struct {
	fx.Out
	Worker worker.Worker `group:"workers"`
}

// NewEngine builds the gin engine with the settings that have to hold before any
// handler runs. It is exported so that tests exercising middleware get an engine
// configured the way the real server configures one — the proxy trust below is
// invisible at the point where it matters, and setting it up by hand in a test
// would only ever test the test.
func NewEngine(config Config) (*gin.Engine, error) {
	g := gin.New()

	// Must precede anything that reads ClientIP. Gin trusts every proxy by
	// default, which makes the reported client address a header the caller
	// writes; see the TrustedProxies field.
	if err := g.SetTrustedProxies(config.TrustedProxies); err != nil {
		return nil, err
	}

	return g, nil
}

// Middleware returns the stack every request passes through before it reaches a
// handler. It is exported for the same reason NewEngine is: what it installs is
// a security property, and a test that builds its own stack would only ever test
// the stack it built.
//
// The recovery middleware is deliberately not gin.Recovery. That one dumps the
// request line verbatim on its broken-pipe path — in release mode too — which
// puts a Torznab apikey, a non-expiring credential that travels in the query
// string, into a log the request logger takes care to keep it out of.
func Middleware(logger *zap.Logger) []gin.HandlerFunc {
	ginLogger := logger.Named("gin")

	return []gin.HandlerFunc{
		ginzap.Ginzap(ginLogger, time.RFC3339, true),
		ginzap.RecoveryWithZap(ginLogger, true),
	}
}

func New(p Params) Result {
	var s *http.Server

	return Result{
		Worker: worker.NewWorker(
			"http_server",
			fx.Hook{
				OnStart: func(context.Context) error {
					gin.SetMode(p.Config.GinMode)

					g, engineErr := NewEngine(p.Config)
					if engineErr != nil {
						return engineErr
					}

					g.Use(Middleware(p.Logger)...)
					options, optionsErr := resolveOptions(p.Config.Options, p.Options)
					if optionsErr != nil {
						return optionsErr
					}
					for _, o := range options {
						if buildErr := o.Apply(g); buildErr != nil {
							return buildErr
						}
					}
					s = &http.Server{
						Addr:    p.Config.LocalAddress,
						Handler: g.Handler(),
					}
					ln, listenErr := net.Listen("tcp", s.Addr)
					if listenErr != nil {
						return listenErr
					}
					go (func() {
						serveErr := s.Serve(ln)
						if !errors.Is(serveErr, http.ErrServerClosed) {
							panic(serveErr)
						}
					})()
					return nil
				},
				OnStop: func(ctx context.Context) error {
					if s == nil {
						return nil
					}
					return s.Shutdown(ctx)
				},
			},
		),
	}
}

func resolveOptions(param []string, options []Option) ([]Option, error) {
	paramMap := make(map[string]struct{})
	for _, p := range param {
		paramMap[p] = struct{}{}
	}

	enabledOptions := make([]Option, 0, len(options))

	foundMap := make(map[string]struct{}, len(options))
	for _, o := range options {
		if _, ok := foundMap[o.Key()]; ok {
			return nil, fmt.Errorf("duplicate http server option: '%s'", o.Key())
		}

		foundMap[o.Key()] = struct{}{}

		enabled := false
		if _, ok := paramMap["*"]; ok {
			enabled = true
		} else if _, ok := paramMap[o.Key()]; ok {
			enabled = true
		}

		if enabled {
			enabledOptions = append(enabledOptions, o)
		}
	}

	for p := range paramMap {
		if _, ok := foundMap[p]; !ok && p != "*" {
			return nil, fmt.Errorf("unknown http server option: '%s'", p)
		}
	}

	sort.Slice(enabledOptions, func(i, j int) bool {
		return enabledOptions[i].Key() < enabledOptions[j].Key()
	})

	return enabledOptions, nil
}

type Option interface {
	Key() string
	Apply(engine *gin.Engine) error
}
