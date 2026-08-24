# Centralize Identity and authorization error mapping

Status: resolved
Type: task
Blocked by: 04

## Goal

Translate bitmagnet's current string-only GraphQL failures into deliberate UI states at one boundary.

## Work

- Match documented error-message substrings rather than exact wrapped messages.
- Use the GraphQL error path to distinguish authorization failures by top-level field.
- Give login throttling, invalid credentials, disabled Users, Invitation failures, duplicate Users, and insufficient password entropy distinct outcomes.
- Treat any GraphQL error on identity or administration mutations as total failure even when partial data is present.
- Keep the mapping isolated so future machine-readable extensions can replace string parsing locally.

## Acceptance criteria

- All documented auth error strings map to stable application outcomes.
- `too many login requests` is not shown as an invalid password.
- An `unauthorized` outcome can trigger the Identity refresh defined by ticket 05.
- Unrecognized errors retain a useful fallback message.

## References

- `docs/accounts-plan.md`, work item 9
- `docs/auth-api.md`, "Error strings"

## Comments

- 2026-08-24: Next Magnes frontier. Deliberately blocked on bitmagnet
  `.scratch/magnes-browser-auth/issues/05-add-stable-graphql-auth-error-codes.md` so the
  client does not commit a new contract based on wrapped English messages. The homeserver
  at `2cd049c` still returns string-only login failures without `extensions.code`.

- 2026-08-24: Unblocked — bitmagnet PR #55 merged (`19696fe5`), and the homeserver on
  trunk was verified live to return `extensions.code` on login failures and
  `{code, namespace, object, action}` on an `unauthorized` refusal. Implemented as
  `src/ApiError.elm`, switching on the code and never on message text; unknown codes fall
  back to the server's message. `Bitmagnet.errorToString` and `Identity.isUnauthorized`
  are gone, so no call site inspects error text any more. Elm registry searched for a
  relevant package first (per `docs/agents/elm-development.md`); nothing covers it.
  `docs/auth-api.md` and `docs/accounts-plan.md` item 9 rewritten — the old "no
  machine-readable detail on any of them" text was stale.

- 2026-08-24, review: renamed `UserSessionRequired` to `UserAuthenticationRequired`
  (CONTEXT.md's Identity entry avoids "session"); split
  `AUTHENTICATION_INFRASTRUCTURE_FAILURE` back out of `INTERNAL_SERVER_ERROR` as
  `ServiceUnavailable`, since one is retryable and the other is not; implemented the error
  `path` as `Refusal.field`, which was the one Work item still outstanding; and extracted
  the duplicated refresh-or-show branch in `Main.elm` as `onRequestFailure`. Added
  `tests/ApiErrorLiveShapeTest.elm`, which runs the bytes the homeserver actually returned
  through elm-graphql's own error decoder.

  Two review findings were deliberately not acted on. `ApiError.ObjectAction` duplicating
  `Identity.ObjectAction` is intentional: Elm record aliases are structurally identical, so
  a refusal's Object action feeds `Identity.can` with no re-wrap (there is a test for
  exactly that), and defining it here keeps a future `Identity`-to-`ApiError` dependency
  from becoming an import cycle. Carrying `Failure` in the Model instead of a `String`
  is a real improvement but touches `Identity.Failed` and `Results.Failed`; it belongs
  with ticket 09, not here.

## Answer

The mapping is one module, `src/ApiError.elm`, with `Failure` as the only vocabulary the
rest of the app sees. Unblocks ticket 09.
