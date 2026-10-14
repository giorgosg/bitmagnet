package httpserver

import (
	"net/http"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/browser_session"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/http_auth"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/identity"
	"github.com/bitmagnet-io/bitmagnet/internal/httpserver"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/gin-gonic/gin"
)

func New(service lazy.Lazy[*moderation.Service], authenticator identity.Authenticator) httpserver.Option {
	return option{service: service, authenticator: authenticator}
}

type option struct {
	service       lazy.Lazy[*moderation.Service]
	authenticator identity.Authenticator
}

func (option) Key() string { return "moderation" }

func (o option) Apply(engine *gin.Engine) error {
	guard := http_auth.NewGuard(o.authenticator)

	engine.GET(browser_session.ModerationExportPath, func(ctx *gin.Context) {
		ctx.Header("Cache-Control", "no-store")
		ctx.Header("X-Content-Type-Options", "nosniff")
		// Unlike older endpoints, a new private surface must fail closed without auth.
		if o.authenticator == nil || !guard.Allow(ctx, http_auth.ObjectActionModerationExport) {
			ctx.AbortWithStatus(http.StatusForbidden)
			return
		}

		includeAutomatic := false

		switch ctx.Query("includeAutomatic") {
		case "", "false":
		case "true":
			includeAutomatic = true
		default:
			ctx.AbortWithStatus(http.StatusBadRequest)
			return
		}

		service, err := o.service.Get()
		if err != nil {
			ctx.AbortWithStatus(http.StatusInternalServerError)
			return
		}

		ctx.Header("Content-Type", "application/json")
		ctx.Header("Content-Disposition", `attachment; filename="moderation-bans-v1.json"`)

		if err = service.Export(
			ctx.Request.Context(),
			ctx.Writer,
			moderation.ExportOptions{IncludeAutomatic: includeAutomatic},
		); err != nil {
			_ = ctx.Error(err)
			if !ctx.Writer.Written() {
				ctx.Header("Content-Disposition", "")
				ctx.AbortWithStatus(http.StatusInternalServerError)
			} else {
				ctx.Abort()
			}
		}
	})

	return nil
}
