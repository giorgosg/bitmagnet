# Accounts in Magnes — what has to be built

Phase 1 ([plan.md](plan.md)) was the browser client alone: search, filters, sort,
infinite scroll, row expansion, `/torrent/<hash>`. **No mutations, no accounts** — and
`Bitmagnet.elm` says so in its module comment: read-only by construction, the generated
`Magnes.Api.Mutation` never imported.

This is the next phase. It is the first work that writes anything, and the first that has
a notion of who is asking.

Read [auth-api.md](auth-api.md) for the server's surface and
[bitmagnet-ui-audit.md](bitmagnet-ui-audit.md) for what the Angular UI already
established. This page is only the plan.

---

## A decision to take before any of it

**The README's argument for a Magnes proxy server no longer holds.** It says:

> Bitmagnet's API has no notion of users or authentication — anything that can reach it
> can do everything. Putting the server in the path gives one place to enforce access.

That was true of upstream. It is not true of this fork: `internal/auth` enforces every
GraphQL field against a permission model, and `auth.anonymous_access` is exactly the
"guests are a permission level, not a flag" idea the README describes, implemented
server-side and configured with one value.

Meanwhile the fork added `http_server.static.dir` (PR #48), whose stated purpose is
serving an alternative UI **from the API's own origin** — no CORS, credentials sent the
way a same-origin page sends them, and a single-page app's routing preserved by an
index.html fallback. That is the deployment the proxy was going to provide, minus the
proxy.

So there are two paths, and they lead to different codebases:

| | **Serve Magnes from bitmagnet** | **Build the Magnes server** |
| --- | --- | --- |
| Deployment | `http_server.static.dir`, working on a real instance today | Cloudflare Worker + D1, or similar |
| Auth | bitmagnet's, entirely | duplicated, or delegated anyway |
| Users | bitmagnet's `users` table | a second user store to reconcile |
| CORS | none | none |
| Deep links | handled by the static mount | handled by the Worker |
| What Magnes writes | Elm only | Elm plus a server plus a schema |

The second column buys things the first does not: a UI that can outlive this fork,
per-user state bitmagnet has no column for (saved searches, history), and an origin that
does not have to be bitmagnet's. Those may be worth it. But "bitmagnet has no auth" is no
longer the reason, and the plan below is written for the first column, because it is what
the fork now supports and what can be tested today. **Nothing in it is wasted if the
server arrives later** — the endpoint is already one runtime string, and every item below
is client-side.

Raise this with the user before writing a server.

---

## The three worlds a build has to survive

The same bundle can meet three servers, and they fail differently:

1. **No auth in the schema** — upstream, and any instance predating the auth port.
   `self` is not a field. Asking for it is a **document validation error that fails the whole request**,
   so identity must never be bundled into the search query. Magnes should degrade to
   phase-1 behaviour: no account UI, everything open.
2. **Anonymous access on** — `self.identity` resolves with `user: null` and near-total
   permissions. Login exists; nothing requires it.
3. **Anonymous access off** — an unauthenticated caller can reach `self`, `health` and
   `version`. Search itself is refused until login.

World 1 is detected by the validation error on a standalone identity query, worlds 2 and 3
by whether `permissions` contains `torrentContent::query`. Treat "no auth surface" as a
first-class state, not an error to display.

Do not assume one instance per world. Any instance built since the auth port is world 2
out of the box, and world 1 needs a build from *before* it — a local bitmagnet pinned to an
older tag is the reliable way to exercise that path, not an instance you expect to lag.
World 3 is world 2 plus `auth.anonymous_access: false`, which alters what every other
client of that instance can do, so ask before flipping it and pick an instance nothing else
depends on. `docs/environment.local.md` records which of these is reachable here.

---

## Work items, in order

### 1. Regenerate the API client against an auth-capable instance

`src/Magnes/Api/` is build output. Nothing below compiles until it contains `Self`, `User`,
`Role`, `APIKey`, `Invitation`, `AuthQuery` and `AuthMutation`, and today it contains none
of them — it was generated from the pre-auth instance.

Generate against an instance that **has the auth port** — see
[serving-and-testing.md](serving-and-testing.md), and
`docs/environment.local.md` for which one that is here:

```bash
BITMAGNET_URL=http://your-bitmagnet:3333 npm run codegen
npm run format
```

Be explicit about the URL rather than relying on the `http://localhost:3333` default.
**[verified]** an instance at `trunk` `77fdb9de7` introspects to 135 types including all
seven above; one predating the auth port returns 101 and none of them, and codegen against
it fails silently — you get a client, it just has no accounts in it. Check before
committing.

Commit the result — a checkout builds without reaching an instance, and that stays true.

### 2. Map the scalars that accounts need

`Magnes/Api/ScalarCodecs.elm` is generated *to be* edited and survives regeneration.
Accounts introduce `Duration` (API-key and invitation expiry), which is a **Go duration
string** — `"24h0m0s"` — not seconds and not ISO 8601. `DateTime` is already used by the
row model; `createdAt`, `lastLoginAt` and `expiresAt` need the same treatment.

### 3. A place for the token, which means a port

Elm cannot read `localStorage`. The token crosses the boundary twice:

- **In, at startup**, as a flag alongside `apiUrl` — so the first request is already
  authenticated and there is no unauthenticated flash.
- **Out, on login and logout**, through an outgoing port that writes or clears it.

Subscribe to the `storage` event in `index.html` and feed it back through an incoming
port. That is what makes a second tab logging out take effect here, and it costs nothing —
the Angular UI re-reads storage on a 10-second timer to get the same result.

Keep the key namespaced to Magnes, not `bitmagnet-jwt`: two UIs served from one origin
would otherwise share one slot.

### 4. One place that builds a request

`Bitmagnet.elm` currently exposes selection sets and `Main.elm` calls
`Graphql.Http.queryRequest` at each site. Adding a header at each site is how one gets
forgotten. Introduce a single pair of functions — query and mutation — that take the
endpoint, the token and the selection set, apply `withHeader "Authorization" ("Bearer " ++ t)`
when there is one, and are the only way a request is built.

This is also where the module comment changes: `Magnes.Api.Mutation` gets imported for the
first time, and "read-only by construction" stops being true. Say what replaced it.

### 5. Session as a type, and the stale-token check

```elm
type Session
    = Unknown            -- before the first identity response
    | NoAuthSurface      -- world 1: the server has no accounts
    | Anonymous (List ObjectAction)
    | SignedIn { user : User, permissions : List ObjectAction }
```

`Self.permissions` is `[AuthObjectAction!]!` while `LoginResult.permissions` is
`[Permission!]!` — the same information in two shapes. Normalise to the flat one at the
boundary.

**The check that is not optional:** if a token is held and the response reports
`user: null`, the token is dead — clear it. A revoked, expired or deleted credential
produces *no error*; it falls through to the anonymous identity, deliberately, so that
`self.login` stays reachable to recover with. The mismatch is the only signal there is.
See [auth-api.md](auth-api.md) for why the server is built that way.

Refresh identity on load, on window focus, and after any `unauthorized` error. Not on a
timer.

### 6. An enforcer, glob-aware

Permissions match by glob. The `admin` role's only permission is `**/**/**`, so an
equality check denies an administrator everything. The server emits only `**` and literals
today, so a full glob implementation is not needed — but a comment saying so is, or the
next pattern will silently fail closed.

```elm
type alias ObjectAction = { namespace : String, object : String, action : String }

can : ObjectAction -> Session -> Bool
```

Default the namespace to `graphql` at call sites; the `http` namespace exists in
`listObjectActions` but nothing in a browser reaches it.

Enforcement here is **presentation, not protection** — the server refuses regardless. The
point is that a user is not shown a screen whose every query will be denied.

### 7. Routes and guards

New routes, following the existing "real paths, not fragments" rule in `Route.elm`:

```
/login          ?returnUrl=<path>
/register       ?code=<invitation>
/account
/account/api-keys
/admin/users
/admin/roles
/admin/invitations
```

`toHref` is the inverse of the parser and the only way links are built — adding a variant
breaks it at compile time, which is the property to preserve.

Guards are a function of `Session` evaluated in `update` on route change, not a wrapper
type: `/login` and `/register` redirect to `/account` when signed in; the account routes
redirect to `/login?returnUrl=…` when not; the admin routes require the matching object
action and otherwise render a refusal rather than redirecting — a signed-in user sent to a
login form they do not need is worse than being told no.

Accepting `?code=` on `/register` makes an invitation a link, which is what an invitation
should be. The Angular UI does not do this.

### 8. The screens

In dependency order, each usable before the next exists:

1. **Login** — username, password, error line. Honour `returnUrl`, navigate with
   `replaceUrl`. Handle `too many login requests` as its own state: it is a wait, not a
   wrong password.
2. **Account** — who you are, your role, when you last signed in, sign out.
3. **API keys** — list, create, **delete**. The secret is shown once, at creation, and is
   never retrievable; the UI has to be emphatic about that. Scoping a key means the object
   action list from `listObjectActions`.
4. **Register** — username, password, email, invitation code. Entropy meter from
   `self.passwordEntropy`, **debounced** — it is a server round trip per keystroke as the
   Angular UI wrote it.
5. **Users admin** — list with `usernameLike` search, change role, enable/disable, delete.
   Every one of those mutations is unbuilt in the Angular UI, so there is nothing to port.
6. **Invitations admin** — list, create with role and expiry, delete. Show the code as a
   `/register?code=…` link.
7. **Roles admin** — list, create, edit, delete. **`putRole` replaces the entire
   permission set**: load the role's current permissions first, or an edit revokes
   everything the form did not list.

Sign-out is a client-side act: clear the token, refetch identity. There is no logout
mutation and the JWT stays valid until it expires.

### 9. Errors, which are strings

**There are no error codes and no extensions at all** — including on `unauthorized`, whose
extensions are dead code in the fork ([auth-api.md](auth-api.md) has the diagnosis). Every
error is a wrapped message chain plus a `path` naming the top-level field. Match on
substring against the table in [auth-api.md](auth-api.md), keep the mapping in one place,
and use `path` to tell an authorization refusal apart from a failure of the same call.

If the extensions get fixed in the fork — a one-line rename — this becomes a switch on
`{namespace, object, action}` instead. Write the mapping so that swap is local.

`GraphqlError` can carry partial data. For account operations, treat any error as total
failure; a half-applied permission change is not something to render optimistically.

---

## What Magnes cannot fix from here

Three gaps need a change in bitmagnet, and the fork is ours, so they are workable — just
not in this repository:

1. **No password-change mutation.** `user.Service.UpdatePassword` exists and is called
   from nowhere: no resolver, no schema field. A user cannot change their own password
   through any API. Needs a `SelfMutation.updatePassword` field.
2. **`User` has no `enabled` field.** `setUserEnabled` exists, the column exists, and the
   type does not expose it — so a users table cannot show who is disabled, or reflect the
   result of disabling someone.
3. **`auth.email_verification` is inert.** Documented as such in bitmagnet's own
   `docs/auth.md`. Do not build UI that implies an address was verified.

Until (1) lands, the account screen has no password section. Do not draw a disabled one.
