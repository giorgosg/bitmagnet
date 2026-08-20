# Authentication

The live record for `codex/auth-port`
([PR #28](https://github.com/giorgosg/bitmagnet/pull/28)), **not yet merged**: what was
built, how it behaves, and what an operator has to decide. Adapted from `upstream/next`.

[auth-port.md](auth-port.md) covers how it got here — the port method, the defects review
found and what they teach, and the alternatives that were weighed. Read that one before
changing a control here, because most of them are shaped by a specific failure.

Upstream `main` has **no authentication at all** — GraphQL, Torznab and the web UI are all
open. It is also permissive cross-origin by default: `internal/httpserver/config.go` sets

```go
Cors: CorsConfig{ AllowedOrigins: []string{"*"}, ... }
```

so any web page in any browser can query a reachable instance directly. That combination
is why bitmagnet is normally run on a trusted network only.

## The default is off, and nothing changes until an operator says so

`auth.anonymous_access` defaults to **true**, which grants the `anon` role every
registered object action **except auth administration**. While it is on, every existing
client keeps working with no credentials: GraphQL, Torznab and the web UI behave exactly
as they did before. Setting it to `false` is what turns authentication on.

So this can merge and ship without changing anything for anyone, and an operator opts in
when they want it. That matters because upstream has no auth, the bundled UI has no login
flow, and existing deployments are open — enabling it unconditionally would lock out
every current client.

**Auth administration is excluded from that grant deliberately, and removing the
exclusion reopens a trapdoor.** Granting it to `anon` let an unauthenticated caller give
the `anon` role a wildcard through `putRole`; role grants live in the database while the
compatibility grant is only in memory, so the wildcard survived setting
`anonymous_access` to `false`. The switch that turns authentication on left the instance
open with nothing outward to show for it. Nothing is lost by the exclusion: the auth
surface is new, so no previously open installation had it, and the first administrator
registers through `self.register`, which the baseline grants.

`upstream/next` uses the same mechanism — its `rbac.ParamAnonymousAccess` also defaults
to `true` — except that `next` distributes the decision across its plugins, each granting
its own object actions to `anon`. Without a plugin registry the same effect is produced
centrally in `authconfig.AnonymousPermissions`.

## What it consists of

```
internal/auth/user/        register, login, invite, password entropy, set_role,
                           set_enabled, list/get/delete
internal/auth/rbac/        Casbin enforcement, roles, permissions, object actions
internal/auth/identity/    authenticator chain: jwt | api_key | anon
internal/auth/api_key/     encoding, auth, create/delete/list, repository
internal/auth/jwt/         token issue and verification
internal/auth/http_auth/   gin middleware and http server option
internal/auth/authconfig/  main-lineage config, and the anonymous-access switch
internal/auth/authfx/      fx wiring and the first-administrator bootstrap
```

A chain-of-responsibility authenticator resolves an identity from a JWT, an API key, or
anonymously, and Casbin evaluates that identity against an object action.

## How a request is authorised

**The middleware only resolves.** It attaches an identity to the request context and
never rejects. Enforcement happens above it: on GraphQL through the `@auth` directive,
and in the Torznab handler.

**The chain always resolves something.** Every way a credential can fail — unparseable,
expired, naming a deleted account, naming a disabled one — falls through to the anonymous
authenticator rather than aborting. Only an infrastructure failure, such as the database
being unreachable, produces an error and no identity. Revocation is expressed as "this
token no longer resolves to its user", never as "this request is aborted"; the permission
model does the rest.

This is a deliberate invariant and it is load-bearing, so do not "improve" a credential
path into aborting. A credential that aborts the chain leaves the request with no
identity at all, so _every_ field is refused — including `self.identity` and `self.login`,
the two calls the UI needs to notice its token is dead and recover. The session then stays
wedged across reloads, re-sending the dead token, until the operator clears browser
storage by hand. It took four separate fixes to hold across both credential types;
`revokedAPIKey` names the five outcomes that mean "not a usable credential" and lets them
fall through, leaving only genuine repository failures to abort.

**Deny by default at the directive.** Every GraphQL root field carries
`@auth(object:, action:)`, and the directive refuses the request unless the resolved
identity holds that object action. No identity, or an identity without the permission, is
refused.

The directive is also the source of truth for the permission set: the schema is walked at
startup and each directive becomes a registered object action in the `graphql` namespace,
so nothing is restated by hand.

A baseline is granted to `anon` and `user` regardless of the anonymous-access setting —
`self::query`, `self::mutate`, `health::query`, `version::query` — because logging in is
itself a GraphQL mutation. Without it, enabling authentication would be a permanent
lockout.

## Configuration

| Key                              | Default        |                                                                   |
| -------------------------------- | -------------- | ----------------------------------------------------------------- |
| `auth.anonymous_access`          | `true`         | `false` enables auth; `true` grants anon all but auth admin       |
| `auth.jwt_secret`                | _(none)_       | random per process when unset, so tokens do not survive a restart |
| `auth.jwt_duration`              | `24h`          |                                                                   |
| `auth.rbac_cache_ttl`            | `1m`           |                                                                   |
| `auth.invitation_required`       | `true`         |                                                                   |
| `auth.email_required`            | `false`        |                                                                   |
| `auth.email_verification`        | `false`        | inert — see Known gaps; `next` defaults it `true`                 |
| `auth.password_min_entropy`      | `70`           |                                                                   |
| `auth.password_hashing_cost`     | bcrypt default | applies to registration _and_ password changes                    |
| `auth.login_requests_per_minute` | `30`           | per bucket, not per process — see below                           |
| `auth.login_request_burst`       | `5`            | per bucket, not per process — see below                           |

Defaults match the corresponding parameters on `next`, except `auth.email_verification`,
which defaults to `false` here and `true` there because neither lineage implements it and
a default of `true` advertises a check that never runs.

**Every bound in that table is a `validate:` tag on `authconfig.Config`, and they are
load-bearing.** `next` carries its constraints in the plugin config builder alongside
each default; re-expressing the parameters as an ordinary struct for `configfx` kept the
defaults and silently dropped the constraints. `login_requests_per_minute: 0` reaches
`rate.Every(time.Minute / 0)` and takes the process down from a config file alone, and
`password_min_entropy: 0` accepts any password at all. `configresolver` enforces the tags
against every resolved config struct. `jwt_duration` greater than zero is not one of
`next`'s constraints, but a zero there issues tokens that have already expired.

One key outside the `auth.` tree matters as much as any of them:

| Key                           | Default   |                                                        |
| ----------------------------- | --------- | ------------------------------------------------------ |
| `http_server.trusted_proxies` | _(empty)_ | whose `X-Forwarded-For` to believe; empty means nobody |

### Secret material

API key secrets are hashed at bcrypt's default cost rather than
`auth.password_hashing_cost`. They are 12 uniformly random bytes, so an offline attack is
infeasible at any work factor, and the cost is paid on every request presenting a key.
Invitation codes are 128 bits for the same reason the bootstrap one needs it: it grants
admin and never expires.

**Those secrets are why this branch raises the module to Go 1.24.** The per-process JWT
secret, the bootstrap invitation and every API key come from `auth.GenerateRandomString`,
which reads `crypto/rand`. Under the pre-1.24 signature `rand.Read` returns an error and
leaves the buffer as it found it, so discarding that error converts an entropy failure
into an all-zero secret — a JWT signing key of zero, an administrator invitation of zero,
and nothing in the log to say so. Go 1.24 made `crypto/rand.Read` incapable of returning
an error; it terminates the program if the system source fails, which is the correct
outcome for a credential. `go.mod`, the `Dockerfile` and the workflow pins move together,
and the error is still checked at the call site rather than left implied by the `go`
directive. That bump is not confined to `go.mod` — see
[auth-port.md](auth-port.md#lint-policy).

## Resisting anonymous abuse

`self.login` and `self.register` are reachable without credentials by construction — they
are how anyone gets credentials in the first place — and both do bcrypt work. That makes
them the two endpoints an unauthenticated caller can aim at.

**Login is throttled per bucket, and refuses rather than queues.** `next` uses one
process-wide `rate.Limiter` and calls `Wait` on it. Both halves are wrong: the budget is
shared, so five wrong guesses against usernames that do not exist lock out every account
on the instance; and waiting holds the request open instead of answering it. Attempts are
counted against an LRU of keyed token buckets — one for `(account, source)`, one for the
source alone with a wider budget — and an attempt that cannot be served immediately is
refused immediately.

There is deliberately **no per-account bucket spanning all sources**. It is the one key an
attacker can fill on someone else's behalf, which would let anyone lock any account out
from its owner's own address. Keying by `(account, source)` means an attacker's guesses
only ever exhaust their own budget; the cost is that an attacker holding many addresses
gets a few guesses from each, which against a password meeting the entropy floor is not a
threat.

The source comes from the HTTP middleware, so a caller reaching the service by any other
route has none and is bounded by account alone. The bucket map is size-capped, so it
cannot be grown without bound by cycling keys; eviction only resets a bucket, which fails
toward availability rather than lockout.

**All of which depends on the source being something the caller cannot pick, which is
what `http_server.trusted_proxies` is for.** The source is gin's `ClientIP()`, and gin
reads that from `X-Forwarded-For` or `X-Real-IP` whenever the peer is a trusted proxy —
where gin's own default is to trust _every_ proxy. Directly reachable, that made the
client address a header the attacker writes, and rotating it bought a fresh bucket per
request. The setting is therefore empty by default: believe nobody, and take the peer that
actually opened the connection. An operator behind a reverse proxy must list it there for
the real client address to survive the hop — and until they do, every request is
attributed to the proxy, which the per-source budget's width is there to absorb.

**Registration validates the invitation before it hashes anything.** bcrypt is
deliberately expensive, and hashing first let an anonymous caller spend a full hash per
request by posting arbitrary invitation codes. The transaction that claims the invitation
still re-reads it, because anything learned before the transaction can be stale by the
time the insert runs; the early pass only rejects codes that were never going to work.

Login's own comparison runs against a decoy hash when the account does not exist, so the
work done for a miss matches the work done for a hit. Returning early was a
username-enumeration oracle even with identical error text.

## First administrator

An `auth_initial_invitation` startup worker creates an admin invitation when no enabled
admin user exists, and logs its code. Without it, an installation that enables
authentication has no way in. It is idempotent: a second start finds the unclaimed
invitation rather than issuing another.

**The check and the insert are serialized by a Postgres advisory lock** held for the
transaction. bitmagnet is routinely run as more than one process against one database, and
without the lock every replica reads the same empty state and inserts its own code — a
synchronized 16-replica start produced 16 distinct, non-expiring administrator
invitations. It has to be a database lock rather than a mutex, because the processes
racing here do not share memory. The lock releases on commit or rollback, so a crashed
replica cannot wedge the next start.

## Torznab

The one surface `next` never wired auth into. The approach here is adapted from the
`kawaii-not-kawaii` fork (`172a784d3`), the only implementation in the fork landscape that
solved it:

- the credential is accepted as the `apikey` query parameter or an `X-Api-Key` header, the
  query parameter being what \*arr clients actually send;
- a refusal is a Torznab XML error, code 100 "Incorrect user credentials", not a bare
  status code, because \*arr clients parse the error code;
- **no network-based bypass.** That fork deliberately excludes Torznab from its
  trusted-network bypass and the same call is made here. Being on the LAN is not a
  credential for machine access.

Two departures. The query-string credential is accepted **only** on this endpoint, where
the protocol requires it — everywhere else the bearer header remains the only accepted
form, since query strings leak into access logs, referrers and browser history. And
authorization goes through rbac rather than that fork's global on/off flag, so Torznab
contributes an object action, `anon` holds it while anonymous access is enabled, and
individual keys can be scoped to it.

**Machine credentials only, enforced in both directions.** The handler ignores whatever
identity the global bearer middleware resolved, and rejects any identity carrying a user
but no API key — an interactive session, whichever slot it arrived in. Reading the ambient
identity made a browser session a third credential type here: an operator with the web UI
open had their JWT attached by the middleware, so Torznab answered `200` to a request
carrying no `apikey` at all, and with the permissive default CORS policy any page that
could make the browser issue that request got Torznab access on the operator's behalf. An
absent credential still resolves the anonymous identity rather than refusing outright, so
whether the endpoint is open depends only on the permission model — including when the
middleware is not mounted at all.

### The key travels in the URL, so it reaches logs

This application redacts `apikey` — and `token`, `password`, `secret`, `api_key` — from
its own request logging, in both the request logger and the panic recovery handler.
**Nothing else in the request path does.** A reverse proxy, ingress controller, CDN or WAF
will record the full URL unless configured otherwise, and API keys do not expire by
default, so read access to those logs is equivalent to application access.

**If you front bitmagnet with a proxy, redact the `apikey` parameter there too.**

Recovery runs through `ginzap.RecoveryWithZap` with the same query-string redaction the
request logger uses, extended to credential-bearing headers, and the middleware stack is
built by one exported `httpserver.Middleware` so a test exercises what the server actually
installs. Both stock `gin.Recovery()` and vendored `ginzap` dump the request line verbatim
on panic — including on the broken-pipe branch in release mode, which is exactly what a
Torznab client reaches by disconnecting mid-response. Keep any new logging sink inside
that redaction.

## Web UI

Login and registration, an account section with API key management, and users, roles and
invitations screens under `dashboard`. Ported from `next`, whose auth components use no
Angular syntax newer than this lineage's Angular 18 supports.

The session token is attached by an Apollo link in `app.config.ts`. A token that no longer
resolves to a user is cleared — the client half of the always-resolves invariant above.
An expired, revoked or deleted-account token yields a successful `self.identity` response
with a null user rather than an error, which is the signal the UI acts on. Were the server
to refuse the query instead, the UI would never reach its token-clearing code.

## Endpoints that are not GraphQL

`/import`, `/metrics` and `/debug/pprof/*` are guarded by object actions in the `http`
namespace, since the identity middleware only resolves and something has to act on it.
Before that they stayed open when anonymous access was disabled — including the
data-mutating importer and `/debug/pprof/cmdline`, which discloses the process command
line.

`/status` is deliberately public, matching the `health::query` grant in the GraphQL
baseline: orchestrators poll it, and it reports liveness only.

## Known gaps

- **No route guards in the UI.** `next`'s `authGuard` was ported and then removed. Now
  that the GraphQL surface is enforced they would be worth adding back, as presentation
  rather than protection: an unauthorised user currently reaches an administrative screen
  and sees its queries refused, instead of being sent to login.
- **`auth.email_verification` does nothing.** The parameter exists on `next` and is
  carried over for fidelity, but its value is never read and no verification code is ever
  issued — the `users.email_verify_code` column is written nowhere. It is inert on `next`
  too, and every other user parameter in the table above is consulted. It defaults to
  `false` here, diverging from `next`, because a parameter that defaults to on while doing
  nothing tells an operator their addresses are verified when they are not. The default
  follows the behaviour, and flips back when the behaviour arrives.
