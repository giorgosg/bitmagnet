# Auth

Where the auth code is, how it is wired, and **why each control is shaped the way it
is** — because most of them are shaped by a specific failure, and several were introduced
by a fix for the previous one.

For configuring and operating it — the parameter table, the proxy requirements, the known
gaps — see [docs/auth.md](../auth.md).

The vocabulary — Identity, Anonymous identity, User, API key, Invitation, Object action,
Permission, Role, Anonymous access — is fixed in [CONTEXT.md](../../CONTEXT.md). Use those
words and avoid the listed near-synonyms.

Upstream `main` has no authentication at all: GraphQL, Torznab and the web UI are open,
and CORS allows `*`. All of `internal/auth` came from a port of `upstream/next`, adapted
to fx because `next` assembles it through a plugin registry this lineage does not have.

## Packages

| Package                     | Role                                                                 |
| --------------------------- | -------------------------------------------------------------------- |
| `auth/authconfig`           | The config struct, its validation bounds, and `AnonymousPermissions` |
| `auth/authfx`               | fx wiring, and the bootstrap worker that mints the first invitation  |
| `auth/browser_session`      | Issues and expires the secure browser credential cookie              |
| `auth/identity`             | Resolving a credential to an `Identity` — the authenticator chain    |
| `auth/rbac`                 | Permissions, roles, object actions; casbin behind a repository       |
| `auth/user`                 | Accounts, registration, login, password rules, the login throttle    |
| `auth/api_key`              | Machine credentials: base62 encoding, bcrypt hashes, CRUD            |
| `auth/jwt`                  | Signing and parsing session tokens                                   |
| `auth/http_auth`            | The gin middleware, and the `Guard` non-GraphQL handlers use         |
| `gql/auth`, `gql/directive` | The `@auth` directive and the GraphQL permission baseline            |
| `auth/util.go`              | `GenerateRandomString` — JWT secret and invitation codes             |

## Anonymous access is a floor, not a ceiling

`auth.anonymous_access` defaults to **true**, granting the `anon` role every registered
object action **except auth administration**. While it is on, every existing client keeps
working with no credentials. Setting it to `false` is what turns authentication on.

**The exclusion of auth administration is not tidiness; removing it reopens a trapdoor.**
Granting it to `anon` let an unauthenticated caller hand the `anon` role a wildcard
through `putRole`. Role grants live in the database while the compatibility grant is only
in memory, so the wildcard **survived setting `anonymous_access` to `false`** — the switch
documented as "this is how you turn authentication on" left the instance wide open with
nothing visible to show for it.

Nothing is lost by the exclusion: the auth surface is new, so no previously open
installation had it, and the first administrator registers through `self.register`, which
the baseline grants.

## Resolving an identity

`http_auth.AttachAuth` runs early (option key `"auth"`, and http server options are
applied in key order). It does two things and **never rejects**:

1. Puts the client IP on the request context, because the login throttle is keyed by it
   and this is the only layer that knows it.
2. Selects an explicit bearer credential, or the configured browser cookie only when no
   `Authorization` header is present, and runs the authenticator chain over it.
3. Stores the resulting `Identity` together with credential source, rejection state, and
   any infrastructure error on the gin context. GraphQL surfaces infrastructure errors
   before authorization; it never turns them into an Anonymous identity or `unauthorized`.

The chain (`identity/authenticator_chain.go`) is JWT → API key → anonymous, and its
invariant is load-bearing:

> **A revoked credential reports _no match_ and falls through to anonymous. Only an
> infrastructure failure reports a match and an error.**

Every way a credential can fail — unparseable, expired, naming a deleted account, naming a
disabled one — falls through. Do not "improve" a credential path into aborting. A
credential that aborts the chain leaves the request with **no identity at all**, so every
field is refused, including `self.identity` and `self.login` — the two calls the UI needs
to notice its token is dead and recover. The session then stays wedged across reloads,
re-sending the dead token, until the operator clears browser storage by hand.

It took four fixes to hold this across both credential types. `revokedAPIKey` names the
five outcomes meaning "not a usable credential" and lets them fall through, leaving only
genuine repository failures to abort.

The HTTP boundary uses the recorded source to expire rejected browser cookies. It never
expires a cookie ignored because an explicit bearer was present, and it leaves credentials
untouched when authentication failed because the database or RBAC service could not answer.

Ambient browser authority has a second boundary after resolution: a valid cookie-backed
GraphQL mutation must carry an `Origin` whose HTTPS host exactly matches the request host.
The check runs after gqlgen identifies the operation but before it invokes a resolver;
cookie-backed reads do not need this CSRF check because they cannot change server state.
Bearer and Anonymous requests, including `loginBrowser` before a credential exists, retain
their existing CORS behavior. GraphQL has no uploads
or subscriptions, so its multipart and WebSocket transports are disabled rather than left
as unreviewed ways to submit a cookie-backed request.

## Enforcing

Three enforcement points, deliberately not one:

- **GraphQL** — the `@auth(object:, action:)` directive, `gql/auth/directive.go`. Deny by
  default: no identity, or an identity without the object action, is refused. The set of
  directives in the schema _is_ the registered set of GraphQL object actions — `gqlfx`
  extracts them from the schema AST rather than restating them, so adding a directive is
  all it takes to register one.
- **Torznab** — its own handler, ignoring whatever the middleware resolved. See
  [interfaces.md](interfaces.md). Reading the ambient identity made a browser session a
  third credential type here: an operator with the web UI open had their JWT attached by
  the middleware, so Torznab answered `200` to a request carrying no `apikey` at all.
