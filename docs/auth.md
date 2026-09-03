# Running bitmagnet with authentication

The operator's page: what to set, what it does, and what you have to do outside bitmagnet
for it to hold. For how the code is put together and why each control is shaped the way it
is, see [architecture/auth.md](architecture/auth.md).

Upstream `main` has **no authentication at all** — GraphQL, Torznab and the web UI are all
open, and CORS allows `*`, so any web page in any browser can query a reachable instance
directly. That is why bitmagnet is normally run on a trusted network only. This lineage
adds authentication, **off by default**: nothing changes for an existing deployment until
you turn it on.

## Turning it on

`auth.anonymous_access` defaults to `true`, which grants the `anon` role every registered
object action except auth administration. Every existing client keeps working with no
credentials. Set it to `false` to require authentication.

Then start the process and **read the log for the bootstrap invitation code**. An
`auth_initial_invitation` worker creates an admin invitation when no enabled admin user
exists and logs its code; that is how the first administrator registers, through the web
UI's registration form. It is idempotent — a restart finds the unclaimed invitation rather
than issuing another — and safe to run as multiple replicas.

**The code is logged once, when it is created.** Later boots report only its last four
characters, enough to tell that an invitation is still outstanding and which one, without
every log file, aggregator and support bundle holding a live path to the first
administrator account. If you lose the code before claiming it, delete the unclaimed
invitation and restart — the next boot mints and logs a new one:

```sql
DELETE FROM invitations
WHERE role_name = 'admin' AND created_by IS NULL AND claimed_by IS NULL;
```

## Configuration

| Key                              | Default              |                                                                   |
| -------------------------------- | -------------------- | ----------------------------------------------------------------- |
| `auth.anonymous_access`          | `true`               | `false` enables auth; `true` grants anon all but auth admin       |
| `auth.jwt_secret`                | _(none)_             | random per process when unset, so tokens do not survive a restart |
| `auth.jwt_duration`              | `24h`                |                                                                   |
| `auth.browser_cookie_name`       | `__Secure-bitmagnet` | must retain the `__Secure-` prefix                                |
| `auth.rbac_cache_ttl`            | `1m`                 | how long a revoked permission or role change stays in force       |
| `auth.invitation_required`       | `true`               |                                                                   |
| `auth.email_required`            | `false`              |                                                                   |
| `auth.email_verification`        | `false`              | inert — see Known gaps                                            |
| `auth.password_min_entropy`      | `70`                 |                                                                   |
| `auth.password_hashing_cost`     | bcrypt default       | applies to registration _and_ password changes                    |
| `auth.login_requests_per_minute` | `30`                 | per bucket, not per process                                       |
| `auth.login_request_burst`       | `5`                  | per bucket, not per process                                       |
| `graphql.introspection`          | `false`              | `__schema` and `__type` queries; off unless asked for             |
| `graphql.playground`             | `false`              | GraphiQL on `GET /graphql`; off means the route 404s              |

**Set `auth.jwt_secret` if you do not want every restart to log everyone out.** Unset, it
is generated per process.

**The two `graphql.*` keys gate developer surface, not access.** Schema introspection and
the GraphiQL page carry no authorization of their own, so on an instance reachable
anonymously they hand out a full map of the API. Nothing shipped needs either at runtime —
the web UI's client is generated from the schema files at build time — so both default to
off, and a developer turns them on deliberately.

## Browser login

Same-origin browser clients can call `self.loginBrowser` to receive the session credential
as an `HttpOnly`, `Secure`, `SameSite=Strict` cookie on `/graphql`. The mutation returns no
credential or Identity snapshot; query `self.identity` after it succeeds. `self.logoutBrowser`
expires the same cookie and succeeds even when the browser has no usable credential.

The cookie name defaults to `__Secure-bitmagnet` and is configurable through
`auth.browser_cookie_name`, but the `__Secure-` prefix is required. Its expiry follows
`auth.jwt_duration`. Because browsers reject `Secure` cookies over plain HTTP, browser login
requires HTTPS in development as well as production. Bearer login remains available through
`self.login` for clients that manage their own credential.

The top-level `self` query and mutation are a recovery boundary, not Role Permissions.
Identity discovery, registration, both login forms, and browser logout remain reachable even
when the current Role has no Permissions. Sensitive fields below that boundary — listing,
creating, or deleting API keys — still require an authenticated User session and reject API
key identities as well as Anonymous callers.

An explicit `Authorization` header always takes precedence over the browser cookie. A bad
explicit bearer credential falls back to the Anonymous identity; it does not borrow the
cookie's authority. An expired, malformed, disabled, or deleted-User cookie also falls back
to Anonymous, and the response expires that cookie so the next request can recover cleanly.
Database and authorization-service failures remain errors and do not clear the cookie.

Cookie-backed GraphQL mutations require an `Origin` of `https://<request-host>` before any
resolver runs. Cookie-backed queries remain available without `Origin`; they cannot change
server state. Explicit bearer requests do not depend on `Origin`, and anonymous requests —
including `loginBrowser` before a browser credential exists — remain governed by CORS. The
GraphQL endpoint accepts JSON POST only; multipart uploads and WebSocket upgrades are not
supported.

