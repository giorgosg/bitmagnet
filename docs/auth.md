# Authentication

**Status: implemented on `codex/auth-port`
([PR #28](https://github.com/giorgosg/bitmagnet/pull/28)), not yet merged.** Adapted from
`upstream/next`. This document records what was built, why, and what is still missing;
the survey of the alternatives that were considered is kept at the end, because it
explains the choice and because two of them contributed material.

Upstream `main` has **no authentication at all** — GraphQL, Torznab and the web UI are all
open. It is also permissive cross-origin by default: `internal/httpserver/config.go` sets

```go
Cors: CorsConfig{ AllowedOrigins: []string{"*"}, ... }
```

so any web page in any browser can query a reachable instance directly. That combination
is why bitmagnet is normally run on a trusted network only.

## Authentication is opt-in, and off by default

`auth.anonymous_access` defaults to **true**, which grants the `anon` role every
registered object action. While it is on, every existing client keeps working with no
credentials: GraphQL, Torznab and the web UI behave exactly as they did before.

Setting it to `false` is what turns authentication on. This is the same mechanism
`upstream/next` uses — its `rbac.ParamAnonymousAccess` also defaults to `true` — except
that `next` distributes the decision across its plugins, each granting its own object
actions to `anon`. Without a plugin registry the same effect is produced centrally in
`authconfig.AnonymousPermissions`.

The practical consequence: this feature can be merged and shipped without changing
anything for anyone, and an operator opts in when they want it.

## What it consists of

```
internal/auth/user/        register, login, invite, password entropy, set_role,
                           set_enabled, list/get/delete
internal/auth/rbac/        Casbin enforcement, roles, permissions, object actions
internal/auth/identity/    authenticator chain: anon | api_key | jwt
internal/auth/api_key/     encoding, auth, create/delete/list, repository
internal/auth/jwt/         token issue and verification
internal/auth/http_auth/   gin middleware and http server option
internal/auth/authconfig/  main-lineage config, and the anonymous-access switch
internal/auth/authfx/      fx wiring and the first-administrator bootstrap
```

**Design:** a chain-of-responsibility authenticator resolves an identity from a JWT, an
API key, or anonymously, and Casbin evaluates that identity against an object action.

The middleware only _resolves_ an identity and attaches it to the request context — it
never rejects. Enforcement happens above it: on GraphQL through the `@auth` directive,
and in the Torznab handler.

### GraphQL enforcement

Every root field carries `@auth(object:, action:)`, and the directive refuses the request
unless the resolved identity holds that object action. It is **deny by default** — no
identity, or an identity without the permission, is refused.

The directive is also the source of truth for the permission set: the schema is walked at
startup and each directive becomes a registered object action in the `graphql` namespace,
so nothing is restated by hand.

A baseline is granted to `anon` and `user` regardless of the anonymous-access setting —
`self::query`, `self::mutate`, `health::query`, `version::query` — because logging in is
itself a GraphQL mutation. Without it, enabling authentication would be a permanent
lockout.

### Schema

`migrations/00022_auth.sql`, six tables: `roles`, `role_permissions`, `users`,
`invitations`, `api_keys`, `api_key_permissions`, seeding four core roles — `admin`,
`editor`, `user`, `anon`. Renumbered from `next`'s `00021`, which collides with
`00021_queue_jobs_fetch_index.sql`.

The model and dao types are **gorm-gen output**, not hand-written:
`internal/database/gen/gen.go` sets `ModelPkgPath` to `internal/model`, so applying the
migration and running `task gen-gorm` produces them. The generated files came out
byte-identical to `next`'s, which is the strongest evidence that the extraction is faithful.

### Configuration

| Key                              | Default        |                                                                   |
| -------------------------------- | -------------- | ----------------------------------------------------------------- |
| `auth.anonymous_access`          | `true`         | the opt-in switch; `false` enables auth                           |
| `auth.jwt_secret`                | _(none)_       | random per process when unset, so tokens do not survive a restart |
| `auth.jwt_duration`              | `24h`          |                                                                   |
| `auth.rbac_cache_ttl`            | `1m`           |                                                                   |
| `auth.invitation_required`       | `true`         |                                                                   |
| `auth.email_required`            | `false`        |                                                                   |
| `auth.email_verification`        | `true`         |                                                                   |
| `auth.password_min_entropy`      | `70`           |                                                                   |
| `auth.password_hashing_cost`     | bcrypt default |                                                                   |
| `auth.login_requests_per_minute` | `30`           |                                                                   |
| `auth.login_request_burst`       | `5`            |                                                                   |

Defaults match the corresponding parameters on `next`.

### First administrator

An `auth_initial_invitation` startup worker creates an admin invitation when no enabled
admin user exists, and logs its code. Without it, an installation that enables
authentication has no way in. It is idempotent: a second start finds the unclaimed
invitation rather than issuing another.

### Torznab

Torznab is the one surface `next` never wired auth into. The approach here is adapted
from the `kawaii-not-kawaii` fork (`172a784d3`), the only implementation in the fork
landscape that solved it:

- the credential is accepted as the `apikey` query parameter or an `X-Api-Key` header,
  the query parameter being what \*arr clients actually send;
- a refusal is a Torznab XML error, code 100 "Incorrect user credentials", not a bare
  status code, because \*arr clients parse the error code;
- **no network-based bypass.** That fork deliberately excludes Torznab from its
  trusted-network bypass and the same call is made here. Being on the LAN is not a
  credential for machine access.

**The key travels in the URL, so it reaches logs.** This application redacts
`apikey` (and `token`, `password`, `secret`, `api_key`) from its own request
logging. Nothing else in the request path does: a reverse proxy, ingress
controller, CDN or WAF will record the full URL unless configured otherwise, and
API keys do not expire by default, so read access to those logs is equivalent to
application access. **If you front bitmagnet with a proxy, redact the `apikey`
parameter there too.**

Two departures from it. The query-string credential is accepted **only** on this
endpoint, where the protocol requires it — everywhere else the bearer header remains the
only accepted form, since query strings leak into access logs, referrers and browser
history. And authorization goes through rbac rather than that fork's global on/off flag,
so Torznab contributes an object action, `anon` holds it while anonymous access is
enabled, and individual keys can be scoped to it.

### Web UI

Login and registration, an account section with API key management, and users, roles and
invitations screens under `dashboard`. Ported from `next`, whose auth components use no
Angular syntax newer than this lineage's Angular 18 supports.

The session token is attached by an Apollo link in `app.config.ts`. A token that no
longer resolves to a user is cleared: the identity chain always resolves _something_,
falling back to anonymous, so an expired token yields a successful response with a null
user rather than an error.

### Endpoints that are not GraphQL

`/import`, `/metrics` and `/debug/pprof/*` are guarded by object actions in the
`http` namespace, since the identity middleware only resolves an identity and
something has to act on it. Before that they stayed open when anonymous access
was disabled — including the data-mutating importer and `/debug/pprof/cmdline`,
which discloses the process command line.

`/status` is deliberately left public, matching the `health::query` grant in the
GraphQL baseline: orchestrators poll it, and it reports liveness only.

## Known gaps

- **No route guards in the UI.** `next`'s `authGuard` was ported and then removed. Now
  that the GraphQL surface is enforced they would be worth adding back, as a matter of
  presentation rather than protection: an unauthorised user currently reaches an
  administrative screen and sees its queries refused, instead of being sent to login.
- **`auth.email_verification` does nothing.** The parameter exists on `next` and is
  carried over for fidelity, but its value is never read and no verification code is ever
  issued — the `users.email_verify_code` column is written nowhere. It is inert on `next`
  too. Every other user parameter in the table above is consulted. Treat it as a
  placeholder, not a feature.
- **Go version divergence.** `next`'s tests use `testing.T.Context`, which needs Go 1.24;
  this module targets 1.23.6 while `next` is on 1.25.1. `context.Background()` is
  substituted rather than bumping the toolchain, which is a repo-wide decision.

## How the port was done

Kept because the method is reusable for the rest of `next`, and because the numbers
corrected an assumption this document previously recorded.

### Dependency surface

Measured by extracting every import across all 59 files of `internal/auth`, then walking
the closure — descending only into packages the `main` lineage does not already provide,
since the port uses `trunk`'s copy of the rest.

`internal/auth` imports just six bitmagnet packages directly: `internal/model`,
`internal/slice`, `internal/database/dao`, `internal/database`, `internal/config/param`
and `internal/atomic`. Closing over those, five packages had to be ported alongside it:

| Support package          |     Lines |
| ------------------------ | --------: |
| `internal/config/param`  |     1,428 |
| `pkg/json_schema`        |       915 |
| `internal/logging/level` |       266 |
| `internal/ecma262`       |       234 |
| `internal/atomic`        |       201 |
| **Total**                | **3,044** |

So about **7,100 lines**: 4,069 of auth plus 3,044 of support. All five compile against
`trunk` unmodified.

Critically the closure **terminates there** — it reaches nothing in `internal/plugin`,
`internal/gql`, `internal/wasm`, `internal/workers`, `internal/search` or `proto/`. An
earlier version of this document claimed auth could not be extracted without taking
`next` wholesale; that is not so at import level.

(Walking `next`'s own copies of `internal/database` and `internal/model` instead of
`trunk`'s does drag in `wasm`, `proto` and the plugin registry — an artefact of measuring
the wrong thing, since those packages already exist on `main`.)

New third-party modules: **casbin/v2**, **golang-jwt/v5**, **go-password-validator** on
the Go side, **picomatch** in the web UI. Everything else the support packages need was
already present, including `gin`.

### Adaptations

Where `next`'s design depends on infrastructure this lineage does not have:

- **fx instead of the plugin builder.** `next` assembles auth through
  `internal/plugin/core/auth/plugin.go`, which needs its plugin registry, worker runner
  and ref system. `authfx` is written directly against fx, mirroring the same service
  set, value groups and the lazy indirection that breaks the rbac dependency cycle.
- **`database.DaoTransactionProvider`.** On `next` this belongs to a provider abstraction
  built on its worker-runner lifecycle. `internal/database/provider.go` adapts the
  lineage's own `lazy.Lazy[*dao.Query]` to the same two-method surface, so the seam is in
  the same place and a future switch to `next`'s provider would not touch
  `internal/auth`.
- **Config as a struct.** `next` declares eleven parameters through its plugin config
  builder; `authconfig.Config` expresses them for `configfx` and converts them to the
  atomic values the services take.
- **Apollo Client v3, not v4.** `query` types `data` as non-null while `mutate` does not,
  so `next`'s assertions are noise on the former and load-bearing on the latter. Its
  `CombinedGraphQLErrors` handling and `dataState`-based `filterComplete` have no v3
  equivalent and were rewritten.

### Defects found and fixed on the way

Both were found by porting, not by review, and both had a test written that was observed
failing first:

- **`rbac.PutRole` could not revoke.** It returned early when handed an empty object
  action set, **before** the delete, so revoking every permission from a role silently did
  nothing and returned a zero-valued `RoleInfo` with a `nil` error — indistinguishable
  from success. It is the only way to change a role's permissions; there is no
  `DeleteRolePermissions`, which is also why `next`'s rbac test carries a commented-out
  revocation case: it exercises an API that was never written.
- **`json_schema.NewValue` returned a document, not a value.** `yaml.Unmarshal` into a
  `yaml.Node` always yields a `DocumentNode` wrapper annotated with its parse position,
  while the `param` encoder emits the value node directly — so two constructions of the
  same value compared unequal. This is the cause of `TestUint32` failing on `next`.

Also removed rather than shipped: an expandable detail row in `next`'s users table whose
entire content is the literal string `Hello`.

### Lint policy

`trunk` adopted `next`'s `var-naming` `skipPackageNameChecks` relaxation, needed for
underscored package names such as `json_schema`. `next` also switches `wsl` to `wsl_v5`
and drops the `if-return` and `nested-structs` revive rules; **`wsl_v5` does not exist in
golangci-lint v2.1.6**, which both repositories pin, and `golangci-lint config verify`
rejects it — so that part cannot be adopted and the affected code was fixed by hand
instead.

---

## Alternatives considered

### `upstream/next` — chosen

The maintainer's design, on the dormant [`next` branch](upstream-status.md#the-next-branch).
Most complete, most likely to match upstream's eventual direction, and the only one with
a real permission model. Still a draft: `next` HEAD is 2026-01-31 and `internal/auth/`
was last touched 2026-01-24 (`93dd4c623`).

### `gabriel20xx` (`internal/auth`, 5 files)

Single-user session auth on the `main` lineage: `auth_users` (bcrypt, singleton unique
index) and `auth_sessions` (sha256 token hash, expiry, cascade delete), with an
`@authenticated` directive. Small enough to read end to end, and its first-run flow suits
a self-hosted app — but **no API key support**, so no authenticated Torznab, and no
permission model. Not used.

### `kawaii-not-kawaii` (`internal/gql/auth`) — contributed the Torznab design

An independent implementation on the `main` lineage: first-run setup through the fork's
config writer, signed browser sessions, a machine API key accepted by GraphQL and
Torznab, and a trusted-network bypass. Coupled to that fork's live config reader/writer
and stores credentials in application config rather than database tables, so it was not a
design basis — but it is the only fork that wired auth into Torznab, and that part was
adapted here with attribution.

### Build a focused implementation

Rejected: security-sensitive code written from scratch, diverging from upstream if `next`
lands.

### Front it with a proxy

Still a valid deployment option, and the least work: do auth at the gateway and bind
bitmagnet to localhost. It leaves Torznab unauthenticated unless the proxy covers that
too, and it does not give per-key scoping.
