// Package static serves a directory of static files, so that a web UI other than
// the bundled one can be served by bitmagnet itself.
//
// The bundled UI is compiled into the binary with go:embed and mounted at
// /webui, so replacing it means rebuilding. This option is the seam for an
// alternative: point it at a built UI on disk and it is served from the same
// origin as the API, which removes CORS from the picture and lets the browser
// send credentials the way a same-origin page does.
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

// reservedPaths are mounts that already exist. Registering the same route twice
// makes gin panic during startup, which is a worse diagnostic than this error.
var reservedPaths = map[string]string{
	"/":      "the root is redirected by the bundled web UI",
	"/webui": "the bundled web UI is mounted there",
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

	e.StaticFS(b.config.Path, spaFileSystem{http.Dir(b.config.Dir)})

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
// 404. This mirrors what the bundled web UI's mount does for the same reason.
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
