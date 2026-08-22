# Porting the `next` auth stack

How `internal/auth` got from `upstream/next` onto the `main` lineage: what the dependency
surface actually measured, where `next`'s design had to be adapted, what review found, and
which alternatives were weighed. [auth.md](auth.md) describes the result; this page is the
making of it.

Reach for it when porting more of `next` — the measurement method is reusable and the
adaptations name the seams — or when reviewing any security-sensitive port, because the
findings below are mostly not about auth.

## Dependency surface

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
earlier version of this record claimed auth could not be extracted without taking `next`
wholesale; that is not so at import level.

Walking `next`'s own copies of `internal/database` and `internal/model` instead of
`trunk`'s does drag in `wasm`, `proto` and the plugin registry — an artefact of measuring
the wrong thing, since those packages already exist on `main`. **Measure against the
lineage you are porting _to_.**

New third-party modules: **casbin/v2**, **golang-jwt/v5**, **go-password-validator** on
the Go side, **picomatch** in the web UI. Everything else the support packages need was
already present, including `gin`.

The migration is `migrations/00022_auth.sql` — six tables (`roles`, `role_permissions`,
`users`, `invitations`, `api_keys`, `api_key_permissions`) seeding four core roles, and
renumbered from `next`'s `00021`, which collides with `00021_queue_jobs_fetch_index.sql`.
The model and dao types are **gorm-gen output**: `internal/database/gen/gen.go` sets
`ModelPkgPath` to `internal/model`, so applying the migration and running `task gen-gorm`
produces them. They came out byte-identical to `next`'s, which is the strongest evidence
the extraction is faithful.

## Adaptations

Where `next`'s design depends on infrastructure this lineage does not have:

- **fx instead of the plugin builder.** `next` assembles auth through
  `internal/plugin/core/auth/plugin.go`, which needs its plugin registry, worker runner
  and ref system. `authfx` is written directly against fx, mirroring the same service set,
  value groups and the lazy indirection that breaks the rbac dependency cycle.
- **`database.DaoTransactionProvider`.** On `next` this belongs to a provider abstraction
  built on its worker-runner lifecycle. `internal/database/provider.go` adapts the
  lineage's own `lazy.Lazy[*dao.Query]` to the same two-method surface, so the seam is in
  the same place and a future switch to `next`'s provider would not touch `internal/auth`.
