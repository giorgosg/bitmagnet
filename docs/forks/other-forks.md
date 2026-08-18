# Other forks

Surveyed but not recommended as merge sources. Recorded so they don't get re-triaged.

## nigowl/bitmagnet

<https://github.com/nigowl/bitmagnet> · remote `nigowl` · 54 unique commits, 1 upstream
commit missing. Active to 2026-07-06.

Turned bitmagnet into a **streaming media application**. Repo layout was restructured
(no `go.mod` at the root any more).

- In-browser torrent player with HLS streaming, seek controls, subtitles
- ffmpeg transcoding with HDR-aware defaults, video image settings
- HLS session caching, startup prebuffer tuning, paused-heartbeat handling
- An admin backend with user authentication (commit messages partly in Chinese)
- Media score filtering and SEO metadata
- Transmission caching integration

**Verdict:** a different product. The HLS/transcoding work has no counterpart upstream
and doesn't decompose into portable patches. Skip.

## dashed/bitmagnet — branch `alberto/my-fork`

<https://github.com/dashed/bitmagnet> · remote `dashed` · 498 unique commits, 1 upstream
commit missing. Active to 2026-08-12. Note the non-default branch.

Contains a **parallel reimplementation of the DHT layer in Rust** (`bitmagnet-rs/`,
Tokio-based) alongside the Go code, plus a `webui-react/` tree next to the Angular one.

Recent commits are all `feat(dht): add … parity` — routing tree, node table, ping and
find-node dispatch, reply driver, query supervisor, KTable core. A methodical port
tracking the Go implementation feature by feature.

Also carries `FORK_WORKFLOW.md`, `SITEM_SPEC.md`, `CLAUDE.md`, and a `docs/` tree.

Has some named topic branches that may be independently useful:

- `dashed/upstream/torrent-published-at-filter`
- `dashed/upstream/torrent-size-filter`
- `dashed/size-fitlers/all-in-one` _(sic)_

**Verdict:** the Rust work is out of scope. The `upstream/*` topic branches are named as
if intended for upstreaming and are small enough to be worth a look if you want size or
published-at filters.

## The other ~250 forks

Unmodified mirrors — 0 commits ahead. Checked via the GitHub compare API across all
forks sorted by push date; everything with a `pushed_at` matching upstream's is a plain
sync. The forks with real divergence are the seven documented here and in
[README.md](README.md).

## Renamed downstream products

`ghobs91/lodestone` is also a rename, but its performance work is worth taking — it gets
its own page at [lodestone.md](lodestone.md).
