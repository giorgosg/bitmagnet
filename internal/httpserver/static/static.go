// Package static serves a separately built web UI from disk, on the API origin.
package static

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"

	"github.com/bitmagnet-io/bitmagnet/internal/httpserver"
	"github.com/gin-gonic/gin"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type Params struct {
	fx.In
	Config httpserver.Config
	Logger *zap.SugaredLogger
}

type Result struct {
	fx.Out
	Option httpserver.Option `group:"http_server_options"`
}

func New(p Params) Result {
	return Result{
		Option: &builder{
			config: p.Config.Static,
			logger: p.Logger.Named("static"),
		},
	}
}

type builder struct {
	config httpserver.StaticConfig
	logger *zap.SugaredLogger
}

func (*builder) Key() string {
	return "static"
}

// reservedPaths are mounts whose wildcard would conflict with the API routes.
// Refuse them with a diagnostic instead of allowing gin to panic at startup.
var reservedPaths = map[string]string{
	"/": "a root wildcard would conflict with the API routes",
}

func (b *builder) Apply(e *gin.Engine) error {
	if b.config.Dir == "" {
		return nil
	}

	if err := validatePath(b.config.Path); err != nil {
		return err
	}

	info, statErr := os.Stat(b.config.Dir)
	if statErr != nil {
		return fmt.Errorf("static: cannot serve %q: %w", b.config.Dir, statErr)
	}

	if !info.IsDir() {
		return fmt.Errorf("static: %q is not a directory", b.config.Dir)
	}

	b.logger.Infof("serving %s at %s", b.config.Dir, b.config.Path)

	staticRoutes := e.Group("")
	staticRoutes.Use(func(c *gin.Context) {
		// Alternative UIs are replaced on disk without changing their URLs. Never let a
		// browser reuse an old index, runtime config, or bundle after such a deployment.
		c.Header("Cache-Control", "no-store")
	})
	staticRoutes.StaticFS(b.config.Path, spaFileSystem{http.Dir(b.config.Dir)})

	return nil
}

func validatePath(path string) error {
	if path == "" {
		return errors.New("static: path is required when a directory is configured")
	}

	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("static: path %q must start with a slash", path)
	}

	trimmed := path
	if trimmed != "/" {
		trimmed = strings.TrimSuffix(trimmed, "/")
	}

	if reason, reserved := reservedPaths[trimmed]; reserved {
		return fmt.Errorf("static: path %q is not available - %s", path, reason)
	}

	return nil
}

// spaFileSystem falls back to index.html for paths that do not exist, because a
// single-page app owns its own routing and a deep link must reach it rather than
// 404.
type spaFileSystem struct {
	http.FileSystem
}

func (s spaFileSystem) Open(name string) (http.File, error) {
	f, err := s.FileSystem.Open(name)
	if err != nil && errors.Is(err, fs.ErrNotExist) {
		return s.FileSystem.Open("/index.html")
	}

	return f, err
}
