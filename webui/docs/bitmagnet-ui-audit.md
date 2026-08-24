# What bitmagnet's own UI does, and what it never finished

An audit of `../bitmagnet/webui` (Angular, `trunk` `77fdb9de7`, read 2026-08-23). Magnes
is meant to replace it, so this page is the specification-by-example: what exists is what
users already expect, and what is missing is what Magnes has to add rather than port.

The account screens were added by the auth port (`feat(webui): add login and registration`,
`feat(webui): add account, API keys and the user admin screens`). They are **a first
pass** — the read paths are built and most of the write paths are not.

## Routes it has

| Route                     | Guard                        | State                        |
| ------------------------- | ---------------------------- | ---------------------------- |
| `/login`                  | redirects away if signed in  | works                        |
| `/register`               | redirects away if signed in  | works                        |
| `/account`                | requires a User              | works                        |
| `/account/api-keys`       | requires a User              | create only, no delete       |
| `/torrents`               | none                         | works                        |
| `/torrents/permalink/:infoHash` | none                   | works                        |
| `/dashboard`              | **none**                     | works                        |
| `/dashboard/queues/{visualize,jobs,admin}` | **none**    | works                        |
| `/dashboard/users`        | **none**                     | read-only table              |
| `/dashboard/roles`        | **none**                     | add and edit, no delete      |
| `/dashboard/invitations`  | **none**                     | create only, no delete       |
| `/dashboard/torrents`     | **none**                     | works                        |

`app.routes.ts` has exactly one guard, `requireUserGuard`, and it only ever asks *is there
a User or not*. It is applied to the four account routes and to nothing else.

## What is missing

Verified by grepping every `.ts` and `.html` under `webui/src/app` for the mutation names.
None of these are called anywhere:

| Mutation          | Consequence                                                      |
| ----------------- | ----------------------------------------------------------------- |
| `setUserRole`     | A user's role cannot be changed from the UI at all               |
| `setUserEnabled`  | A user cannot be disabled — and `User` has no `enabled` field to show it anyway |
| `deleteUser`      | A user cannot be removed                                          |
| `deleteRole`      | A custom role can be created and edited but never deleted         |
| `deleteInvitation`| An unclaimed invitation cannot be withdrawn                       |
| `deleteAPIKey`    | A key can be issued but never revoked                             |

So the administrative surface is roughly "create and look at". Everything that takes access
*away* is unbuilt, which is the half that matters when an account is compromised.

**No permission-driven UI whatsoever.** `AuthService.enforce()` and `AuthService.hasRole()`
exist and are called from nowhere. The only use of the enforcer is inside
`role-edit.component.ts`, computing checkbox state for the role being edited. The
navigation draws the dashboard link for everyone; an unauthorised user reaches
`/dashboard/users` and sees a table whose query is refused. bitmagnet's own
`docs/auth.md` lists this under "Known gaps" and calls it presentation, not protection —
which is true, and also means Magnes gets no help from the existing UI here.

**No password change.** Not a UI gap: there is no mutation to call
([auth-api.md](auth-api.md)).

## Points worth taking, other than the GraphQL

These are decisions the Angular UI got right, or got wrong in an instructive way. They are
about client behaviour, not about the schema.

### The stale-token heuristic

`auth.service.ts` polls `self.identity` every 10 seconds and treats *"I hold a token but
the server reports no user"* as proof the token is dead, clearing it. This is the correct
response to the chain-never-rejects invariant in [auth-api.md](auth-api.md) — there is no
error to catch, so the only signal is the mismatch. **Magnes needs this exact check.**

The 10-second poll itself is worth reconsidering: it is a request every 10 seconds forever
to notice an event that happens once a day at most. Checking on load, on focus, and after
any `unauthorized` error covers the same ground for a fraction of the traffic.

### Token storage

`localStorage`, key `bitmagnet-jwt`, read through a `BrowserStorageService` wrapper, plus a
10-second `setInterval` re-read so a second tab logging out is noticed. The `storage`
event does that without polling.

For Magnes the storage choice is forced anyway: Elm cannot touch `localStorage`, so it goes
through a port either way, and the port can subscribe to `storage` for free.

### Login returns you where you were

`/login?returnUrl=<path>`, and the guard sets it. Navigation after login uses `replaceUrl`
so the back button does not land on the login form. Both are right and cheap.

### The permission editor

`permissions-edit.component.ts` renders every object action from `listObjectActions` as a
checkbox tree, used for both role editing and API-key scoping. Two things fall out of it:

- The `http` namespace actions (`import`, `pprof`, `metrics`) appear in that list, so the
  editor shows permissions that have nothing to do with a browser.
- `putRole` replaces the whole permission set, so the editor must load the role's current
  permissions first. It does.

### The API key is shown once

`api-key-add.component.ts` displays the secret after creation with a copy-to-clipboard
control and a "take note of this" line, and it is never retrievable again — only the
hash is stored. Any replacement has to be as emphatic about it.

### Live password entropy

Registration queries `self.passwordEntropy` and draws a meter against `minEntropy`. It is
a server round trip, and as written it fires per keystroke. Keep the meter, debounce the
query.

### Errors are strings in a toast

`ErrorsService.addError(...)` with the raw GraphQL message appended to a translated
prefix. Serviceable, and the reason [auth-api.md](auth-api.md) lists the message strings —
there are no codes to map, so any friendlier presentation means substring matching.

### Things Magnes should not copy

- **The dashboard is drawn for everyone.** Nav should follow permissions.
- **Polling identity every 10s.** See above.
- **`role` as a bare string on `User`.** Fine on the wire; in Elm it should be parsed into
  a type at the boundary rather than compared as text at each site.
