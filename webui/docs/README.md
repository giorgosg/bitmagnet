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
| [auth-api.md](auth-api.md)                             | Writing any query or mutation that touches identity, users, roles, keys           |
| [bitmagnet-ui-audit.md](bitmagnet-ui-audit.md)         | Deciding what Magnes should do — what the Angular UI does, and what it never did  |
| [accounts-plan.md](accounts-plan.md)                   | Implementing accounts in Magnes: the work items, in order, with the decisions     |
| [serving-and-testing.md](serving-and-testing.md)       | Running Magnes against a real instance, same-origin or cross-origin               |
| `environment.local.md`                                 | Which instances exist here, and how to reach them — gitignored, may be absent     |

## Which instance to point at

Nothing in this directory names a host, an address or a path on anyone's machine. Those
live in **`docs/environment.local.md`**, which is gitignored (`*.local.md`) because it is a
map of one person's deployment and has no business in a public repository.

Write one if it is missing. What it needs to answer:

- Which instances exist, how each is reached, and what version each reports.
- Which of them has the **auth port** — nothing about accounts can be tested without it.
- Which has `http_server.static` configured, at what path, and whether anything is
  actually deployed there.
- Whether `auth.anonymous_access` is still on, and which instance is safe to turn it off
  on. Not one that other people or other clients depend on.
- The exact `BITMAGNET_URL` for codegen, and the `MAGNES_API_URL` for `public/config.js`.

[serving-and-testing.md](serving-and-testing.md) covers the general shape of all of that —
the three ways to serve, what each requires, and the failure modes worth recognising.

`src/Magnes/Api/` has no auth types: it was generated against an instance that predated the
auth port. Regenerating against one that has it is the first step of any account work.

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

Everything here was checked on **2026-08-24** against bitmagnet `trunk` at `77fdb9de7`.
Statements marked **[verified]** were checked by request against a live instance running
that commit; the rest are read off the source. Re-check before relying on a number, and
see `docs/environment.local.md` for what the instances were at the time.

Nothing behind a credential has been verified: the probes were all anonymous, so
registration, login, API keys and the whole `auth` namespace are read off the source only.
