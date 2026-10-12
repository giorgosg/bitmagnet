// Package static serves bundled or external web UIs on the API origin.
package static

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"os"
	"strings"

	"github.com/bitmagnet-io/bitmagnet/internal/httpserver"
	"github.com/bitmagnet-io/bitmagnet/webui"
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
		if !b.config.Bundled {
			return nil
		}

		return b.mountBundled(e)
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

func (b *builder) mountBundled(e *gin.Engine) error {
	assets := webui.Files()
	if assets == nil {
		return nil
	}

	assets, err := fs.Sub(assets, "public")
	if err != nil {
		return fmt.Errorf("static: bundled assets: %w", err)
	}

	if err := validatePath(b.config.Path); err != nil {
		return err
	}

	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return fmt.Errorf("static: bundled index: %w", err)
	}

	path := strings.TrimSuffix(b.config.Path, "/")
	index = []byte(strings.Replace(string(index), `<base href="/" />`,
		`<base href="`+html.EscapeString(path+"/")+`" />`, 1))

	basePath, err := json.Marshal(path)
	if err != nil {
		return fmt.Errorf("static: bundled path: %w", err)
	}

	config := []byte(`window.MAGNES_API_URL = "/graphql"; window.MAGNES_BASE_PATH = ` + string(basePath) + `;`)
	files := http.StripPrefix(path, http.FileServer(http.FS(assets)))
	e.Match([]string{http.MethodGet, http.MethodHead}, path+"/*filepath", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")

		name := strings.TrimPrefix(c.Param("filepath"), "/")
		if name == "config.js" {
			c.Data(http.StatusOK, "text/javascript; charset=utf-8", config)
			return
		}

		_, statErr := fs.Stat(assets, name)
		if name == "" || name == "index.html" || errors.Is(statErr, fs.ErrNotExist) {
			c.Data(http.StatusOK, "text/html; charset=utf-8", index)
			return
		}

		files.ServeHTTP(c.Writer, c.Request)
	})
	e.GET("/", func(c *gin.Context) { c.Redirect(http.StatusTemporaryRedirect, path+"/") })
	b.logger.Infof("serving bundled UI at %s", path)

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
