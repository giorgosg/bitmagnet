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

| Package                     | Role                                                                   |
| --------------------------- | ---------------------------------------------------------------------- |
| `auth/authconfig`           | The config struct, its validation bounds, and the fixture read surface |
| `auth/authfx`               | fx wiring, the bootstrap worker, and the anonymous-role reset          |
| `auth/browser_session`      | Issues and expires the secure browser credential cookie                |
| `auth/identity`             | Resolving a credential to an `Identity` — the authenticator chain      |
| `auth/rbac`                 | Permissions, roles, object actions; casbin behind a repository         |
| `auth/user`                 | Accounts, registration, login, password rules, the login throttle      |
| `auth/api_key`              | Machine credentials: base62 encoding, SHA-256 hashes, CRUD             |
| `auth/jwt`                  | Signing and parsing session tokens                                     |
| `auth/http_auth`            | The gin middleware, and the `Guard` non-GraphQL handlers use           |
| `gql/auth`, `gql/directive` | The `@auth` directive and the GraphQL permission baseline              |
| `auth/util.go`              | `GenerateRandomString` — JWT secret and invitation codes               |

## Anonymous access starts with no grants

`auth.anonymous_access` defaults to **true**, but the `anon` role starts empty. The flag
allows its stored grants to take effect; it grants nothing by itself. An administrator can
grant chosen read actions through `putRole`, or set the flag to `false` as a deny-override.

**The exclusion of auth administration is not tidiness; removing it reopens a trapdoor.**
Granting it to `anon` let an unauthenticated caller hand the `anon` role a wildcard
through `putRole`. Role grants live in the database while the compatibility grant is only
in memory, so the wildcard **survived setting `anonymous_access` to `false`** — the switch
documented as "this is how you turn authentication on" left the instance wide open with
nothing visible to show for it.

The first administrator registers through `self.register`, which is outside the role's
stored permissions. `version` and `health` also remain available before login.

**Role names are validated on the way in, so that wildcard cannot be stored at all.** The
matcher is `globMatch(r.sub, p.sub)`, and the _stored policy_ is the pattern rather than
the request — a role literally named `*` matches every subject, `anon` included.
`Role.Validate` restricts a name to the username character class, which contains no glob
metacharacter, and `rbac.Service.PutRole` refuses before the repository is reached. The
exclusion above is what keeps an unauthenticated caller away from `putRole` at all; this
is the second lock, for a caller who already holds `auth:mutate`.

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

**A stale token epoch is one of those ways to fail.** `users.token_epoch` is the
generation of a user's sessions; every JWT carries the value current when it was minted,
and `authenticator_jwt.go` refuses one that is behind the row. It is the only revocation a
stateless token can have: the token cannot be recalled, so what changes is the row it is
checked against. `user.RevokeSessions` bumps it, and `UpdatePassword` does the same inside
its own transaction, so a rotated password and the sessions it invalidates commit together.

The increment is computed by the database (`token_epoch = token_epoch + 1`) rather than
read and written back, so two concurrent revocations cannot both write the same value and
leave the first one's tokens alive.

This revokes per **account**, not per session, which is the trade recorded in
[ADR 0003](../adr/0003-revoke-sessions-with-a-per-user-token-epoch.md): it costs nothing
per request, because the authenticator already loads the user row, and it cannot express
"sign out of this device only". Tokens minted before the claim existed decode as epoch 0,
which is the column's default, so deploying it revokes nothing retroactively.

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
  default: no identity, or an identity without the object action, is refused. The deliberate
  exception is the top-level `Query.self` and `Mutation.self` recovery boundary; individual
  key-management fields below it require a User session. The set of directives in the schema
  _is_ the registered set of GraphQL object actions — `gqlfx` extracts them from the schema
  AST rather than restating them, so adding a directive is all it takes to register one.
- **Torznab** — its own handler, ignoring whatever the middleware resolved. See
  [interfaces.md](interfaces.md). Reading the ambient identity made a browser session a
  third credential type here: an operator with the web UI open had their JWT attached by
  the middleware, so Torznab answered `200` to a request carrying no `apikey` at all.
