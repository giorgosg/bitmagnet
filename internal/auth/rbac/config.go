package rbac

import "time"

// AnonymousAccess reports whether permissions stored against the anon role take
// effect. It is the live `auth.anonymous_access`, reaching this package through
// authfx because authconfig cannot be imported here - authconfig imports rbac.
//
// It is a deny-override, not a source of grants: false drops anon's permissions
// when the policy is compiled, so the anon subject matches nothing whatever the
// database holds. True simply defers to the stored rows.
type AnonymousAccess bool

// CacheTTL is how long the compiled casbin policy is reused. The live value
// comes from authconfig.Config.RBACCacheTTL, the `auth.rbac_cache_ttl` key,
// through authfx.
type CacheTTL time.Duration
