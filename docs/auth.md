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

This is an adaptation, not a direct cherry-pick: `next` auth depends on its plugin,
GraphQL, and database rewrites. Begin with a dependency and schema map, preserve the
auth package boundaries and tests, and introduce explicit adapters where the `main`
lineage differs. Gabriel20xx and kawaii-not-kawaii remain useful for main-lineage HTTP
integration tests, first-run behavior, and Torznab compatibility, but not as the design
basis. Do not import either frontend.

The initial port should include:

1. The `identity` authenticator chain and anonymous, API-key, and user identities.
2. Revocable, encoded machine API keys using the `next` repository/service split.
3. User bootstrap/login and JWT handling, preserving `next`'s password and token tests.
4. The RBAC permission boundary, including Casbin if it remains required by the extracted
   object/action model.
5. GraphQL middleware plus Torznab API-key enforcement. `next` does not currently wire its
   auth stack into Torznab, so that integration still needs a focused adapter and tests.

If `next` comes out of draft, compare the port with the then-current upstream stack and
replace adapters with upstream components where practical.

## Open question

The remaining decisions are extraction boundaries and compatibility: which parts of
`next`'s plugin and database infrastructure must be adapted, whether loopback is trusted
implicitly, how Torznab maps its conventional `apikey` query parameter to a `next` API-key
identity, and how an existing open deployment enables auth without locking out clients.
