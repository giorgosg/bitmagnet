package rbac

import (
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/config/param"
)

// CacheTTL is how long the compiled casbin policy is reused. The live value
// comes from authconfig.Config.RBACCacheTTL, the `auth.rbac_cache_ttl` key,
// through authfx.
type CacheTTL time.Duration

// ParamCacheTTL has no reader. It describes the setting above for a
// param-driven configuration surface that this fork does not have: nothing
// collects the Param* values, there is no init and no registry, and the live
// path is authconfig through configfx. It is left in place because it is
// inert and describes a real setting; the wider question of whether the Param*
// machinery should be wired up or deleted is open, and is not this file's to
// answer.
//
// AnonymousAccess used to be declared here the same way, and was not inert in
// the same harmless sense: authconfig.Config.AnonymousAccess is the live
// `auth.anonymous_access`, so the tree carried two identically named settings
// of which only one did anything. That is a reader walking into the wrong one,
// so it was deleted rather than left.
var ParamCacheTTL = param.MustNew(
	param.Duration[CacheTTL](false),
	param.Description[CacheTTL]("Permissions cache TTL"),
	param.Default(CacheTTL(time.Minute)),
)