## GraphQL authentication error codes

Authentication and registration failures expose stable machine-readable values at
`errors[].extensions.code`. Clients must branch on the code, not on the human-readable
message or its wrapping. Application errors retain their GraphQL `path` and `locations`.

| Code                                    | Meaning                                                        |
| --------------------------------------- | -------------------------------------------------------------- |
| `INVALID_CREDENTIALS`                   | The username/password pair is unusable                         |
| `USER_DISABLED`                         | The credentials are correct, but the User is disabled          |
| `LOGIN_THROTTLED`                       | The login bucket has no capacity; retry later                  |
| `USER_ALREADY_EXISTS`                   | Registration would duplicate a User                            |
| `USERNAME_INVALID`                      | The username does not meet server validation                   |
| `INVITATION_REQUIRED`                   | Registration requires an Invitation code                       |
| `INVITATION_INVALID`                    | The Invitation code does not exist                             |
| `INVITATION_EXPIRED`                    | The Invitation has expired                                     |
| `INVITATION_CLAIMED`                    | The Invitation has already been claimed                        |
| `EMAIL_REQUIRED`                        | `auth.email_required` is on and no email was supplied          |
| `EMAIL_INVALID`                         | The email does not match the server's address pattern          |
| `PASSWORD_INSUFFICIENT_ENTROPY`         | The password is below `auth.password_min_entropy`              |
| `ROLE_NOT_FOUND`                        | The named Role does not exist                                  |
| `PERMISSION_INVALID`                    | A requested API-key Object action is not a registered one      |
| `UNAUTHORIZED`                          | The Identity lacks the refused GraphQL Object action           |
| `AUTHENTICATION_INFRASTRUCTURE_FAILURE` | A credential could not be resolved because a dependency failed |
| `USER_SESSION_REQUIRED`                 | The field requires an interactive User session                 |
| `API_KEY_MANAGEMENT_FORBIDDEN`          | An API-key Identity attempted to manage API keys               |
| `INTERNAL_SERVER_ERROR`                 | An unclassified server-side failure occurred                   |

`UNAUTHORIZED` also includes `namespace`, `object`, and `action` in its extensions. The
two session-specific codes are field-level account constraints, not Role-Permission
refusals, so they do not invent an Object action. `ROLE_NOT_FOUND` and
`PERMISSION_INVALID` are input validation: both name something the caller supplied that
the server does not recognise. Previously one reached the client as a foreign-key
violation presented as `INTERNAL_SERVER_ERROR`, and the other was accepted silently as a
key that grants nothing. Authentication infrastructure and
unclassified internal failures use fixed public messages; their underlying error details
are not returned to the client. GraphQL parsing and validation errors remain gqlgen's
ordinary protocol errors rather than being mislabeled as application failures.

## Magnes integration environment

Exercise Magnes against the same boundary a browser will use in production. The reference
harness is `internal/gql/resolvers/auth_integration_test.go`; it mounts the production Gin
authentication middleware and gqlgen server over an isolated, fully migrated PostgreSQL
database. Run that matrix, including the unchanged Torznab boundary, with:

```bash
TEST_POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/postgres' \
  go test ./internal/gql/resolvers ./internal/torznab/httpserver -count=1
```

For a real Magnes browser run, make these three properties explicit:

1. Set `auth.jwt_secret` to one fixed value shared by every bitmagnet replica and every
   restart in the environment. A generated-per-process secret invalidates the browser
   cookie whenever the backend restarts. A deterministic test-only value is acceptable in
   an isolated integration environment; do not reuse it in production.
2. Leave `http_server.trusted_proxies` empty when the browser connects directly. When TLS
   terminates at a reverse proxy, set it to that proxy's actual CIDR — and only that CIDR —
   so forwarded client addresses feed the login throttle without becoming caller-controlled.
3. Serve Magnes and `/graphql` from the exact same browser-visible HTTPS origin, including
   port. The `__Secure-` cookie is rejected over plain HTTP, and cookie-authorized mutations
   require an `Origin` of `https://<request-host>`. A TLS-terminating proxy may use HTTP to
   bitmagnet internally, but it must preserve the browser-facing `Host` when forwarding
   `/graphql`.

Set `auth.anonymous_access` to `false` for the authenticated Magnes scenario, and also run
the matrix once with its default `true` value to preserve the open-installation contract.
Registration uses the bootstrap Invitation from the bitmagnet log and remains separate
from login; after registration, submit the username and password to `loginBrowser`, then
query `self.identity`. Browser code must never read or store the cookie value.

**Every bound in that table is enforced, and they are load-bearing.**
`login_requests_per_minute: 0` reaches `rate.Every(time.Minute / 0)` and takes the process
down from a config file alone; `password_min_entropy: 0` accepts any password at all; a
`jwt_duration` of zero issues tokens that have already expired. `configresolver` validates
every resolved config struct, so a bad value fails at startup rather than in production.

One key outside the `auth.` tree matters as much as any of them:

| Key                           | Default   |                                                        |
| ----------------------------- | --------- | ------------------------------------------------------ |
| `http_server.trusted_proxies` | _(empty)_ | whose `X-Forwarded-For` to believe; empty means nobody |

