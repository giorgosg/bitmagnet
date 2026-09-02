# The account and auth API

Everything Magnes needs to know about bitmagnet's authentication surface. Transcribed
from `../bitmagnet/graphql/schema/*.graphqls` and `../bitmagnet/internal/auth/` at `trunk`
`77fdb9de7`, and re-checked on 2026-08-24 against a live instance running that exact
commit. See [serving-and-testing.md](serving-and-testing.md) for what an instance needs to
have before any of this is reachable.

**[verified]** marks a claim checked by request against that instance. Everything behind a
credential — registration, login, API keys, the whole `auth` namespace — is read off the
source only, because the probes were anonymous.

Vocabulary is fixed in `../bitmagnet/CONTEXT.md`. Identity, User, API key, Invitation,
Object action, Permission, Role, Anonymous access. Do not write "guest", "session",
"scope" or "account" in code or comments.

## The shape of it

`auth.anonymous_access` defaults to `true`, granting the `anon` role every registered
object action except auth administration. Magnes supports both configurations of the
fork:

1. **Anonymous access on** — `self.identity` returns `user: null` with the anonymous
   permissions. Login exists but search does not require it.
2. **Anonymous access off** — an unauthenticated caller reaches `self`, `health`, and
   `version`; search requires a User.

## Types

```graphql
type Self {
  user: User            # null for anonymous; the owning User for an API-key identity
  apiKey: APIKey        # non-null only when the credential was an API key
  permissions: [AuthObjectAction!]!
}

type User {
  id: Int!
  username: String!
  role: String!         # a role NAME, not an object
  email: String
  lastLoginAt: DateTime
  createdAt: DateTime!
  updatedAt: DateTime!
}

type Role {
  name: String!
  core: Boolean!        # admin/editor/user/anon are core and cannot be deleted
  permissions: [Permission!]!
}

type AuthObjectAction { namespace: String!  object: String!  action: String! }
type AuthSubject      { type: AuthSubjectType!  name: String! }   # type is only `role`
type Permission       { subject: AuthSubject!  objectAction: AuthObjectAction!  core: Boolean! }

type APIKey {
  id: Int!  name: String!  userId: Int!  user: User!
  expiresAt: DateTime  createdAt: DateTime!
}

type Invitation {
  code: String!  role: String!  email: String
  createdBy: User  claimedBy: User
  expiresAt: DateTime  createdAt: DateTime!
}
```

**`User` has no `enabled` field**, even though `setUserEnabled` exists and the column does.
A UI therefore cannot show whether an account is disabled, or reflect the result of
disabling one. Closing that needs a schema change in bitmagnet.

## Queries

```graphql
query {
  self {
    identity: Self!
    passwordEntropy(password: String!): PasswordEntropyResult!   # { entropy, minEntropy, valid }
    apiKeys: [APIKey!]!
  }
  auth {
    listUsers(input: ListUsersInput): ListUsersResult!           # { users, totalCount }
    listRoles: [Role!]!
    listObjectActions: [AuthObjectAction!]!
    listInvitations(input: ListInvitationsInput): ListInvitationsResult!
  }
}

input ListUsersInput       { pagination: PaginationInput  usernameLike: String }
input ListInvitationsInput { pagination: PaginationInput }
input PaginationInput      { limit: Int  page: Int  offset: Int }
```

`passwordEntropy` is a **server round trip per keystroke** if used the way the Angular UI
uses it. Debounce it. **[verified]** it answers anonymously, and reports `minEntropy: 70`
— the default — so the meter can be drawn before anyone has logged in.

## Mutations