- **Everything else** — `http_auth.Guard`, used by `/import`, `/metrics`, and
  `/debug/pprof`. All three fail closed, and all three resolve the anonymous identity
  themselves when nothing is on the context, so authorization follows the permission
  model rather than how the server happened to be assembled.

A baseline is granted to `anon` and `user` regardless of the anonymous-access setting —
`health::query` and `version::query`. Recovery calls under `self` need no baseline Permission:
their top-level fields are intentionally outside the Role-Permission model, so no Role
assignment can hide Identity discovery, login, registration, or browser logout.

The gqlgen HTTP server owns one error presenter for this boundary. It classifies wrapped
authentication, registration, and authorization sentinels with `errors.Is`, emits stable
`extensions.code` values, and preserves gqlgen's path and source locations. The
authorization refusal keeps its Object action and whether the Identity is anonymous on
the typed error. The presenter returns `AUTHENTICATION_REQUIRED` for an anonymous refusal
and `UNAUTHORIZED` for an authenticated one, adding `namespace`, `object`, and `action` to
both; the old `GraphQLExtensions()` method was not a gqlgen
hook and its result was silently discarded. Unknown resolver errors and authentication
infrastructure causes are replaced with fixed public messages, while parsing and validation
errors remain useful protocol errors.

## The permission model

An **object action** is `namespace/object/action`. A **permission** binds one to one
subject; a **role** is a named set of them. Core roles: `admin`, `editor`, `user`, `anon`.

Object actions and permissions are both collected from fx value groups
(`auth_object_actions`, `auth_permissions`), so a module registers its own without
`authfx` knowing about it.

## What anonymous means

**The anon role is an ordinary database role, and `anonymous_access` is a deny-override on
it.** There is one source of anonymous permission — `role_permissions` — and one switch that
either honours it or does not:

- `true`: anonymous callers get exactly what the anon role holds. `putRole` is how that
  changes, in the running process.
- `false`: `rbac.service.withoutOverriddenAnonymous` drops every stored permission that
  could grant the anon subject when the policy is compiled, so casbin never sees them.

### Why the deny is at compile and not at the decision

`Enforce`, `EnforceAny`, `EnforceEvery` and `FilterAllowed` are the hot path — the `@auth`
directive fires per field of every query — and they hold no service lock. A branch there
would be evaluated per field for a value that cannot change without a restart. The TTL and
reload machinery already recompiles the policy, so the override takes effect on the same
schedule as any other permission change.

### The invariant that makes a subject filter sufficient

casbin's matcher is `globMatch(r.sub, p.sub)`: the **stored** value is the pattern, not the
request. So a policy subject of `role::*` would match an anonymous request without being
spelled `role::anon`. `Role.Validate` forbids glob metacharacters in a role name, which is
what stops `putRole("*", …)` granting to every role — but the filter asks casbin's own
matcher rather than comparing names, so it stays correct if that ever relaxes. A pattern
casbin cannot parse is treated as matching, because the safe direction for an authorization
filter is to withhold. `rbac.service_test` pins both halves.

### What the override does not reach

Its scope is **stored** permissions, deliberately. `internal/gql/auth.Permissions` grants
anon `version:query` and `health:query` regardless of the setting, because the web UI shell
reads them before anyone can log in; filtering the provider set as well would take the login
screen away from a closed instance, which is the state that most needs one. There is an
integration test pinning that, so the scope fails loudly if it moves.

### Empty default and the old read surface

`authfx.TranslateAnonRole` clears the anon role on the first start with this default and
records `auth.anon_role_empty_default` in `key_values`. This applies to an existing
installation too. The old automatic read grants and later administrator grants share the
same rows, so the upgrade cannot tell them apart. The first start clears all of them; later
starts preserve the administrator's new edits. A transaction advisory lock serializes
concurrent replicas around the marker check and delete. The HTTP worker has a decorator
that runs this reset before it opens a listener, including when an operator starts only
`http_server`; the separate auth startup worker can run in either order and then sees the
marker.

`authconfig.AnonymousReadSurface` now serves only the development fixture's explicit
`--anonymous-access` option. It selects registered read actions except `auth`, `pprof`,
and `metrics`, so the browser harness can exercise an open catalogue. Its allow-list
continues to exclude new action verbs by default. The object-name exclusions are string
literals because `authconfig` cannot import `http_auth` without a cycle; a test pins
them against the registered actions.

