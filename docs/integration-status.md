# Integration status

This is the compact ledger for downstream work evaluated for `trunk`. The fork pages
under [forks/](forks/README.md) explain the source repositories; this page records what
we actually accepted, what we intend to evaluate next, and why plausible patches were
deferred.

Update this page when a candidate is merged, rejected, or superseded. A source commit
listed here is attribution and provenance, not permission to cherry-pick it without the
test-first review described in [git-workflow.md](git-workflow.md).

## Integrated into trunk

| PR                                                   | Change                                                                      | Downstream source                                | Verification note                                          |
| ---------------------------------------------------- | --------------------------------------------------------------------------- | ------------------------------------------------ | ---------------------------------------------------------- |
| [#1](https://github.com/giorgosg/bitmagnet/pull/1)   | CI action upgrades and manual trigger                                       | niklas2233 `6be9151cf`, `152d9a405`, `b09aacad5` | Full fork CI passed                                        |
| [#2](https://github.com/giorgosg/bitmagnet/pull/2)   | PostgreSQL pool tuning, hybrid queue dispatch, batched queue GC, TMDB cache | selected ghobs91/lodestone commits               | Added queue, cache, and PostgreSQL regression coverage     |
| [#3](https://github.com/giorgosg/bitmagnet/pull/3)   | Queue fetch expression index                                                | o51r15 `6ee9ec545`                               | PostgreSQL plan test observed red before the migration     |
| [#4](https://github.com/giorgosg/bitmagnet/pull/4)   | Torznab category disjunction fix                                            | niklas2233 `77b7920f4`                           | PostgreSQL regression observed red                         |
| [#5](https://github.com/giorgosg/bitmagnet/pull/5)   | DHT bootstrap interval and empty-table recovery                             | o51r15 `9711ecbbb`, substantially rewritten      | Deterministic config and routing-table tests observed red  |
| [#6](https://github.com/giorgosg/bitmagnet/pull/6)   | Integration-fork purpose in the README                                      | local                                            | Documentation only                                         |
| [#7](https://github.com/giorgosg/bitmagnet/pull/7)   | Standalone season and episode search lexemes                                | niklas2233 `fbbcd4388`                           | Search regression observed red                             |
| [#8](https://github.com/giorgosg/bitmagnet/pull/8)   | Torznab resolution categories                                               | niklas2233 `1ae29df84`                           | Category mapping regressions observed red                  |
| [#9](https://github.com/giorgosg/bitmagnet/pull/9)   | Canonical-ID Torznab searches and `tvdbid`                                  | niklas2233 `eb843dacc`                           | Both behaviors observed red                                |
| [#10](https://github.com/giorgosg/bitmagnet/pull/10) | Ignore duplicate importer queue jobs                                        | o51r15 `dd314bf47`                               | PostgreSQL regression observed red                         |
| [#11](https://github.com/giorgosg/bitmagnet/pull/11) | Exclude BEP-47 padding from the file threshold                              | o51r15 `5efcbd1c5`, rewritten                    | Regression observed red and passed under the race detector |

## Next batch

The order favors demonstrated data loss and broad correctness before operational tuning.
Each item should remain a focused PR.

1. **Scope null-content reprocessing correctly** — port the Go portion of
   kawaii-not-kawaii `f5f027a45`. The current `OR content_type IS NULL` can detach from
   the correlated subquery and enqueue the whole database. Replace the source's SQL-shape
   test with a PostgreSQL behavior test that proves only unclassified torrents are queued.
2. **Classify title-only RSS torrents** — adapt niklas2233 `dca622876`. Let the video-name
   parser run when file information is absent or total size is unknown, with workflow
   fixtures proving a video-looking title is classified and a non-video title is not.
3. **Preserve rule-derived types when file evidence is unavailable** — port the Go-only
   portion of kawaii-not-kawaii `68bcddf43`. Reprocessing `no_info` and `over_threshold`
   torrents can otherwise clear source-less classifications such as `xxx`. Exercise the
   processor behavior, not only the extracted helper.
4. **Make copy-magnet truthful on plain HTTP** — adapt kawaii-not-kawaii `236c129d7`.
   Use the Angular CDK clipboard fallback at all three call sites, test failure feedback,
   and rebuild committed `webui/dist`.
5. **Cap global in-flight DHT queries** — redesign o51r15 `727328128` with validation for
   non-positive configuration and deterministic concurrency/cancellation tests. This is
   complementary to upstream PR #514, which only configures the existing per-IP limiter.
6. **Add crawler queue backpressure** — reconstruct o51r15 `f7cb97d4b` plus the behavior
   embedded in `5efcbd1c5`. First measure the queue-growth failure, then test threshold,
   disabled, drain-and-resume, and persistence-without-classification behavior.

## Deferred or already resolved

- niklas2233 `0b3f1480b` edits only `categories.gen.go`; its generator was removed from
  the repository. Do not hand-edit the generated file. Revisit only with a maintained
  source-of-truth and reproducible generator.
- niklas2233 `a6f69bf23` raises the aggregation budget tenfold. It needs a representative
  PostgreSQL accuracy/performance measurement before becoming a code change.
- o51r15 `9e564ff9e` claims a ticker-reset data race but has no failing reproduction. Fold
  any proven scheduling issue into a separately tested queue change.
- o51r15 `a2ee2958e` removes `router.bittorrent.cloud`; that node is already absent from
  `trunk`.
- Upstream [#514](https://github.com/bitmagnet-io/bitmagnet/pull/514) makes the per-IP DHT
  limiter configurable and remains open. Avoid carrying a duplicate unless upstream
  stalls or the global concurrency work establishes a combined configuration design.
