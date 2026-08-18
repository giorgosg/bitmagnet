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

Three implementations of auth exist, plus the option to build a focused fourth design.
This matters at the GraphQL/HTTP layer regardless of which frontend you use, so it is
relevant even if you replace the web UI entirely.

## Option 1 — `upstream/next` (`internal/auth`, 60 files)

The maintainer's design, on the dormant [`next` branch](upstream-status.md#the-next-branch).
The most complete, and the most likely to match upstream's eventual direction.

```
internal/auth/user/        17 files — register, login, invite, password entropy,
                           set_role/set_enabled, list/get/delete, service, config
internal/auth/rbac/        14 files — Casbin: casbin_adapter, casbin_enforcer,
                           casbin_model.conf, permission, role, object_action(_provider),
                           service(_lazy), subject (+ tests, mocks)
internal/auth/identity/    12 files — authenticator chain: anon | api_key | jwt,
                           identity types per authenticator, factory
internal/auth/api_key/     11 files — encoding, method_auth, method_create/delete/list,
                           repository, service (+ tests, mocks)
internal/auth/jwt/          3 files — config, jwt (+ tests)
internal/auth/http_auth/    1 file  — gin middleware
internal/auth/util.go                (+ test)
```

**Design:** a chain-of-responsibility authenticator resolving an identity from anonymous,
API key, or JWT, with Casbin RBAC for authorization.

**Total: 4,069 lines across 59 Go files** (5 test files, 2 generated mock files), plus an
87-line migration.

### Measured dependency surface

Surveyed 2026-08-18 by extracting every import across all 59 files, then walking the
closure — descending only into packages the `main` lineage does not already provide, since
the port uses `trunk`'s copy of the rest. This corrects an earlier claim in this document
that the package could not be extracted without taking `next` wholesale.

`internal/auth` imports just six bitmagnet packages directly:

| Direct dependency       | Refs | Status on `main` lineage |
| ----------------------- | ---- | ------------------------ |
| `internal/model`        | 19   | present — and see below  |
| `internal/slice`        | 9    | present                  |
| `internal/database/dao` | 7    | present (generated)      |
| `internal/database`     | 3    | present                  |
| `internal/config/param` | 3    | **`next`-only**          |
| `internal/atomic`       | 2    | **`next`-only**          |

Closing over those, the full set of packages that must be ported alongside auth is five,
not two — `internal/config/param` pulls in three more:

| Support package          |     Lines |
| ------------------------ | --------: |
| `internal/config/param`  |     1,428 |
| `pkg/json_schema`        |       915 |
| `internal/logging/level` |       266 |
| `internal/ecma262`       |       234 |
| `internal/atomic`        |       201 |
| **Total**                | **3,044** |

So the port is roughly **7,100 lines**: 4,069 of auth plus 3,044 of support.

Critically, the closure **terminates there**. It reaches nothing in `internal/plugin`,
`internal/gql`, `internal/wasm`, `internal/workers`, `internal/search` or `proto/` — the
plugin and GraphQL entanglement previously recorded here is not in the import graph.

(Walking `next`'s own copies of `internal/database` and `internal/model` instead of
`trunk`'s does drag in `wasm`, `proto` and the plugin registry. That is an artefact of
measuring the wrong thing: those packages already exist on `main`, and the port uses the
`main` versions.)

Third-party dependencies are nearly as clean. `gorm`, `bcrypt`, `testify` and
`x/time/rate` are already in `main`, and so is **`gin` v1.10.0**, which is all
`http_auth/middleware.go` needs. The only genuinely new modules are **`casbin/v2`** and
**`golang-jwt/v5`**.

The six model types it uses — `User`, `APIKey`, `Role`, `Invitation`, `RolePermission`,
`APIKeyPermission` — are **gorm-gen output**, not hand-written: `internal/database/gen/gen.go`
sets `ModelPkgPath` to `internal/model`. Apply the migration and run `task gen-gorm` and
they appear, along with their dao counterparts. Nothing to port by hand.

### Schema

`migrations/00021_auth.sql`, 87 lines, six tables: `roles`, `role_permissions`, `users`,
`invitations`, `api_keys`, `api_key_permissions`. Seeds four core roles (admin, editor,
user, anon). Password and key material are `bytea` hashes; expiry is nullable on both keys
and invitations; foreign keys cascade.

It **collides with `trunk`'s `00021_queue_jobs_fetch_index.sql`** — renumber to `00022`.
Note that `next` also carries a `0022_tags.sql` with a four-digit typo; do not inherit it.

### Assessment

- ➕ Comprehensive, tested, has mocks, API keys are a real requirement for \*arr integrations
- ➕ Aligns with upstream if `next` ever lands
- ➕ Import-decoupled from `next`'s plugin and GraphQL rewrites
- ➕ Model types and dao come from the generator, given the migration
- ➕ The five support packages **compile unmodified against `trunk`** — verified on the port
  branch, so the signature-drift risk is settled for that layer at least