There used to be an `authconfig.AnonymousPermissions` provider that granted the read surface
from the setting alone, held in memory. That left **two** sources of anonymous permission,
unioned — the setting's and the database's — so the setting withheld only its own half and a
grant written through `putRole` survived being switched off, with nothing in the
configuration or the logs disagreeing. That was `docs/issues/0012`.

### What reporting shows

`GetRole` reports stored grants truthfully whether or not the setting honours them, because
role administration has to show what is **configured** — so `listRoles` shows an overridden
grant. `identity.Anon.EffectivePermissions` answers the different question "what can this
caller reach", and routes through `FilterAllowed` so that it agrees with `Enforce` rather
than advertising a permission every request for which is refused.

`rbac.Service` wraps casbin and caches the compiled policy for `RBACCacheTTL` — so a
permission change takes up to that long to take effect.

**Decisions run concurrently.** The enforcer is a `casbin.SyncedEnforcer`, which takes a
read lock to enforce and a write lock to load, so a decision can never see a half-loaded
policy and never waits for another decision. The service's own `casbinMutex` guards only
`casbinDeps` and `lastUpdate`, and is held while the policy is compiled or reloaded —
never while a decision is made. `acquireCasbin` reads under it and, when the TTL has
passed, takes it for writing and checks again, so a stampede at expiry costs one
permissions query rather than one each.

This replaced a one-slot semaphore that every authorization decision in the process passed
through, one at a time — every `@auth` field, every Torznab request, every guarded
endpoint. The semaphore made the wait cancellable, which the mutex does not; what is
waited for is now one permissions query once per TTL rather than every other decision and
every role write.

**Role writes do not block decisions.** `PutRole` and `DeleteRole` hold a separate
`writeMutex` across the repository write and the reload that follows it — two
administrators writing at once would otherwise be able to leave the compiled policy
reflecting the earlier write until the TTL expired — and that mutex is not on the decision
path. It used to be the same semaphore, so an authorization decision queued behind a
database write.

One trap for anything editing this: `casbinDeps` embeds `*casbin.SyncedEnforcer`, which
itself embeds a plain `*casbin.Enforcer` under the field name `Enforcer`. Writing
`s.Enforcer.LoadPolicy()` therefore reaches the **unsynchronised** method of the same name.
Say `s.SyncedEnforcer.LoadPolicy()`.

**Roles are cached on the same TTL**, behind their own `RWMutex`.
Every authentication resolves a role, and the repository preloads its permissions, so the
lookup was two statements straight to the database on every request — the steady-state
cost of an instance a Torznab client is polling. A role written by this process
invalidates the snapshot immediately, so an administrator sees their own change; a change
made by another process becomes visible on the TTL, as permissions already did.

Because the `@auth` directive fires **per field**, a decision that asks more than one
question is worth combining. `EnforceEvery` takes a list of subject groups — every group
must allow, and a group is satisfied by any subject in it — and answers them in one
`BatchEnforce`. `identity.APIKey` is the caller that needs it: its role gate and its scope
gate are two questions for one decision, and asked separately an N-field query paid 2N
where N would do. It mattered more when each of those rounds also took the process-global
semaphore; the batch is still the cheaper shape. `FilterAllowed` is the
same idea for a different shape of question — which of these object actions does this
subject allow — and exists so that reporting an identity's permissions asks casbin rather
than reimplementing its matcher.

**Selected is not the same as effective.** An API key names the object actions it may
perform, and `createAPIKey` checks each against the registered set: `api_key_permissions`
has no foreign key to a registry because there is no registry table, so an unchecked typo
produced a key that granted nothing and reported success, and an unchecked wildcard —
matched by `globMatch` at enforcement — collapsed the key's own gate to "anything".
What the key was scoped to is reported by `APIKey.permissions`; what it can currently
reach is `Self.permissions`, which intersects that selection (or the anonymous role's
permissions, the other way its second gate is satisfied) with the owning user's role
through `FilterAllowed`. The two differ whenever the owner's role is narrowed after the
key was minted, and reporting the selection alone claimed authority the enforcer refuses.

