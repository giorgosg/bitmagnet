# End-to-end tests

The real Elm bundle, in a real browser, against a real bitmagnet.

```bash
npm run test:e2e            # headless
npm run test:e2e -- --ui    # pick through them interactively
```

Playwright starts `npm run dev` itself and reuses one already running. The upstream
bitmagnet comes from the gitignored `.dev/env`, so no host is named in the repository —
see `docs/serving-and-testing.md`.

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

## What this cannot cover yet

Everything past a successful sign-in: the redirect to `returnUrl`, sign-out, API-key
management, and the administration workflows. Those need a User to exist, and pointing
them at a shared instance would mean either a real password in CI or tests that mutate
someone's live data.

The offer to the browser's credential store (ticket 17) is covered only on its refusal
paths, where nothing should be offered. The call itself happens on success, so it needs
the same harness.

Registration is a case of its own. Every refusal is covered here, as is the live
`self.passwordEntropy` round trip — bitmagnet answers it anonymously — but a *successful*
registration would consume a real Invitation and leave a User behind, which is exactly
what a disposable instance is for.

The way through is a bitmagnet with a known state per run, which is more than a config
flag. It has three parts, in two other places:

- `../btm-testdb` — a local PostgreSQL holding ~100k torrents sampled from a real crawl,
  a seed database that clones in about a second, and a reset.
- `../bitmagnet` — a fixture-server command standing up the real Gin, auth middleware, and
  gqlgen stack against a cloned database, printing a bootstrap invitation this harness can
  register a throwaway admin through. It has to live there: those packages are under
  `internal/`, which Go will not let another module import.
- Here — a second Playwright project that consumes both, leaving this credential-free
  suite runnable with no services present.

Tracked as `.scratch/identity-and-permissions/issues/16-build-credentialed-e2e-harness.md`.

## Conventions

- Address elements the way a person does — `getByLabel`, `getByRole` — so the tests assert
  the accessible names really exist rather than pinning CSS classes.
- Assert on behaviour, not implementation. `toBeFocused()`, not "did a focus command run".
- Keep them credential-free. If a test needs a password, it belongs in the harness above.
