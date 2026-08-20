# niklas2233/bitmagnet

<https://github.com/niklas2233/bitmagnet> · remote `niklas2233` · branch `main`

**Snapshot:** surveyed 2026-08-18. See [integration-status.md](../integration-status.md) for what has been taken from it since.

**44 unique commits, 0 missing from upstream.** Active Jun–Jul 2026.
Real diff: **38 Go files, +2,022 / −23** — almost entirely additive, no line-ending or
rename churn.

**The cleanest fork on the list.** Conventional commits, focused scope, CI kept green,
lint fixed. If you want something that cherry-picks without a fight, this is it.

## Torznab fixes — take these regardless

Small, isolated, clearly correct, and useful to anyone running \*arr integrations:

- **`CategoryBooks` used `catCriteria` instead of `options`** — OR where it should have
  been AND. A real bug.
- Map video resolution to HD/SD/UHD subcategories
- Add `tvdbid` attribute; skip text search when an IMDB/TMDB ID is supplied
- Add anime category 5070 as a TV subcategory

Plus:

- **Raise the search aggregation budget to 50k** for accurate source counts —
  `internal/search`
- **Index standalone season/episode lexemes** in full-text search — improves matching
  on `S01E02`-style queries
- **Parse video names when file info or size is missing** — classifier robustness fix

## Prowlarr / RSS integration

- `internal/prowlarr` — Prowlarr as a unified torrent source, settings page, a
  `POST /api/prowlarr/test` connection test, per-indexer state
- `internal/rssimporter` — RSS/Torznab source management with a queue

Thoughtful details that suggest real operational use:

- **Skip the bitmagnet indexer to prevent self-feeding** — the obvious footgun
- Surface Prowlarr 429s and abort on a rate-limit storm
- Download delay, non-2xx handling, 60s timeout, skip delay for cached URLs
- Percent-encode `+` in the Prowlarr link parameter
- Use `t=search` for torrent feeds and filter out usenet indexers
- Poll feeds concurrently, count connection errors toward abort

Competes with [o51r15](o51r15.md)'s implementation (`internal/prowlarr` +
`internal/rssfeed`). Same package name, different code — **pick one**.

Of the two, niklas2233's looks more carefully hardened against Prowlarr's actual
misbehaviour (rate limits, usenet indexers, self-feeding). o51r15's is larger and
bundles OMDB/TPDB enrichment alongside.

## Infrastructure

- Dependabot for Go, Docker, and GitHub Actions
- `actions/checkout` v4→v5, `golangci-lint-action` v9, `install-nix-action` upgrades
- `transloco-keys-manager` upgrade to fix `npm ci`
- docker-compose restructured with a VPN toggle, TMDB key moved to env

The CI/lint upgrades are worth taking early — upstream's workflows have Node 20
deprecation warnings and the `npm ci` breakage will bite you on any webui work.

## Noise

About a third of the commits are lint churn (`fix(lint): fix 7 more wsl return-after-block
issues`) and `build(webui): rebuild dist`. Squash on merge; don't cherry-pick individually.

## Assessment

Best cherry-pick target. Suggested order:

1. Torznab fixes + FTS season/episode + aggregation budget — small, independent, valuable
2. CI/lint/dependabot infrastructure
3. Prowlarr/RSS, if and when you want it
