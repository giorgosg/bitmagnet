# Authentication options

Upstream `main` has **no authentication at all** — GraphQL, Torznab and the web UI are
all open.

It is also permissive cross-origin by default: `internal/httpserver/config.go` sets

```go
Cors: CorsConfig{ AllowedOrigins: []string{"*"}, ... }
```

so any web page in any browser can query a reachable bitmagnet instance directly. That
combination — no auth, `*` CORS — is why bitmagnet is normally run on a trusted network
only, and why a third-party browser UI can talk to it with no server in between.

Three implementations of auth exist. This matters at the GraphQL/HTTP layer regardless
of which frontend you use, so it's relevant even if you replace the web UI entirely.

## Option 1 — `upstream/next` (`internal/auth`, 60 files)

The maintainer's design, on the dormant [`next` branch](upstream-status.md#the-next-branch).
The most complete, and the most likely to match upstream's eventual direction.

```
internal/auth/api_key/     encoding, method_auth, method_create/delete/list,
                           repository, service (+ tests, mocks)
internal/auth/identity/    authenticator chain: anon | api_key | jwt
                           identity types per authenticator, factory
internal/auth/jwt/         config, jwt (+ tests)
internal/auth/rbac/        Casbin: casbin_adapter, casbin_enforcer, casbin_model.conf,
                           permission, role, object_action(_provider), service (+ tests)
internal/auth/http_auth/   middleware
```

**Design:** a chain-of-responsibility authenticator resolving an identity from anonymous,
API key, or JWT, with Casbin RBAC for authorization.

- ➕ Comprehensive, tested, has mocks, API keys are a real requirement for \*arr integrations
- ➕ Aligns with upstream if `next` ever lands
- ➖ Adds Casbin as a dependency
- ➖ **Cannot be extracted from `next` in isolation** — it is entangled with `next`'s
  plugin system, GraphQL rewrite, and database changes. Taking it means taking `next`.
- ➖ Dormant since 2026-04-03 and still a draft

## Option 2 — `gabriel20xx` (`internal/auth`, 5 files)

Built on the `main` lineage. See [forks/gabriel20xx.md](forks/gabriel20xx.md).

Single-user session auth: `auth_users` (bcrypt, singleton unique index) and
`auth_sessions` (sha256 token hash, expiry, cascade delete). GraphQL surface is
`login` / `logout` / `createInitialUser` / `updateCredentials` / `status`, with an
`@authenticated` directive on field definitions.

- ➕ Small enough to read end to end before trusting it
- ➕ Applies cleanly to `main` — standard fx module, one line in `appfx/module.go`
- ➕ Sensible schema decisions, documented in the migration itself; has a `service_test.go`
- ➕ `setupRequired` first-run flow suits a self-hosted app
- ➖ **No API key support** — so no authenticated Torznab for \*arr clients. This is the
  significant gap; sessions alone don't cover the integration surface.
- ➖ Single user by design (deliberately, and reversible — the singleton index can be
  dropped without a schema change)
- ➖ Migration numbered `00033`; renumber to `00021`
- ➖ Arrives from a fork with poor commit hygiene — review the diff, not the log

## Option 3 — roll your own

If you're replacing the web UI, the frontend half of both options above is discarded
regardless, which lowers the cost of this option considerably.

- ➕ Only the pieces actually needed
- ➕ Can design the API-key story for Torznab from the start
- ➖ Security-sensitive code written from scratch
- ➖ Diverges from upstream if `next` lands

## Recommendation

If you front bitmagnet with your own proxy or gateway, doing auth there and leaving
bitmagnet bound to localhost is a legitimate fourth option, and the least work. It
leaves Torznab unauthenticated unless the proxy covers that too.

Otherwise: **start from gabriel20xx's implementation, add API keys.**

It's the only option that applies to `main` without dragging in a rewrite, and it's
small enough to audit properly. Its one real gap — API keys for Torznab — can be filled
by borrowing the _design_ of `next`'s `internal/auth/api_key` (encoding + repository +
service, without Casbin) rather than porting the whole stack.

Concretely:

1. Cherry-pick `internal/auth` + `graphql/schema/auth.graphqls` + the migration from
   gabriel20xx, renumbered to `00021`.
2. Skip everything under `webui/`.
3. Read `upstream/next:internal/auth/api_key/` for reference, then add a minimal
   API-key authenticator to the service.
4. Apply the `@authenticated` directive to GraphQL fields; decide separately how
   Torznab endpoints authenticate.

If `next` ever comes out of draft, expect to throw this away and adopt the upstream
stack — that's an acceptable cost given `next` has been dormant for four months.

## Open question

Whether authentication should also gate the **Torznab** endpoints, and how. Sonarr and
Radarr pass an API key in the URL; none of the three options above documents a decision
here. Worth resolving before implementing.
