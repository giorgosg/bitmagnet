# Magnes — working notes

Reference for building Magnes, an alternative web UI for a
[fork of bitmagnet](https://github.com/giorgosg/bitmagnet) that has authentication and
accounts. These pages are written **for agents**: they carry the facts a change needs,
with the file paths and the reasoning, so the same investigation is not repeated.

The user-facing description of the project is [../README.md](../README.md). It describes
where Magnes is going; these pages describe what is actually true today.

## Which page, and when

| Read                                                   | When                                                                              |
| ------------------------------------------------------ | --------------------------------------------------------------------------------- |
| [plan.md](plan.md)                                     | Understanding phase 1 — the UI with no server, which is what currently runs       |
| [bitmagnet-api.md](bitmagnet-api.md)                   | Writing any query at all — the schema, transcribed; bitmagnet publishes no reference |
| [auth-api.md](auth-api.md)                             | Writing any query or mutation that touches identity, users, roles, keys           |
| [bitmagnet-ui-audit.md](bitmagnet-ui-audit.md)         | Deciding what Magnes should do — what the Angular UI does, and what it never did  |
| [accounts-plan.md](accounts-plan.md)                   | Implementing accounts in Magnes: the work items, in order, with the decisions     |
| [serving-and-testing.md](serving-and-testing.md)       | Running Magnes against a real instance, same-origin or cross-origin               |
| `environment.local.md`                                 | Which instances exist here, and how to reach them — gitignored, may be absent     |

## Which instance to point at

Write one if it is missing. What it needs to answer:

- Which instances exist, how each is reached, and what version each reports.
- Which fork version each instance runs.
- Which has `http_server.static` configured, at what path, and whether anything is
  actually deployed there.
- Whether `auth.anonymous_access` is still on, and which instance is safe to turn it off
  on. Not one that other people or other clients depend on.
- The exact `BITMAGNET_URL` for codegen, and the `MAGNES_API_URL` for `public/config.js`.

[serving-and-testing.md](serving-and-testing.md) covers the general shape of all of that —
the three ways to serve, what each requires, and the failure modes worth recognising.

`src/Magnes/Api/` is committed, so a checkout builds without a live schema. It was last
regenerated on 2026-09-03, for ticket 11, from the fork's SDL. As of 2026-10-05 it matches
bitmagnet `trunk` except for `self.updatePassword`, which ticket 20 adds. Regenerate it
only when the fork's schema changes; see
[serving-and-testing.md](serving-and-testing.md#regenerating-the-client).

## Where the truth lives

Magnes has no server of its own yet, so almost every fact these pages state is a fact
about the bitmagnet fork. Its checkout is `../bitmagnet` (branch `trunk`), and it carries
its own working notes:

| bitmagnet path                    | What it holds                                                        |
| ---------------------------------- | -------------------------------------------------------------------- |
| `graphql/schema/*.graphqls`       | The schema — the source of truth for every type quoted here          |
| `docs/auth.md`                    | The operator's page: config keys, defaults, deployment requirements  |
| `docs/architecture/auth.md`       | Why each auth control is shaped the way it is; the four failure modes |
| `docs/architecture/interfaces.md` | HTTP surface, the `static` option, CORS                              |
| `CONTEXT.md`                      | The auth glossary — **use its words**: Identity, User, Object action |

Read `CONTEXT.md` before naming anything. "Guest", "session", "scope" and "account" are
listed there as words to avoid, and Magnes should not reintroduce them.

## Snapshots expire

Most of what is here was checked on **2026-08-24** against bitmagnet `trunk` at
`77fdb9de7`. Statements marked **[verified]** were checked by request against a live
instance; the rest are read off the source. Re-check before relying on a number, and see
`docs/environment.local.md` for what the instances were at the time.

Rechecked on **2026-08-29** against `trunk` at `a76cb8f5c`, which is what the development
instance now runs: the error-code table, the two browser mutations, and the shapes of
`listInvitations`, `listRoles`, `invite` and `deleteInvitation` — the last four by sending
them anonymously and confirming they reach authorization rather than failing validation.

Since 2026-09-03, the credentialed end-to-end suite (`e2e/README.md`) has exercised
registration, login, sign-out and API keys against a real, disposable bitmagnet. The rest
of the `auth` namespace (User, Invitation and Role administration) is still confirmed by
the browser only as far as its pages loading; that is ticket 21.

Checked again on **2026-10-05** against `trunk` at `3d0f85d63`, and on **2026-10-06** at
`0b6feb025`, by reading the source. The homeserver could not be reached on the first of
those days. Since 2026-09-03, trunk has:

- added `self.updatePassword`;
- made sign-out end every session for the account;
- narrowed Anonymous access to read actions;
- made Anonymous access a deny-override over a stored `anon` Role. `auth-api.md` records all three, and the spec's
"Cross-repository state" section records what each one means for Magnes.