- **Config as a struct.** `next` declares eleven parameters through its plugin config
  builder; `authconfig.Config` expresses them for `configfx` and converts them to the
  atomic values the services take. The builder also carried validity constraints that a
  plain struct does not — see the `validate:` tags in [auth.md](auth.md#configuration).
- **Apollo Client v3, not v4.** `query` types `data` as non-null while `mutate` does not,
  so `next`'s assertions are noise on the former and load-bearing on the latter. Its
  `CombinedGraphQLErrors` handling and `dataState`-based `filterComplete` have no v3
  equivalent and were rewritten.
- **Anonymous access centrally, not per plugin.** `next` has each plugin grant its own
  object actions to `anon`; `authconfig.AnonymousPermissions` does it in one place.

## Lint policy

**This branch** adopts `next`'s `var-naming` `skipPackageNameChecks` relaxation, needed
for underscored package names such as `json_schema`; `trunk` still carries a bare
`var-naming`, so the relaxation arrives with the merge rather than preceding it. A
path-scoped exclusion covering only `pkg/json_schema` would have been tighter, and was
weighed and passed over: the relaxation is already committed and commented, and churning
it buys nothing. `next` also switches `wsl` to `wsl_v5`
and drops the `if-return` and `nested-structs` revive rules; **`wsl_v5` does not exist in
golangci-lint v2.1.6**, which both repositories pin, and `golangci-lint config verify`
rejects it — so that part cannot be adopted and the affected code was fixed by hand.

**A version bump is never confined to `go.mod`.** Raising the module to Go 1.24 (for the
`crypto/rand` guarantee, see [auth.md](auth.md#secret-material)) switched on the
`usetesting` rule, which requires `t.Context()` as soon as the module targets 1.24. That
was 50 call sites across 29 files, and it is the whole reason the first CI run on this
branch went red. The `context.Background()` substituted throughout the ported tests is now
`t.Context()`, which cancels with the test rather than outliving it — retiring this
record's former note that `next`'s use of `testing.T.Context` was unavailable here.

## Defects the port itself surfaced

Both were found by porting rather than review, and both landed with a test watched
failing — **red** — before the fix:

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

## What review found

Everything above compiled, passed `go vet`, and passed the whole suite before any of the
below was known. **That is the argument for the repo's test-first rule**, and each of
these landed with a test watched failing against the unfixed code first. The mechanics of
each fix live in [auth.md](auth.md); this is the index, in the order the fixes landed.

**Authorization was missing rather than wrong.**

| Defect                                               | Effect                                                    |
| ---------------------------------------------------- | --------------------------------------------------------- |
| GraphQL surface was unenforced                       | every field reachable regardless of the `@auth` directive |
| `/import`, `/metrics`, `/debug/pprof` were unguarded | a data-mutating importer and `pprof/cmdline` left open    |
| `anon` was granted auth administration               | a trapdoor, not merely an open door — see below           |
| Torznab trusted the ambient bearer identity          | a browser session authenticated an API-key-only endpoint  |
| API key could mint a second key naming any action    | scope escalation to the owner's full role                 |

**Credentials did not mean what they said.**

| Defect                                        | Effect                                                     |
| --------------------------------------------- | ---------------------------------------------------------- |
| Invitation expiry comparison inverted         | expired invitations accepted, valid ones refused           |
| Invitation claimed by an unconditional update | concurrent registrations all claimed the same invitation   |
| Disabling a user revoked nothing              | existing tokens and API keys worked until they expired     |
| Bootstrap did an unlocked check-then-insert   | 16 replicas produced 16 permanent administrator codes      |
| `SetRole` updated without a `WHERE` clause    | gorm's global-update guard was the only thing stopping it  |
| Roughly one API key in 256 could not decode   | `decodedLength` used the base32 formula on a 16-byte value |

**Disclosure and abuse.**

| Defect                                     | Effect                                          |
| ------------------------------------------ | ----------------------------------------------- |
| Login distinguished missing from wrong     | username enumeration, by message and by timing  |
| Torznab keys were logged verbatim          | log read access equalled application access     |
| Registration hashed before validating      | one bcrypt per anonymous request, in parallel   |
| Login limiter was process-wide, and waited | five wrong guesses locked out every account     |
| Revoked tokens aborted the chain           | UI wedged, unable to reach the call clearing it |

A **second pass over the fixed branch** found five more:

| Defect                                           | Effect                                               |
| ------------------------------------------------ | ---------------------------------------------------- |
| Login throttle keyed on a spoofable client IP    | rotating `X-Forwarded-For` bought unlimited guesses  |
| Revoked API keys still aborted the chain         | the always-resolves invariant held for JWTs only     |
| `UpdatePassword` ignored `password_hashing_cost` | raising the cost silently did not apply to changes   |
| `NewSecret` discarded its bcrypt error           | a zero-valued hash would be stored as the credential |
| Invitation codes were 48 bits                    | thin for a non-expiring credential that grants admin |

A **third pass** found five more:

| Defect                                     | Effect                                                     |
| ------------------------------------------ | ---------------------------------------------------------- |
| `gin.Recovery` dumped the raw request line | the redaction guarantee did not cover the panic path       |
| Config validation was dropped in the port  | `login_requests_per_minute: 0` panics; entropy `0` is mute |
| `GenerateRandomString` discarded its error | an entropy failure minted an all-zero secret before 1.24   |
| `self.apiKeys` accepted an API key         | a Torznab-scoped key enumerated its owner's other keys     |
| The JWT issuer was emitted, never checked  | a reused secret let another service's token be accepted    |

Those last two are the same mistake — a check written in one place and not the
neighbouring one. `CreateAPIKey` and `DeleteAPIKey` require an interactive session through
`UserSessionFromContext`, while `APIKeys` asked only for a user, so listing was the one
key-management operation a machine credential could reach. `Generate` set
`Issuer: "bitmagnet"` and `Parse` never looked at it, which costs nothing while the signing
key is unique to the instance and everything when an operator reuses one across services.

Also removed: `rbac.repository.DeleteRolePermissions`, dead on arrival — absent from the
`Repository` interface, called by nothing, and carrying a copy-paste bug that compared the
object column against the namespace. `jwt.Parse` now pins HS256 rather than accepting
whatever algorithm a token nominates; not exploitable against a symmetric key, but the
parser built for exactly that purpose was being constructed and then ignored.

## The four worth learning from

In each of these the security control worked and something around it did not — the failure
mode least likely to be caught by a test written after the fix, and the reason review
passes kept finding more.

**The anonymous-access trapdoor was the worst.** Granting `anon` every registered object
action included auth administration, so an unauthenticated caller could hand the `anon`
role a wildcard through `putRole`. Role grants live in the database while the
compatibility grant is only in memory, so the wildcard **survived setting
`anonymous_access` to `false`**: the switch documented as "this is how you turn
authentication on" left the instance wide open with nothing visible to show for it. A
compatibility default has to be a floor, not a ceiling — anything it grants that can
rewrite the permission model is not a default, it is a bypass.

**The chain-abort lockout and the login limiter were failures of the recovery path.** In
each the control did its job and made the way back out unreachable: a dead token refused
the query that would have cleared it, and a shared login budget refused the login that
would have replaced it. When designing a refusal, ask what the legitimate user does next
and check that path is still open.

**The spoofable throttle key was introduced by a fix**, which is why it is the one to
learn from. Replacing the global limiter with keyed buckets was right, and the keys were
chosen carefully — the reasoning about which bucket an attacker can fill on someone else's
behalf still stands. What went unexamined was whether the attacker controls the key
itself, and they did: the value came from a framework whose default is to believe a
request header. **A control is only as good as the least trustworthy input to it**, and
that input was two dependencies away from the code doing the reasoning.

**And the panic path was a second sink.** Redacting the request logger did not cover
`gin.Recovery`, which dumps the request line verbatim — in release mode too, on exactly
the broken-pipe branch a Torznab client reaches by disconnecting mid-response. A redaction
guarantee holds only over the sinks it was applied to.

## Alternatives considered

### `upstream/next` — chosen

The maintainer's design, on the dormant
[`next` branch](upstream-status.md#the-next-branch). Most complete, most likely to match
upstream's eventual direction, and the only one with a real permission model. Still a
draft: `next` HEAD is 2026-01-31 and `internal/auth/` was last touched 2026-01-24
(`93dd4c623`).

### `gabriel20xx` (`internal/auth`, 5 files)

Single-user session auth on the `main` lineage: `auth_users` (bcrypt, singleton unique
index) and `auth_sessions` (sha256 token hash, expiry, cascade delete), with an
`@authenticated` directive. Small enough to read end to end, and its first-run flow suits
a self-hosted app — but **no API key support**, so no authenticated Torznab, and no
permission model. Not used. See [forks/gabriel20xx.md](forks/gabriel20xx.md).

### `kawaii-not-kawaii` (`internal/gql/auth`) — contributed the Torznab design

An independent implementation on the `main` lineage: first-run setup through the fork's
config writer, signed browser sessions, a machine API key accepted by GraphQL and Torznab,
and a trusted-network bypass. Coupled to that fork's live config reader/writer and stores
credentials in application config rather than database tables, so it was not a design
basis — but it is the only fork that wired auth into Torznab, and that part was adapted
here with attribution (`172a784d3`). See
[forks/kawaii-not-kawaii.md](forks/kawaii-not-kawaii.md).

### Build a focused implementation

Rejected: security-sensitive code written from scratch, diverging from upstream if `next`
lands.

### Front it with a proxy

Still a valid deployment option, and the least work: do auth at the gateway and bind
bitmagnet to localhost. It leaves Torznab unauthenticated unless the proxy covers that
too, and it does not give per-key scoping.