```graphql
mutation {
  self {
    register(input: RegisterInput!): RegisterResult!    # { user }
    login(username: String!, password: String!): LoginResult!   # non-browser: returns the token
    loginBrowser(username: String!, password: String!): Void   # sets the HttpOnly cookie
    logoutBrowser: Void                                        # clears it
    createAPIKey(input: CreateAPIKeyInput!): CreateAPIKeyResult!
    deleteAPIKey(id: Int!): Void
  }
  auth {
    setUserRole(userId: Int!, roleName: String!): User!
    setUserEnabled(userId: Int!, enabled: Boolean!): User!
    deleteUser(userId: Int!): Void
    putRole(role: String!, objectActions: [AuthObjectActionInput!]!): Role!
    deleteRole(role: String!): Void
    invite(input: InviteInput!): Invitation!
    deleteInvitation(code: String!): Void
  }
}

input RegisterInput    { invitationCode: String  username: String!  password: String!  email: String }
input CreateAPIKeyInput{ name: String!  permissions: [AuthObjectActionInput!]!  expiry: Duration }
input InviteInput      { email: String  role: String  expiry: Duration }

type LoginResult       { token: String!  user: User!  permissions: [Permission!]! }
type CreateAPIKeyResult{ id: Int!  apiKey: String!  name: String!  expiresAt: DateTime }
```

**There is no password-change mutation.** `user.Service.UpdatePassword` is implemented in
`../bitmagnet/internal/auth/user/method_update_password.go` and called from nowhere — no
resolver, no schema field. A user cannot change their own password through any API.
Closing that needs a schema change in bitmagnet.

`putRole` is a **replace, not a merge**: the object actions given become the role's entire
permission set. Read the role first, or an edit silently revokes everything unlisted. It
also **upserts on the name**, so there is no rename: saving a role under a different name
leaves the original where it was and creates a second beside it.

`Role.permissions` merges two sources, and `Permission.core` is what tells them apart. A
core permission is held **in memory** by `mergeCoreRolePermissions` — admin's `**/**/**`
and the `anon`/`user` baseline — and has no row in `role_permissions`. `putRole` writes
only the stored set, so it can neither grant nor revoke a core permission, and sending one
back merely writes a row for something that was already answering. The schema reports one
flag for both, so a stored row that duplicates a core permission cannot be told apart from
the core permission alone.

**Deleting a role is three foreign keys**, none of them visible in the GraphQL schema
(`migrations/00022_auth.sql`):

| Table | Reference | What deleting a role does |
| --- | --- | --- |
| `role_permissions` | `on delete cascade` | its permissions go with it |
| `invitations` | `on delete cascade` | **every invitation issued for it is deleted**, claimed or not |
| `users` | no cascade | Postgres **refuses** the delete while any user holds the role |

So a role in use cannot be deleted, and the refusal arrives as an opaque database error
rather than a coded one; and a role nobody holds can still take invitations with it
silently. The cascade draws no claimed/unclaimed distinction — `invitations.claimed_by` is
just another column — so the record of invitations already used goes as well. `deleteRole` additionally refuses the four core role names before touching the
database at all (`rbac.service.DeleteRole`).

`Duration` is gqlgen's `graphql.Duration` — a Go duration string, `"24h0m0s"`, parsed with
`time.ParseDuration`. Not seconds, not ISO 8601.

## Two shapes for the same idea

`Self.permissions` is `[AuthObjectAction!]!` — flat triples. `LoginResult.permissions` is
`[Permission!]!` — triples wrapped with a subject and a `core` flag. The same information
arrives in two shapes depending on which call produced it. Normalise to the flat form at
the boundary; the subject is always the caller's own role and carries nothing.

## Authorization

An **object action** is `namespace/object/action`. Three namespaces exist, seventeen
actions in total:

- `graphql` (13) — derived from the `@auth` directives in the schema, not listed anywhere
  by hand: `self::query`, `self::mutate`, `auth::query`, `auth::mutate`, `version::query`,
  `health::query`, `workers::query`, `queue::query`, `queue::mutate`, `torrent::query`,
  `torrent::mutate`, `torrent::delete`, `torrentContent::query`.
- `http` (3) — `import::mutate`, `pprof::query`, `metrics::query`, for the non-GraphQL
  endpoints.
- `torznab` (1) — `torznab::query`, registered by
  `../bitmagnet/internal/torznab/httpserver/auth.go`.

