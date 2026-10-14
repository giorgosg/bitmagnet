package httpserver

import (
	"errors"
	"mime"
	"net/http"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/browser_session"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/http_auth"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/identity"
	"github.com/bitmagnet-io/bitmagnet/internal/httpserver"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/gin-gonic/gin"
)

func New(
	service lazy.Lazy[*moderation.Service],
	authenticator identity.Authenticator,
	cookie browser_session.Cookie,
) httpserver.Option {
	return option{service: service, authenticator: authenticator, cookie: cookie}
}

type option struct {
	service       lazy.Lazy[*moderation.Service]
	authenticator identity.Authenticator
	cookie        browser_session.Cookie
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

	engine.POST(browser_session.ModerationImportPath, func(ctx *gin.Context) {
		ctx.Header("Cache-Control", "no-store")

		if o.authenticator == nil || !guard.Allow(ctx, http_auth.ObjectActionModerationImport) {
			ctx.AbortWithStatus(http.StatusForbidden)
			return
		}

		if resolution, ok := http_auth.GetResolution(ctx); ok && resolution.UsesBrowserAuthority() {
			if o.cookie == nil || o.cookie.RequireSameOrigin(ctx.Request.Context()) != nil {
				ctx.AbortWithStatus(http.StatusForbidden)
				return
			}
		}

		contentType, _, err := mime.ParseMediaType(ctx.GetHeader("Content-Type"))
		if err != nil || contentType != "application/json" {
			ctx.AbortWithStatus(http.StatusUnsupportedMediaType)
			return
		}

		ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, moderation.MaxBanFileBytes)

		service, err := o.service.Get()
		if err != nil {
			ctx.AbortWithStatus(http.StatusInternalServerError)
			return
		}

		result, err := service.Import(ctx.Request.Context(), ctx.Request.Body)
		if err != nil {
			var tooLarge *http.MaxBytesError
			switch {
			case errors.Is(err, moderation.ErrBanFileTooLarge), errors.As(err, &tooLarge):
				ctx.AbortWithStatus(http.StatusRequestEntityTooLarge)
			case errors.Is(err, moderation.ErrInvalidBanFile):
				ctx.AbortWithStatus(http.StatusBadRequest)
			default:
				_ = ctx.Error(err)
				ctx.AbortWithStatus(http.StatusInternalServerError)
			}

			return
		}

		ctx.JSON(http.StatusOK, result)
	})

	return nil
}