## Resisting anonymous abuse

`self.login` and `self.register` are reachable without credentials by construction — they
are how anyone gets credentials — and both do bcrypt work. They are the two endpoints an
unauthenticated caller can aim at.

There was a third, and it was worse than either, because it needed no endpoint of its own:
presenting an API key hashed a caller-supplied secret before checking anything. See **Why
an API key's hash is fast** below for what that cost and what replaced it.

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

## Why an API key's hash is fast

API key secrets are stored as a plain SHA-256, with no salt, no pepper and no work
factor. For a password that would be indefensible. For this credential it is the correct
choice, and the reason is worth keeping, because the thing that makes it safe is not in
the hashing code.

**bcrypt was not slowing an attack down, it was the attack.** Measured on a Ryzen 5 2600,
2026-10-06: `bcrypt.CompareHashAndPassword` at cost 10 answers in **57.6 ms**, about 18
verifications per second per core. `api_key.Auth` runs that comparison **before any
authorization check**, on a secret an unauthenticated caller supplies, and `api_keys.id`
is a `serial` — so `1` is a valid key id. Anyone who could reach the port could encode id
1 with a random secret and spend 57 ms of server CPU per request, with no credential at
all; twelve concurrent requests saturated a twelve-core box. SHA-256 plus a constant-time
compare answers the same question in **118 ns**.

**A work factor only buys something when the input space can be enumerated.** That is
true of passwords and false here: the secret is `secretLength` bytes from `crypto/rand`,
96 bits, which nobody searches — salted, peppered or neither. An HMAC under a persisted
server key was considered and rejected for the same reason: it would have bought nothing
for this secret while costing a secret with its own generation, rotation and backup story,
whose loss invalidates every key at once. An in-memory cache of verified secrets was
rejected because it does not address the exhaustion path at all — a wrong secret is always
a cache miss, so the attack pays full price every time.

**What this moves, and where it is pinned.** bcrypt would still have protected a _weak_
secret. From here, safety rests entirely on `secretLength` and `crypto/rand` staying as
they are, and on no caller ever supplying a secret. That is not visible from the hashing
code, so it is pinned by a test — `TestSecretLengthIsPinnedBecauseTheHashIsFast` — whose
failure message points back at this section. Shortening the secret is a security change,
not a parameter change.

**Two hash formats are accepted, and the width tells them apart.** A bcrypt hash cannot be
turned into a SHA-256 of the same secret without the plaintext, and the plaintext exists
only in whatever the key's holder saved. So there is no migration: a legacy row is
rewritten the first time it verifies, which is the one moment the server legitimately
holds the secret. The discriminator is the stored width — 32 bytes is a digest, anything
else goes to bcrypt — and **not** bcrypt's `$2` prefix, which is the obvious check and is
wrong: a digest is 32 uniformly random bytes, so one in 65,536 of them begins with the
bytes `$2` and would be sent down the bcrypt path, permanently breaking that key.

**The rewrite runs before the expiry and enabled checks, deliberately.** Doing it after
them is tidier — no write on behalf of a credential being refused — and it was the first
version of this change, and it was wrong. Those are exactly the rows that would otherwise
go on paying 57 ms per attempt forever: an expired key an \*arr client keeps polling, the
keys of an account disabled today and enabled next week. The write grants nothing, because
it replaces a hash of a secret with another hash of the same secret, and the refusal that
follows is unaffected by it.

**What that leaves open, stated as the vector and not as a cost.** The rewrite needs a
_successful_ verification, and attack traffic never supplies one — an attacker varies the
secret, so every attempt misses. So for a key whose correct secret is never presented
again, **the original unauthenticated CPU-exhaustion path is intact**: the row keeps its
bcrypt hash, and anyone who aims at that id spends 57 ms of server CPU per request with no
credential. Since `api_keys.id` is a `serial`, the id an attacker tries first is `1`.