## If you run bitmagnet behind a reverse proxy

Two things need doing, and neither is optional once authentication is on.

**1. Set `http_server.trusted_proxies` to your proxy's CIDR.** The login throttle is keyed
partly by client address, taken from gin's `ClientIP()`. Gin's own default is to trust
_every_ proxy, which would make that address a header the attacker writes — rotating
`X-Forwarded-For` buys a fresh budget per request. bitmagnet therefore defaults to
trusting nobody and using the peer that actually opened the connection. Behind a proxy and
with this unset, every request is attributed to the proxy and shares one bucket; the
per-source budget is wide enough to absorb that, but the per-account protection is what is
really doing the work until you set it.

**2. Redact the `apikey` query parameter in your proxy's logs.** The Torznab protocol
carries its credential in the URL, and API keys do not expire by default — so read access
to a log recording full URLs is equivalent to application access. bitmagnet redacts
`apikey`, `token`, `password`, `secret` and `api_key` from **its own** logging, in both
the request logger and the panic recovery path. Nothing else in the request path does. A
reverse proxy, ingress controller, CDN or WAF will record the full URL unless you tell it
not to.

## Torznab and \*arr clients

The credential is accepted as the `apikey` query parameter (what \*arr clients actually
send) or an `X-Api-Key` header. A refusal is a Torznab XML error, code 100 "Incorrect user
credentials", not a bare status code, because \*arr clients parse the error code.

**There is no network-based bypass**: being on the LAN is not a credential for machine
access. And Torznab takes machine credentials only — a browser session presented in the
`apikey` slot is refused, and the endpoint ignores whatever the global bearer middleware
resolved.

Issue an API key from the web UI's account section. Keys can be scoped to individual
object actions, so a key handed to Prowlarr can be allowed Torznab and nothing else.

## Endpoints that are not GraphQL

`/import`, `/metrics` and `/debug/pprof/*` are guarded by object actions in the `http`
namespace, so disabling anonymous access closes them — including the data-mutating
importer and `/debug/pprof/cmdline`, which discloses the process command line.

`/import` additionally requires `Content-Type: application/json` and answers 415 otherwise.
This is defence against the browser rather than against the operator: without it, a
cross-origin `POST` of `text/plain` is a CORS simple request and lands with no preflight,
whatever `allowed_origins` says. Command-line clients already send the header — the
importing guide has always shown it — so nothing that worked before stops working.

`/status` is deliberately public, matching the `health::query` grant in the GraphQL
baseline: orchestrators poll it, and it reports liveness only.

## Known gaps

- **No route guards in the web UI.** An unauthorised user reaches an administrative screen
  and sees its queries refused, rather than being sent to login. The GraphQL surface is
  enforced, so this is presentation, not protection.
- **`auth.email_verification` does nothing.** The parameter is carried over from
  `upstream/next` for fidelity, but its value is never read and no verification code is
  ever issued — the `users.email_verify_code` column is written nowhere. It is inert on
  `next` too. It defaults to `false` here, diverging from `next`, because a parameter that
  defaults to on while doing nothing tells an operator their addresses are verified when
  they are not. The default follows the behaviour, and flips back when the behaviour
  arrives.
- **The bundled web UI keeps its token in `localStorage`, not in the session cookie.** The
  Angular screens authenticate with `self.login`, which returns the JWT, and store it under
  `bitmagnet-jwt` where any script on the origin can read it. `self.loginBrowser` and the
  `HttpOnly` cookie described above have no caller in `webui/src` outside the generated
  client.

  This is the shipped default rather than an oversight. The cookie path was built for a
  separately served same-origin browser client — the Magnes integration environment above
  is what it is for — and the bundled auth screens are transitional: they exist so that
  `auth.anonymous_access: false` does not leave the bundled UI unusable, and they come out
  once that replacement is complete. They are presentation, never a boundary; the `@auth`
  directive enforces server-side regardless of what the browser holds.

  What it means for an operator: script injection anywhere on the API origin yields a
  bearer token valid for the whole of `auth.jwt_duration`, and `logoutBrowser` expires the
  cookie without revoking the token it carried, so signing out does not shorten that
  window. Prefer `loginBrowser` for any client you write yourself, and keep
  `auth.jwt_duration` no longer than you would accept a leaked token living.

- **CORS origins still default to `*`.** Carried over from before this lineage had
  authentication. While anonymous access is on, any web page can query a reachable
  instance as the anonymous identity — including an instance bound to a LAN address that
  the page could not otherwise reach. Narrowing it is safe only if you know where your web
  UI is served from: the bundled UI shares the API's origin and needs no CORS at all, but
  serving it separately is supported, and an empty origin list breaks that deployment.
  Set `http_server.cors.allowed_origins` explicitly if you serve the UI from another
  origin.

  The allowed _headers_ are no longer `*`; they are the four the server actually reads
  (`Content-Type`, `Authorization`, `X-Api-Key`, `X-Import-Id`). A client sending anything
  else cross-origin now fails its preflight, and needs the header added to
  `http_server.cors.allowed_headers`.