- ➖ Adds Casbin and golang-jwt as dependencies
- ➖ Pulls in five `next`-only support packages (3,044 lines), and `internal/config/param`
  has a **failing test on `next`** (`TestUint32`) that must be fixed rather than carried in
  red — see [the port branch](#port-status) for the cause
- ➖ Import-level decoupling is not compile-level proof for the auth packages themselves:
  `next` also changed the internals of `internal/model` and `internal/database`, so
  signature drift is still possible there
- ➖ Brings no GraphQL wiring — `next`'s gql is a rewrite, so the resolver surface must be
  built fresh against `trunk`'s gqlgen setup
- ➖ **`next` relaxed the lint config for this code**: `var-naming` with
  `skipPackageNameChecks`, `wsl` → `wsl_v5`, and the `if-return` and `nested-structs` revive
  rules dropped. Porting means either adopting those relaxations or fixing the code to
  `trunk`'s stricter config
- ➖ Still a draft: `next` HEAD is 2026-01-31 and `internal/auth/` was last touched
  2026-01-24 (`93dd4c623`)

### Port status

Tracked on `codex/auth-port`, cut from `trunk`.

**Done —** the five support packages, compiling and green on `trunk` with no source changes
beyond lint fixes. `TestUint32` is fixed at its cause: `json_schema.NewValue` returned the
`DocumentNode` that `yaml.Unmarshal` always wraps around a value, annotated with its parse
position, while the `param` encoder emits the value node directly, so two constructions of
the same value compared unequal. `NewValue` and `UnmarshalYAML` now unwrap and clear
position; new tests in `pkg/json_schema` cover it.

**Next —** the renumbered migration and `task gen-gorm`, then `internal/auth` itself.

**Open —** `pkg/json_schema` trips `revive`'s `var-naming` under `trunk`'s config. Either
rename the package to `jsonschema` (diverges from `next`, noisier future re-syncs) or adopt
`next`'s `skipPackageNameChecks` relaxation. This is a repo-wide lint policy decision, not a
port detail.

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

## Option 3 — `kawaii-not-kawaii` (`internal/gql/auth`)

An independent implementation on the `main` lineage, added after the original fork
survey. It combines:

- first-run username/password setup persisted through the fork's config writer;
- signed browser sessions derived from the password hash and a private persisted salt;
- a machine API key accepted by GraphQL and by Torznab as `apikey` or `X-Api-Key`;
- trusted-network bypass with an explicit trusted-proxy list;
- HTTP, middleware, session, and Torznab authorization tests.

- ➕ Covers both browser and \*arr clients, including the Torznab decision left open by
  gabriel20xx
- ➕ Has substantially more boundary and failure-path coverage than the smaller module
- ➕ Remains on the upstream `main` architecture rather than depending on `next`
- ➖ Is coupled to the fork's live config reader/writer and privileged `auth` config update
  path
- ➖ Stores the password hash and API key in the application config rather than dedicated
  database tables
- ➖ Its browser flow is tied to the fork's Angular UI, which is not useful when replacing
  the frontend

This is a design and test source, not a clean cherry-pick. Extracting only the backend
requires deciding which configuration machinery and persistence model to keep.

## Option 4 — build a focused implementation

If you're replacing the web UI, the frontend half of the fork implementations is
discarded regardless, which lowers the cost of this option considerably.

- ➕ Only the pieces actually needed
- ➕ Can design the API-key story for Torznab from the start
- ➖ Security-sensitive code written from scratch
- ➖ Diverges from upstream if `next` lands

## Recommendation

If you front bitmagnet with your own proxy or gateway, doing auth there and leaving
bitmagnet bound to localhost is a separate deployment option, and the least work. It
leaves Torznab unauthenticated unless the proxy covers that too.

Otherwise: **base the implementation on the `upstream/next` auth architecture.** Its
identity chain, user and invitation lifecycle, API-key encoding and repository, JWT
handling, and Casbin authorization model are the source of truth. Port the smallest
coherent backend slice to the `main` lineage rather than designing a competing session
or permission model.

This is an adaptation, not a direct cherry-pick, but the dependency map above makes it a
much smaller one than previously assumed: the auth packages themselves are import-clean,
and the adaptation cost sits in the two `next`-only support packages, the GraphQL surface,
and Torznab. Preserve the auth package boundaries and tests. Gabriel20xx and
kawaii-not-kawaii remain useful for main-lineage HTTP integration tests, first-run
behavior, and Torznab compatibility, but not as the design basis. Do not import either
frontend.

The initial port should include, in dependency order:

1. The five `next`-only support packages — `internal/config/param`, `pkg/json_schema`,
   `internal/logging/level`, `internal/ecma262`, `internal/atomic` — fixing `TestUint32`
   rather than carrying it red.
2. The migration, renumbered to `00022`, then `task gen-gorm` for the model and dao types.
3. The `identity` authenticator chain and anonymous, API-key, and user identities.
4. Revocable, encoded machine API keys using the `next` repository/service split.
5. User bootstrap/login and JWT handling, preserving `next`'s password and token tests.
6. The RBAC permission boundary, including Casbin if it remains required by the extracted
   object/action model.
7. GraphQL middleware plus Torznab API-key enforcement. `next` does not currently wire its
   auth stack into Torznab, so that integration still needs a focused adapter and tests.

### Auth must be opt-in

Upstream `main` has no auth, the bundled Angular UI has no login flow, and existing
deployments are open by default. Enabling authentication unconditionally would lock out
every current client and break the committed web UI.

So the port defaults to **disabled**, and the anonymous identity retains full access until
an operator turns auth on. That keeps `webui/dist` working untouched for as long as the
feature is off, and confines UI work to the enabled path — a login view and a 401 handler —
rather than making it a prerequisite for merging the backend. The `anon` role seeded by the
migration is the natural place to express this.

If `next` comes out of draft, compare the port with the then-current upstream stack and
replace adapters with upstream components where practical.

## Open question

The extraction-boundary question is largely answered by the dependency map above. What
remains is compatibility: whether loopback is trusted implicitly, how Torznab maps its
conventional `apikey` query parameter to a `next` API-key identity, and whether the
existing Angular UI gets a login view or is left to the eventual replacement frontend.

One thing the map does not settle: import-level decoupling is not the same as compiling.
`next` also changed the internals of `internal/model` and `internal/database`, so the port
may still hit signature drift. That is measurable rather than arguable — attempt the port
and record what actually fails.
