# End-to-end tests

The real Elm bundle, in a real browser, against a real bitmagnet.

There are two suites, because they need different things to exist.

```bash
npm run test:e2e                 # credential-free: no password, no database
npm run test:e2e:credentialed    # signed in, against a bitmagnet it brings itself
npm run test:e2e -- --ui         # pick through them interactively
```

The **credential-free** suite is everything reachable while Anonymous. Playwright starts
`npm run dev` itself and reuses one already running. The upstream bitmagnet comes from the
gitignored `.dev/env`, so no host is named in the repository — see
`docs/serving-and-testing.md`.

The **credentialed** suite is everything past a successful sign-in. It needs no host and
nobody's password: it stands up its own bitmagnet per run, registers its own User, and
generates every password it uses. See below.

## Why these exist alongside elm-test

`Test.Html` renders a view function to virtual DOM and asserts about the result. Elm's
runtime never runs, so focus, history, navigation, and anything depending on a response
actually arriving are invisible to it.

That is not hypothetical. The login field's focus was broken in a way every unit test
passed through: `autofocus` cannot work under `Browser.application`, and the obvious fix —
focus when the login route is entered — was also wrong, because the guard renders
"Resolving Identity…" until `self.identity` answers, so the field does not exist yet.
Only a real browser could show that.

Prefer elm-test for anything decidable from a value. Reach for these when the question is
about the runtime.

## No credentials, by construction

Every assertion here is about being *refused*, so the suite runs unattended and no
password exists in the repository, the environment, or CI. The refusals are real: the
rejection message is bitmagnet's `INVALID_CREDENTIALS` mapped through `ApiError`, so the
path from `extensions.code` to the rendered sentence is genuinely exercised.

## The credentialed suite

`npm run test:e2e:credentialed` runs `e2e/credentialed/` against a bitmagnet that exists
only for that run. Nothing is asked of a person and nothing is left behind.

What happens, in order, from `e2e/harness/serve.js`:

1. **A fixture server starts.** `dev fixture serve` — built there as of 2026-09-03 — from
   the `../bitmagnet` checkout, serving the real Gin, auth middleware and gqlgen stack over a
   clone of the `../btm-testdb` seed template, built there as of 2026-08-29. So the index has
   ~100k real torrents in it, not three rows. It announces its address and a freshly minted
   bootstrap Invitation as one line of JSON on stdout. bitmagnet documents that line as the
   only thing there, but since bitmagnet #82 its logger writes to stdout as well, ahead of
   it. The harness passes every other line on to stderr, so they are still seen. Observed
   2026-10-08 against `trunk` at `51a7c2895`.
2. **A throwaway administrator is registered** through that Invitation, with a password
   generated for the run. The first registration through a bootstrap Invitation is always an
   `admin`. This is the issuer: it only mints Invitations, and no test signs in as it.
3. **The credentials are written** to `.dev/e2e-credentials.json`, which is gitignored, and
   read from there by the `issuer` fixture in `e2e/support/credentialed.js`. Each worker
   then registers an administrator of its own through an `admin` Invitation the issuer
   mints; that is the `credentials` fixture tests sign in as. See "Each worker has its own
   User" below.
4. **`dev.js` starts** pointed at the fixture server. It is the same development proxy a
   person uses: it terminates TLS and forwards `/graphql` with the browser's `Host` and
   `Origin` intact, which is what lets bitmagnet issue its `Secure`, `SameSite=Strict`
   cookie to a page on `localhost`. Playwright waits for this, so by the time any test runs
   the credentials are already there.
5. **Everything is dropped on the way out** — the cloned database, the credentials file, and
   the built binary. That shutdown is driven from `e2e/harness/teardown.js` rather than left
   to Playwright, which kills its web server faster than a `DROP DATABASE` finishes; without
   it, every run left a `bitmagnet_test_*` database behind. Verified 2026-09-03 over three
   consecutive runs: no database, no build directory, no credentials file.

### What it drives

Sign-in, sign-out across tabs and devices, API keys, the status page, and since ticket 21 the
three administration screens. `users.spec.js`, `invitations.spec.js` and `roles.spec.js` act
through each screen, check what bitmagnet did, and check the effect on the User acted on from
a browser of its own; `administration-access.spec.js` checks what an Identity without
`auth::query`, or with it but without `auth::mutate`, is offered.

