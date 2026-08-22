# Auth — the code map

This page says **where the auth code is and how it is wired**. It deliberately does not
restate policy or rationale:

- [docs/auth.md](../auth.md) — what the system does, what an operator configures, and why
  each control is shaped the way it is. Read it before changing a control.
- [docs/auth-port.md](../auth-port.md) — how it was ported from `upstream/next`, the
  defects review found, and the alternatives weighed.

Upstream `main` has no authentication. All of `internal/auth` came from the auth port,
and `auth.anonymous_access` defaults to **true**, so nothing changes for an existing
deployment until an operator turns it off.

## Packages

| Package                     | Role                                                                 |
| --------------------------- | -------------------------------------------------------------------- |
| `auth/authconfig`           | The config struct, its validation bounds, and `AnonymousPermissions` |
| `auth/authfx`               | fx wiring, and the bootstrap worker that mints the first invitation  |
| `auth/identity`             | Resolving a credential to an `Identity` — the authenticator chain    |
| `auth/rbac`                 | Permissions, roles, object actions; casbin behind a repository       |
| `auth/user`                 | User accounts, registration, login, password rules, login throttle   |
| `auth/api_key`              | Machine credentials: base62 encoding, bcrypt hashes, CRUD            |
| `auth/jwt`                  | Signing and parsing session tokens                                   |
| `auth/http_auth`            | The gin middleware and the `Guard` non-GraphQL handlers use          |
| `gql/auth`, `gql/directive` | The `@auth` directive and the GraphQL permission baseline            |
| `auth/util.go`              | `GenerateRandomString` — JWT secret and invitation codes             |

The vocabulary — Identity, Anonymous identity, User, API key, Invitation, Object action,
Permission, Role, Anonymous access — is fixed in [CONTEXT.md](../../CONTEXT.md). Use those
words and avoid the listed near-synonyms.

## Resolving an identity

`http_auth.AttachAuth` runs early (option key `"auth"`, and options are applied in key
order). It does two things and **never rejects**:

1. Puts the client IP on the request context, because the login throttle is keyed by it
   and this is the only layer that knows it.
2. Runs the authenticator chain over the bearer token and, on a match, stores the
   `Identity` on the gin context.

The chain (`identity/authenticator_chain.go`) is JWT → API key → anonymous. Its central
invariant, and the thing every bug in this area came down to:

> **A revoked credential reports _no match_ and falls through to anonymous. An
> infrastructure failure reports a match and an error.**

Get that backwards and either a database outage silently downgrades a session to
anonymous, or an expired token aborts the chain and leaves the request with no identity at
all — refusing even `self.login`, the one call needed to recover. Both happened; both are
documented in the authenticators' comments. Read them before editing a return value.

## Enforcing

Three enforcement points, deliberately not one:

- **GraphQL** — the `@auth(object:, action:)` directive, `gql/auth/directive.go`. No
  identity on the context means no permission, and the error carries the namespace,
  object and action as GraphQL extensions so a client can say what was refused.
- **Torznab** — its own handler, ignoring whatever the middleware resolved. Only the
  `apikey` parameter or `X-Api-Key` header counts, and a user session presented there is
  refused. See [interfaces.md](interfaces.md).
- **Everything else** — `http_auth.Guard`, used by `/import` and `/debug/pprof`. Both fail
  closed, and both resolve the anonymous identity themselves when nothing is on the
  context, so authorization follows the permission model rather than how the server
  happened to be assembled.

## The permission model

An **object action** is `namespace/object/action` — `graphql/torrentContent/query`,
`torznab/torznab/query`, `http/import/write`. A **permission** binds one object action to
one subject; a **role** is a named set of them. Core roles: `admin`, `editor`, `user`,
`anon`.

Object actions and permissions are both collected from fx value groups
(`auth_object_actions`, `auth_permissions`), so a module registers its own without
`authfx` knowing about it. The GraphQL set is _extracted from the schema AST_ rather than
listed by hand, which is why adding an `@auth` directive is all it takes to register a new
object action.

`rbac.Service` wraps casbin. Because casbin has no context support, the service serialises
every call through a one-slot semaphore and caches the compiled policy for
`RBACCacheTTL` (default 1 minute) — so a permission change takes up to that long to take
effect, and every authorization check in the process is serialised. Both properties are
written up in the issue notes below.

## Credentials

- **Session tokens** are JWTs, `auth/jwt`. The secret is generated at startup if
  unconfigured, which means restarts invalidate sessions unless an operator sets one.
- **API keys** are `secret(12 random bytes) || uint32 id`, base62-encoded to 22 chars,
  with the secret bcrypt-hashed in the database (`api_key/encoding.go`). The comments
  there record two fixed defects — a decoded-length formula borrowed from base32 that
  rejected one key in 256, and a dropped bcrypt error that would have stored a zero hash.
- **Invitations** are single-use codes. The `auth_initial_invitation` worker creates one
  at startup and logs it; that is how the first administrator registers.

---

_Known defects and improvement ideas referenced above are kept as untracked `docs/issues/*.local.md` notes, which a given checkout may or may not have._