That is not "a dormant key costs its owner something". It is the whole vulnerability,
surviving against however many pre-change keys an instance never uses again — which for an
instance whose keys are all in active use is none, and for one with an abandoned key is
one. Active keys self-heal on first use, immediately for an \*arr client polling Torznab.
Which rows remain is a question nothing in the API answers, because `List` nils `Hash`
before returning; it is a `length(hash) = 60` query against the table, and
[docs/auth.md](../auth.md) gives it to the operator.

**The timing oracle this does _not_ close.** `repository.Get` runs before the comparison,
so response time still distinguishes "no such key id" from "wrong secret". The
decoy-comparison technique Login uses does not help here: under bcrypt the gap was 57 ms
of hashing, which a decoy hash could absorb, but what is left is the difference between a
`First()` that misses and one that hits and runs three preloads — milliseconds of database
work that no decoy comparison can equalise. What the oracle now reveals is how many API
keys exist, against ids that were already guessable, and the exhaustion path that made
knowing one worth anything is what this change removed. A decoy comparison here would look
like a control and be none.

Equalising it properly would mean a decoy _lookup_ — a second query shaped like the real
one, issued on the miss path — which doubles the database cost of every API-key request to
hide a count of how many keys exist, against ids that are sequential anyway. That is the
trade, and it is not worth making; it is written down here so the next reader reaches the
same answer without rediscovering it.

## First administrator

An `auth_initial_invitation` startup worker creates an admin invitation when no enabled
admin user exists, and logs its code — once, on creation. The unclaimed branch runs on
every boot until somebody claims it, so it logs a four-character suffix instead of the
credential. It is idempotent, and **the check and the insert are
serialized by a Postgres advisory lock** held for the transaction. bitmagnet is routinely
run as more than one process against one database; without the lock every replica reads
the same empty state and inserts its own code — a synchronized 16-replica start produced
16 distinct, non-expiring administrator invitations. It has to be a database lock rather
than a mutex, because the processes racing here do not share memory.

Because the code is logged only once, `user.Service` carries a second, **read-only**
method beside it: `GetInitialInvitation` answers "what is the outstanding code?" and never
writes. The two must not be confused — `CreateInitialInvitation` is a create-or-return, so
using it to answer that question would issue an administrator invitation on an instance
deliberately left without one. Both ask the same two questions of the database, through
the shared `enabledAdminConditions` and `bootstrapInvitationConditions` predicates, so the
read cannot drift onto a different invitation than the boot hook issued. The read needs no
advisory lock: the lock serializes a check followed by an insert, and there is no insert.

`internal/app/cmd/authcmd` exposes that read as `bitmagnet auth initial-invitation`. It is
a **command and not a GraphQL field on purpose**: the log line was proof of console
access, and serving the code over the API would hand a fresh instance to whoever asked
first. See [../auth.md](../auth.md) for the operator-facing description.

## Credentials

- **Session tokens** are JWTs. `jwt.Parse` pins HS256 rather than accepting whatever
  algorithm a token nominates, and checks the issuer it emits — which costs nothing while
  the signing key is unique to the instance, and everything when an operator reuses one
  across services.
- **Browser session cookies** carry the same JWT without returning it through GraphQL.
  `loginBrowser` reuses the User login path, then the authentication-owned cookie service
  writes the credential with the configured `__Secure-` name, `/graphql` path, and strict
  browser-only attributes. `logoutBrowser` expires the identical name and path.

  **Nothing in the bundled UI calls either.** `webui/src` authenticates with `self.login`
  and keeps the returned JWT in `localStorage`; the only reference to `loginBrowser` under
  `webui/src` is in the generated client. Do not read the cookie layer as the path in use
  — it was built for a separately served same-origin client, and it is exercised by its
  own tests rather than by the shipped UI. The Angular auth screens are transitional and
  are expected to be removed rather than migrated, which is why they were not pointed at
  this path. The operator-facing consequence is in `docs/auth.md` under Known gaps.

- **API keys** are `secret(12 random bytes) || uint32 id`, base62-encoded to 22 chars,
  stored as a plain SHA-256 of the secret — see **Why an API key's hash is fast** above.
  Two fixed defects are recorded in the comments: a decoded-length formula borrowed from
  base32 that rejected one key in 256, and a dropped hashing error that would have stored
  a zero hash as the credential.
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
