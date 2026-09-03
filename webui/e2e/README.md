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
   bootstrap Invitation as one line of JSON on stdout.
2. **A throwaway administrator is registered** through that Invitation, with a password
   generated for the run. The first registration through a bootstrap Invitation is always an
   `admin`, which is what makes the administration screens reachable.
3. **The credentials are written** to `.dev/e2e-credentials.json`, which is gitignored, and
   read from there by the `credentials` fixture in `e2e/support/credentialed.js`.
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

### What it needs present

If the database is not up, the harness says so and stops before building anything —
`nothing is listening at 127.0.0.1:5434 … Is the test database up?` — rather than letting
the fixture server panic with a connection error buried in a stack trace.


- `../bitmagnet` — a checkout, and a Go toolchain to build it. Overridable with
  `MAGNES_E2E_BITMAGNET`.
- `../btm-testdb` — up, with a seed template loaded (`bin/testdb status`). Overridable with
  `MAGNES_E2E_TESTDB`, or bypassed entirely by setting `TEST_POSTGRES_TEMPLATE_DSN`.

Neither is needed by `npm run test:e2e`, which is why the two suites do not run together.

### Deliberate settings

The instance the harness asks for is stated in `fixtureFlags` in `e2e/harness/serve.js`,
along with why the login throttle is not the shipped one. Change it there.

## What is still not covered

- **API-key management**, which is not built yet.
- **The administration workflows** beyond reaching them: the User, Invitation and Role
  screens are driven only as far as arriving on each.
- **Anonymous access off**, which the feature spec requires the one bundle to handle, and
  **the login throttle's wait state**. Both need a fixture server configured the other way,
  which is a second set of flags and a second project rather than anything new underneath.

All of these are now a spec away rather than a harness away.

## Conventions

- Address elements the way a person does — `getByLabel`, `getByRole` — so the tests assert
  the accessible names really exist rather than pinning CSS classes.
- Assert on behaviour, not implementation. `toBeFocused()`, not "did a focus command run".
- Keep the credential-free suite credential-free. If a test needs a password, it belongs in
  `e2e/credentialed/`, which has one.
- Shared helpers live in `e2e/support/`, not beside a spec: the credential-store recorder is
  used by both suites, because the refusal paths need no credential and the success paths do.