Only the `graphql` ones are reachable from a browser, but **all seventeen appear in
`listObjectActions`**, and therefore in any role editor or API-key scoping form. Scoping a
key to Torznab and nothing else is the case that makes the last one matter.

**[verified]** on a live instance the anonymous identity holds exactly 15 of the 17 — every
one except `graphql::auth::query` and `graphql::auth::mutate`, and `{auth{…}}` accordingly
returns `"unauthorized"`. That is the deliberate exclusion below, confirmed in the field.

Enforcement is per **top-level field**. `Mutation.torrent` is gated by
`torrent::mutate`, and every field beneath it inherits that, with `delete` additionally
gated by `torrent::delete`. There is no per-argument or per-row authorization.

### Roles

`admin`, `editor`, `user`, `anon` are core and seeded by migration `00022_auth.sql`, with
**no permissions rows**. What each actually gets:

| Role     | Where its permissions come from                                                     |
| -------- | ------------------------------------------------------------------------------------ |
| `admin`  | An in-memory core permission of `**/**/**`. Everything, always.                       |
| `user`   | The baseline below, plus `torrent::query` and `torrentContent::query`.                |
| `anon`   | The baseline below; plus everything except `auth::*` while anonymous access is on.    |
| `editor` | **Nothing.** It is a name with no grants until an admin uses `putRole`.               |

The baseline granted to `anon` and `user` regardless of the anonymous-access setting is
`self::query`, `self::mutate`, `health::query`, `version::query` — because logging in is
itself a GraphQL mutation, and without it enabling authentication is a permanent lockout.

Permissions match by **glob**, not equality: admin's `**` is a pattern. Server-side this
is casbin with `globMatch` over three fields — the subject as `role::<name>`, the object as
`<namespace>::<object>`, and the action on its own
(`../bitmagnet/internal/auth/rbac/service.go`, `casbin_model.conf`). A client only ever
sees the flat triple, so it matches component by component.

**A client-side enforcer has to glob-match too, or it will deny an admin everything** —
admin's sole permission is `**/**/**`. The Angular UI uses picomatch for this; an Elm
implementation needs the equivalent, and the only patterns the server actually emits today
are `**` and literals.

Revocation takes up to `auth.rbac_cache_ttl` (default 1 minute) to take effect.

## Credentials

**JWT**: sent as `Authorization: Bearer <token>`. HS256, pinned. Its
lifetime is `auth.jwt_duration`, default 24h. If `auth.jwt_secret` is unset the server
generates one per process, so **every restart invalidates every token** — expect this
constantly in development.

**API key**: 22 base62 characters, sent as `?apikey=` or `X-Api-Key`, and used by \*arr
clients over Torznab. An API-key Identity reports both the key and its owning User, but it
is not a User-authenticated Identity and cannot use operations that require one. Torznab
**refuses a JWT** in the apikey slot and ignores the bearer middleware entirely, so a
browser credential is never a Torznab credential. A UI creates and deletes keys; it never
authenticates with one.

The current API-key enforcement first requires the owning User's Role to permit an Object
action, then requires either the key's stored action list or the Anonymous identity to
permit it. Disabling the owning User makes the key unusable. Two current contract gaps are
being corrected as part of the Magnes work: creation accepts unregistered and wildcard
action strings without validation, and `Self.permissions` concatenates the stored and
Anonymous actions without intersecting them with the owning User's Role, so it can report
an action that enforcement denies. The `APIKey` GraphQL type also does not yet expose its
stored or effective actions.

**Invitation**: a single-use 128-bit code. `auth.invitation_required` defaults to `true`,
so registration normally needs one. The first administrator's invitation is minted by a
startup worker and **logged, not displayed** — an operator reads it out of the journal.

## The failure mode that decides the client design

The authenticator chain is JWT → API key → anonymous, and its invariant is:

> A revoked, expired, unparseable, deleted or disabled credential reports **no match** and
> falls through to anonymous. Only an infrastructure failure reports an error.