### What it needs present

If the database is not up, the harness says so and stops before building anything —
`nothing is listening at 127.0.0.1:5434 … Is the test database up?` — rather than letting
the fixture server panic with a connection error buried in a stack trace.


- `../bitmagnet` — a checkout, and a Go toolchain to build it. Overridable with
  `MAGNES_E2E_BITMAGNET`. The harness builds whatever branch is checked out there, not
  necessarily `trunk`, so check that before reading much into a result.
- `../btm-testdb` — up, with a seed template loaded (`bin/testdb status`). Overridable with
  `MAGNES_E2E_TESTDB`, or bypassed entirely by setting `TEST_POSTGRES_TEMPLATE_DSN`.

Neither is needed by `npm run test:e2e`, which is why the two suites do not run together.

### Deliberate settings

The instance the harness asks for is stated in `fixtureFlags` in `e2e/harness/serve.js`,
along with why the login throttle is not the shipped one. Change it there.

## What is still not covered

- **A mount other than the origin root.** The harness serves Magnes at `/`, so nothing here
  checks that a deployment under a base path, such as `/ui`, still parses its routes and
  builds its links, an Invitation's registration link among them. `RouteTest` and
  `InvitationsTest` check both under `/magnes`. A browser check needs `dev.js` to serve
  beneath a prefix, with the matching `<base href>`.
- **Anonymous access off**, which the feature spec requires the one bundle to handle, and
  **the login throttle's wait state**. Both need a fixture server configured the other way,
  which is a second set of flags and a second project rather than anything new underneath.

The last two are a spec away rather than a harness away; the first needs `dev.js` to serve
beneath a prefix.

### Each worker has its own User, because sign-out ends all of a User's sessions

Since bitmagnet `77f3fd9e3` (2026-09-14), `logoutBrowser` and `updatePassword` end **every**
session the User has, on every device. While every test signed in as one administrator,
under `fullyParallel`, a test that signed out ended the sessions of every other test running
at that moment, along with any bearer token `mintInvitation` held. Observed 2026-10-05
against a `trunk` export: three parallel runs each failed one or two tests, a different test
each time, while serial runs passed.

So each worker registers an administrator of its own (the `credentials` fixture), through an
`admin` Invitation minted by the harness's administrator (the `issuer` fixture). Tests in
one worker run one at a time and each signs in afresh, so a sign-out can only reach that
worker's later tests, which sign in again anyway. The issuer only ever mints over the API and
never signs in through a browser, so nothing can revoke the token it mints with. Ticket 23
in `.scratch/identity-and-permissions/`. Verified 2026-10-08: five consecutive runs against
bitmagnet `trunk` at `30e8d486b`, at the default worker count, 28 of 28 each time.

`identity.spec.js` pins the behaviour itself: the same User signed in from two browser
contexts, which share no cookie and stand for two devices, and signing out in one ends the
session in the other.

A test that changes a password, disables, deletes or demotes a User, or needs an ordinary
one, still registers a User of its own with `registerUser`. Changing the worker's
administrator's password would leave its later tests signing in with the old one, and the
rest would take away what they sign in as.

## Conventions

- Address elements the way a person does — `getByLabel`, `getByRole` — so the tests assert
  the accessible names really exist rather than pinning CSS classes.
- Assert on behaviour, not implementation. `toBeFocused()`, not "did a focus command run".
- Keep the credential-free suite credential-free. If a test needs a password, it belongs in
  `e2e/credentialed/`, which has one.
- Shared helpers live in `e2e/support/`, not beside a spec: the credential-store recorder is
  used by both suites, because the refusal paths need no credential and the success paths do.

## What the fixture serves the operational pages

The fixture server from bitmagnet PR #83 supplies health, workers, queue queries and
mutations, and torrent metrics. Health runs a real Postgres check against the cloned
database. The worker registry lists production worker keys, with the fixture's HTTP
worker started and the crawler and queue workers stopped; it does not start crawlers or
process queued jobs.

`dev fixture serve` enables `--seed-dashboard-data` by default: jobs in every status
across two queues, and a bounded set of torrent-source timestamps moved into the recent
metrics window. `torrent.listSources` reads the cloned database. The status spec checks
Anonymous access, administrator and ordinary User views, and visibility-aware polling.