- **Everything else** — `http_auth.Guard`, used by `/import` and `/debug/pprof`. Both fail
  closed, and both resolve the anonymous identity themselves when nothing is on the
  context, so authorization follows the permission model rather than how the server
  happened to be assembled.

A baseline is granted to `anon` and `user` regardless of the anonymous-access setting —
`self::query`, `self::mutate`, `health::query`, `version::query` — because logging in is
itself a GraphQL mutation. Without it, enabling authentication is a permanent lockout.

## The permission model

An **object action** is `namespace/object/action`. A **permission** binds one to one
subject; a **role** is a named set of them. Core roles: `admin`, `editor`, `user`, `anon`.

Object actions and permissions are both collected from fx value groups
(`auth_object_actions`, `auth_permissions`), so a module registers its own without
`authfx` knowing about it.

`rbac.Service` wraps casbin. Because casbin has no context support, the service serialises
every call through a one-slot semaphore and caches the compiled policy for
`RBACCacheTTL` — so a permission change takes up to that long to take effect, and every
authorization check in the process is serialised. Both properties are written up in the
issue notes below.

## Resisting anonymous abuse

`self.login` and `self.register` are reachable without credentials by construction — they
are how anyone gets credentials — and both do bcrypt work. They are the two endpoints an
unauthenticated caller can aim at.

**Login is throttled per bucket, and refuses rather than queues.** `next` used one
process-wide `rate.Limiter` and called `Wait` on it; both halves are wrong. The budget is
shared, so five wrong guesses against usernames that do not exist lock out every account
on the instance, and waiting holds the request open instead of answering it. Attempts are
counted against an LRU of keyed token buckets — one for `(account, source)`, one for the
source alone with a wider budget — and an attempt that cannot be served immediately is
refused immediately.

There is deliberately **no per-account bucket spanning all sources**. It is the one key an
attacker can fill on someone else's behalf, which would let anyone lock any account out
from its owner's own address.

**All of which depends on the source being something the caller cannot pick**, which is
what `http_server.trusted_proxies` is for — see [docs/auth.md](../auth.md).

**Registration validates the invitation before it hashes anything.** Hashing first let an
anonymous caller spend a full bcrypt per request by posting arbitrary codes. The
transaction that claims the invitation still re-reads it; the early pass only rejects
codes that were never going to work.

**Login compares against a decoy hash when the account does not exist**, so a miss costs
what a hit costs. Returning early was a username-enumeration oracle even with identical
error text.

## First administrator

An `auth_initial_invitation` startup worker creates an admin invitation when no enabled
admin user exists, and logs its code. It is idempotent, and **the check and the insert are
serialized by a Postgres advisory lock** held for the transaction. bitmagnet is routinely
run as more than one process against one database; without the lock every replica reads
the same empty state and inserts its own code — a synchronized 16-replica start produced
16 distinct, non-expiring administrator invitations. It has to be a database lock rather
than a mutex, because the processes racing here do not share memory.

## Credentials

- **Session tokens** are JWTs. `jwt.Parse` pins HS256 rather than accepting whatever
  algorithm a token nominates, and checks the issuer it emits — which costs nothing while
  the signing key is unique to the instance, and everything when an operator reuses one
  across services.
- **Browser session cookies** carry the same JWT without returning it through GraphQL.
  `loginBrowser` reuses the User login path, then the authentication-owned cookie service
  writes the credential with the configured `__Secure-` name, `/graphql` path, and strict
  browser-only attributes. `logoutBrowser` expires the identical name and path.
- **API keys** are `secret(12 random bytes) || uint32 id`, base62-encoded to 22 chars,
  bcrypt-hashed in the database. Two fixed defects are recorded in the comments: a
  decoded-length formula borrowed from base32 that rejected one key in 256, and a dropped
  bcrypt error that would have stored a zero hash as the credential.
- **Invitations** are single-use 128-bit codes — the bootstrap one grants admin and never
  expires.
- **All of them come from `auth.GenerateRandomString`**, and that is why the module
  requires Go 1.24. Under the older `crypto/rand.Read` signature, discarding the error
  left the buffer zeroed — minting an all-zero JWT signing key or administrator
  invitation with nothing in the log to say so. See
  [adr/0001](../adr/0001-go-1-24-for-crypto-rand.md).

## The four worth learning from

Every defect in this subsystem was found _after_ the code compiled, vetted and passed the
whole suite. These four are the ones whose lesson generalises past auth — in each, the
security control worked and something around it did not, which is the failure mode least
likely to be caught by a test written after the fix.

**A compatibility default has to be a floor, not a ceiling.** The anonymous-access
trapdoor above: anything a permissive default grants that can _rewrite the permission
model_ is not a default, it is a bypass.

**When designing a refusal, ask what the legitimate user does next, and check that path is
still open.** The chain-abort lockout and the process-wide login limiter were the same
mistake: a dead token refused the query that would have cleared it, and a shared login
budget refused the login that would have replaced it.

**A control is only as good as the least trustworthy input to it.** The spoofable throttle
key is the one to learn from because it was _introduced by a fix_. Replacing the global
limiter with keyed buckets was right, and the reasoning about which bucket an attacker can
fill still stands — what went unexamined was whether the attacker controls the key itself.
They did: the value came from a framework whose default is to believe a request header,
two dependencies away from the code doing the reasoning.

**A redaction guarantee holds only over the sinks it was applied to.** Redacting the
request logger did not cover `gin.Recovery`, which dumps the request line verbatim — in
release mode too, on exactly the broken-pipe branch a Torznab client reaches by
disconnecting mid-response. Keep any new logging sink inside the redaction.

---

_Known defects and improvement ideas referenced above are kept as untracked
`docs/issues/*.local.md` notes, which a given checkout may or may not have._
