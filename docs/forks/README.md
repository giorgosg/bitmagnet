# Fork triage

Which downstream repo a change should come from, and which ones to leave alone. Of 258
forks, all but a handful are unmodified mirrors; the seven with real work get a page
each.

**Snapshot:** surveyed 2026-08-18. [integration-status.md](../integration-status.md)
records what has happened to these candidates since.

## Real divergence

Counts are patch-equivalence based (`git cherry`), not GitHub's ahead/behind — see
[../porting.md](../porting.md#the-three-measurements-that-lie) for why that distinction
matters. Diffstats are two-dot, whitespace/CRLF-**normalized**, excluding generated code
and `webui/dist`.

| Fork                                      | Unique commits | Real Go diff          | Up to date?     | Theme                                    |
| ----------------------------------------- | -------------- | --------------------- | --------------- | ---------------------------------------- |
| [o51r15](o51r15.md)                       | 106            | 65 files, +4,920/−26  | ✅ current      | Prowlarr, RSS, OMDB/TPDB, DB import, ops |
| [lodestone](lodestone.md)                 | 41             | ~20 files substantive | 1 commit behind | Performance / concurrency                |
| [niklas2233](niklas2233.md)               | 44             | 38 files, +2,022/−23  | ✅ current      | RSS + Prowlarr, torznab fixes            |
| [kawaii-not-kawaii](kawaii-not-kawaii.md) | 340            | 157 files             | ✅ current      | LLM classification, Angular 21 UI        |
| [gabriel20xx](gabriel20xx.md)             | 86             | 226 files             | ✅ current      | React UI rewrite, auth, qBittorrent      |
| [other forks](other-forks.md)             | —              | —                     | —               | nigowl (media player), dashed (Rust)     |

## Ranked pick list

Ordered at survey time. Where integration has since contradicted the ranking, the entry
says so — [integration-status.md](../integration-status.md) is the authority.

**1. lodestone's performance commits** — highest value. Self-contained, no product
opinion attached, addresses real bottlenecks. Only complication is the module rename
churn, which `-w` filtering handles.

> **The survey also called this the lowest-risk pick, and that was wrong.** Working
> through it produced two accepted changes and five rejections, and the pre-rename
> `perf:` commits contained a deadlock in `request_meta_info.go` that compiled, vetted
> and passed the whole suite. Highest value, yes. Lowest risk, no — see
> [lodestone.md](lodestone.md#assessment).

**2. o51r15's DHT and DB fixes** — bootstrap node reseeding, queue backpressure,
concurrency semaphore, the `queue_jobs` expression index. Plus it already carries
upstream PRs #446/#454/#458.

**3. Auth** — three implementations available, see [../auth.md](../auth.md). Relevant
even if you replace the web UI, since auth belongs at the GraphQL/HTTP layer.

**4. Prowlarr / RSS** — two independent implementations (niklas2233 and o51r15).
Pick one. Not currently in use, so low priority.

**5. kawaii's LLM classification** — genuinely novel, but 340 commits and a new
`internal/llm` package. Evaluate as a unit later, not as cherry-picks.

## Overlap and collision map

Things that will fight each other if you take two forks naively:

- **Prowlarr**: niklas2233 (`internal/prowlarr` + `internal/rssimporter`) vs o51r15
  (`internal/prowlarr` + `internal/rssfeed`). Same package names, different code.
- **Auth**: gabriel20xx vs `upstream/next` vs rolling your own.
- **DHT rate limiting**: lodestone's global limiter vs o51r15's concurrency semaphore
  vs open PR #514.
- **Migrations**: o51r15 uses `00023`, gabriel20xx uses `00033`; upstream is at `00020`.
  Renumber on merge.
- **Web UI**: upstream Angular 18, kawaii Angular 21, gabriel20xx React 19. No two of
  these can be combined. If you're replacing the frontend anyway, their backend and
  GraphQL changes are the only portable part.

## Not worth pursuing

- `nigowl` — video player / transcoding product, restructured repo layout.
- `dashed:alberto/my-fork` — parallel Rust (`bitmagnet-rs`) DHT reimplementation.
- `ghobs91/lodestone`'s rename/branding commits — take the perf work, leave the rest.
- The ~250 remaining forks — unmodified mirrors.
