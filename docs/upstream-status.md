# Upstream status

Surveyed 2026-08-18.

## `main`

Last commit `e31b30d` "fix: upgrade go-resty to fix TMDB (#506)", **2026-05-21** — about
three months stale. Development is slow but not abandoned; the maintainer (mgdigital)
was active on the `next` branch as recently as 2026-04.

## The `next` branch — the thing that shapes everything else

Draft PR [#469](https://github.com/bitmagnet-io/bitmagnet/pull/469), branch
`upstream/next`. Last touched **2026-04-03**.

Only 35 commits off `main`, but they are enormous: **1,144 files, +111,334 / −34,359**.

Areas rewritten:

| Area                  | Files touched |
| --------------------- | ------------- |
| `webui/src`           | 180           |
| `internal/database`   | 74            |
| `internal/plugin`     | 60 (new)      |
| `internal/auth`       | 60 (new)      |
| `internal/protocol`   | 56            |
| `internal/wasm`       | 45 (new)      |
| `internal/classifier` | 41            |
| `internal/gql`        | 36            |

New subsystems that don't exist on `main` at all: a **plugin system**, a **WASM
runtime**, and a **full auth stack** (JWT, API keys, Casbin RBAC — see [auth.md](auth.md)).

### Why this matters

Anything merged into `main` today may be irrelevant if `next` lands. But `next` has
been dormant for four months and is still a draft, and no fork has adopted it —
o51r15, lodestone, niklas2233, kawaii-not-kawaii and gabriel20xx are all on the `main`
lineage, none of them carry `internal/plugin`, `internal/wasm`, or `internal/auth`.

**Working assumption: build on `main`.** That's where the community is. Revisit if
`next` comes out of draft.

## PR queue

30 open PRs: **22 mergeable, 8 conflicting**. Oldest still open is #125 from
2024-01-28.

Worth noting: **o51r15 has already cherry-picked several of them** (#446 bloom filter
nil panic, #454 bootstrap nodes, #458 BEP-47 padding files), which is a useful
shortcut — see [forks/o51r15.md](forks/o51r15.md).

Notable open PRs not yet absorbed by any fork:

| PR                                                         | Title                                                           | State                                         |
| ---------------------------------------------------------- | --------------------------------------------------------------- | --------------------------------------------- |
| [#516](https://github.com/bitmagnet-io/bitmagnet/pull/516) | bulk-delete acts on all matching results, not just current page | mergeable                                     |
| [#515](https://github.com/bitmagnet-io/bitmagnet/pull/515) | AV1 codec                                                       | mergeable, 9 lines                            |
| [#514](https://github.com/bitmagnet-io/bitmagnet/pull/514) | Configurable DHT rate limit                                     | mergeable — overlaps lodestone's rate limiter |
| [#513](https://github.com/bitmagnet-io/bitmagnet/pull/513) | Docker layer caching for Go modules                             | mergeable, 6 lines                            |
| [#510](https://github.com/bitmagnet-io/bitmagnet/pull/510) | fix(dht): unmap IPv4-in-IPv6 to prevent EAFNOSUPPORT            | mergeable                                     |
| [#500](https://github.com/bitmagnet-io/bitmagnet/pull/500) | Named regex capture groups in ParseVideoContent                 | mergeable                                     |
| [#482](https://github.com/bitmagnet-io/bitmagnet/pull/482) | Updated classifier banned terms                                 | mergeable                                     |
| [#467](https://github.com/bitmagnet-io/bitmagnet/pull/467) | Verbose mode on classifier                                      | mergeable                                     |

All are available locally as `upstream-pr/<number>`.