So **a dead token does not produce an error.** It produces a successful `self.identity`
with `user: null`. **[verified]** both halves: `Authorization: Bearer not.a.jwt` and a
well-formed JWT with a bogus signature each returned `200` with the full anonymous
identity and no error at all. That is deliberate — a credential path that aborted the chain left the
request with no identity at all, which refused `self.identity` and `self.login`, the two
calls a client needs to notice its token is dead and recover. The identity then stayed
wedged across reloads until the operator cleared browser storage by hand.

**The client-side consequence, and it is not optional:** holding a token while the server
reports `user: null` means the token is stale. Detect that and clear it. Nothing else will
tell you.

## Anonymous abuse controls a client will hit

- **Login is throttled and refuses rather than queues.** Buckets are keyed by
  `(account, source)` and by source alone. An attempt that cannot be served immediately is
  refused immediately with `too many login requests`. Defaults are 30/minute, burst 5.
- **Registration validates the invitation before hashing.** A bad code fails fast; a good
  one costs a bcrypt.
- **Login compares against a decoy hash when the account does not exist**, so timing does
  not disclose whether a username is taken. Do not build a "username available?" check on
  top of login latency.

## Error codes

Errors arrive as ordinary GraphQL errors carrying a stable `extensions.code`, emitted by
the `errorPresenter` in `../bitmagnet/internal/gql/httpserver/error_presenter.go`. The code
is the contract; the message is presentation and may be reworded. **Never match on message
text.**

An authorization refusal also carries the refused Object action **[verified 2026-08-24
against the homeserver on trunk]**:

```json
{"errors":[{"message":"unauthorized","path":["auth"],"locations":[...],
  "extensions":{"code":"UNAUTHORIZED","namespace":"graphql","object":"auth","action":"query"}}],
 "data":null}
```

`path` still names the top-level field (`["auth"]`, `["self","login"]`) and remains useful
for telling which of several fields failed, but it is no longer the only signal.

| Code | Means |
| ---- | ----- |
| `INVALID_CREDENTIALS` | Login miss — covers "no such user" too |
| `USER_DISABLED` | Login against a disabled User |
| `LOGIN_THROTTLED` | Throttled; back off, do not retry immediately |
| `USER_ALREADY_EXISTS` | Registration, username or email taken |
| `USERNAME_INVALID` | Registration validation |
| `INVITATION_REQUIRED` | `invitation_required` is on and none was given |
| `INVITATION_INVALID` | Registration, no such code |
| `INVITATION_EXPIRED` | Registration, code past its expiry |
| `INVITATION_CLAIMED` | Registration, code already used |
| `PASSWORD_INSUFFICIENT_ENTROPY` | Below `auth.password_min_entropy` (default 70) |
| `EMAIL_REQUIRED` | `auth.email_required` is on and no address was given |
| `EMAIL_INVALID` | Registration or invitation, malformed address |
| `UNAUTHORIZED` | Refusal; carries `namespace`, `object`, `action` |
| `USER_SESSION_REQUIRED` | Operation needs a User-authenticated Identity |
| `API_KEY_MANAGEMENT_FORBIDDEN` | An API-key Identity may not manage API keys |
| `AUTHENTICATION_INFRASTRUCTURE_FAILURE` | Authentication service unavailable |
| `INTERNAL_SERVER_ERROR` | Unknown internal error; details deliberately withheld |

Codes without a case in `src/ApiError.elm` fall back to the server's own message, so a
newer bitmagnet stays legible without a client change.

The two email codes are read from `error_presenter.go` and its
`auth_error_codes_integration_test.go`, **not observed live** — Magnes collects no email
address, so it can provoke neither. `EMAIL_REQUIRED` is mapped anyway, because an instance
with `auth.email_required` on (it defaults off) cannot be registered with from Magnes at
all, and saying which setting is in the way beats a bare refusal. `EMAIL_INVALID` is
deliberately left unmapped: Magnes sends no address that could be malformed, so a case for
it would be unreachable.
