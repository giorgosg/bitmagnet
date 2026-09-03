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

## Deployment decision

Build this phase in Elm against the fork's identity and permission model, and sign it off
from bitmagnet's same-origin `http_server.static` mount. A separate Magnes server is a new
architecture branch, justified only by independently hosted state or origin requirements;
raise that decision before adding one. Every work item below remains client-side.

---

## Supported configurations

[auth-api.md](auth-api.md#the-shape-of-it) defines the fork's anonymous-access-on and
anonymous-access-off configurations. This phase is complete when one bundle behaves
correctly in both; [serving-and-testing.md](serving-and-testing.md) and
`docs/environment.local.md` identify instances for exercising them.

---

## Work items, in order

### 1. Regenerate the API client against an auth-capable instance

`src/Magnes/Api/` is build output. Nothing below compiles until it contains `Self`, `User`,
`Role`, `APIKey`, `Invitation`, `AuthQuery` and `AuthMutation`, and today it contains none
of them.

Generate against an instance running the target fork — see
[serving-and-testing.md](serving-and-testing.md), and
`docs/environment.local.md` for which one that is here:

```bash
BITMAGNET_URL=http://your-bitmagnet:3333 npm run codegen
npm run format
```

Be explicit about the URL rather than relying on the `http://localhost:3333` default, and
turn `graphql.introspection` on at the instance first — it defaults to `false`, and codegen
cannot read the schema without it. See
[serving-and-testing.md](serving-and-testing.md#regenerating-the-client).
**[verified]** `trunk` `77fdb9de7` introspects to 135 types including all seven above.
Check for them before committing; codegen can succeed against the wrong schema while
producing a client without the required surface.

Commit the result — a checkout builds without reaching an instance, and that stays true.

### 2. Map the scalars that accounts need

`Magnes/Api/ScalarCodecs.elm` is generated *to be* edited and survives regeneration.
Accounts introduce `Duration` (API-key and invitation expiry), which is a **Go duration
string** — `"24h0m0s"` — not seconds and not ISO 8601. `DateTime` is already used by the
row model; `createdAt`, `lastLoginAt` and `expiresAt` need the same treatment.

### 3. A place for the token — superseded

**This item was replaced and is kept only so the change is legible.** It planned to hold
the JWT in `localStorage` and move it across a port on login and logout, the way the
Angular UI does.

[ADR 0005](adr/0005-use-an-http-only-cookie-for-browser-authentication.md) settled on an
HttpOnly cookie instead: `loginBrowser` and `logoutBrowser` set and clear it server-side,
and **Magnes never sees the credential at all**. There is nothing to persist, nothing to
attach to a request, and nothing for a cross-site script to read.

What survives of the plan is the tab synchronization, minus the credential. A
`BroadcastChannel` message says only "authentication changed"; each tab then asks
`self.identity` what it may now do. `public/index.html` carries that pair of ports.

One port does carry a password, in one direction, once: after a successful registration or
sign-in, Magnes offers the username and the password just typed to
`navigator.credentials.store`, because Elm's `onSubmit` prevents the default and no
password manager would otherwise see a submission to offer saving on. That is the person's
own password on its way out of the model, not bitmagnet's credential. See ticket 17.

### 4. One place that builds a request

`Bitmagnet.elm` currently exposes selection sets and `Main.elm` calls
`Graphql.Http.queryRequest` at each site. Adding a header at each site is how one gets
forgotten. Introduce a single pair of functions — query and mutation — that take the
endpoint, the token and the selection set, apply `withHeader "Authorization" ("Bearer " ++ t)`
when there is one, and are the only way a request is built.

This is also where the module comment changes: `Magnes.Api.Mutation` gets imported for the
first time, and "read-only by construction" stops being true. Say what replaced it.

### 5. Identity state, and the stale-token check

```elm
type IdentityState
    = Unknown            -- before the first identity response
    | Anonymous (List ObjectAction)
    | SignedIn { user : User, permissions : List ObjectAction }
```

`Self.permissions` is `[AuthObjectAction!]!` while `LoginResult.permissions` is
`[Permission!]!` — the same information in two shapes. Normalise to the flat one at the
boundary.

Apply the stale-token rule from [auth-api.md](auth-api.md#the-failure-mode-that-decides-the-client-design):
holding a token while `self.identity.user` is null clears the token.

Refresh identity on load, on window focus, and after any `unauthorized` error. Not on a
timer.

### 6. An enforcer, glob-aware

Permissions match by glob. The `admin` role's only permission is `**/**/**`, so an
equality check denies an administrator everything. The server emits only `**` and literals
today, so a full glob implementation is not needed — but a comment saying so is, or the
next pattern will silently fail closed.

```elm
type alias ObjectAction = { namespace : String, object : String, action : String }

can : ObjectAction -> IdentityState -> Bool
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

Guards are a function of `IdentityState` evaluated in `update` on route change, not a wrapper
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
4. **Register** — username, password, invitation code. Entropy meter from
   `self.passwordEntropy`, **debounced** — it is a server round trip per keystroke as the
   Angular UI wrote it. **No email field**: `auth.email_verification` is inert (see "What
   Magnes cannot fix from here" below), so collecting an address would imply a
   verification nothing performs. An instance setting `auth.email_required` therefore
   cannot be registered with from Magnes, and `EMAIL_REQUIRED` maps to a message that says
   so; the setting defaults to `false`.
5. **Users admin** — list with `usernameLike` search, change role, enable/disable, delete.
   Every one of those mutations is unbuilt in the Angular UI, so there is nothing to port.
6. **Invitations admin** — list, create with role and expiry, delete. Show the code as a
   `/register?code=…` link.
7. **Roles admin** — list, create, edit, delete. **`putRole` replaces the entire
   permission set**: load the role's current permissions first, or an edit revokes
   everything the form did not list.

Sign-out is a request, not a client-side act. That changed with
[ADR 0005](adr/0005-use-an-http-only-cookie-for-browser-authentication.md): the browser
credential is an HttpOnly cookie Magnes cannot read or clear, so `self.logoutBrowser`
clears it server-side. There is nothing to erase locally beyond telling the other tabs,
and until the mutation answers the User is still signed in.

### 9. Errors, which are codes

[auth-api.md](auth-api.md#error-codes) is the source of truth for live error shapes.
bitmagnet now emits a stable `extensions.code` on every identity and authorization
failure, and an authorization refusal carries the refused `{namespace, object, action}`,
so the mapping switches on the code and never on message text.

That mapping lives in one place, `src/ApiError.elm`. `path` remains useful for telling
which of several top-level fields failed, but it is no longer the only signal.

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
