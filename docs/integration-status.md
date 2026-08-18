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
| [#13](https://github.com/giorgosg/bitmagnet/pull/13) | Scope null-content reprocessing correctly                                   | kawaii-not-kawaii `f5f027a45`, adapted           | PostgreSQL behavior regression observed red                |
| [#14](https://github.com/giorgosg/bitmagnet/pull/14) | Classify title-only video torrents                                          | niklas2233 `dca622876`, adapted                  | Video and non-video title-only fixtures observed red       |
| [#15](https://github.com/giorgosg/bitmagnet/pull/15) | Preserve rule-derived types without file evidence                           | kawaii-not-kawaii `68bcddf43`, adapted           | Processor integration regressions observed red             |
| [#16](https://github.com/giorgosg/bitmagnet/pull/16) | Cap global in-flight DHT queries                                            | o51r15 `727328128`, redesigned                   | Deterministic concurrency and fallback tests observed red  |
| [#17](https://github.com/giorgosg/bitmagnet/pull/17) | Pause DHT classification at queue capacity                                  | o51r15 `f7cb97d4b`, adapted                      | PostgreSQL threshold, drain-and-resume tests observed red  |
| [#19](https://github.com/giorgosg/bitmagnet/pull/19) | Allow concurrent local classifier searches                                  | lodestone `4785219e4`, adapted                   | Configured-capacity and invalid-value tests observed red   |

## Deferred or already resolved

- niklas2233 `0b3f1480b` edits only `categories.gen.go`; its generator was removed from
  the repository. Do not hand-edit the generated file. Revisit only with a maintained
  source-of-truth and reproducible generator.
- kawaii-not-kawaii `236c129d7` assumes `copyMagnet` clipboard call sites that do not
  exist on this UI baseline. Porting it would introduce a new feature, not repair a
  present behavior.
- lodestone `2e31cae01` fixes a slice-index map lookup in a later triage refactor.
  `trunk` already iterates the keyed hash map correctly, so this candidate is obsolete.
- niklas2233 `a6f69bf23` raises the aggregation budget tenfold. It needs a representative
  PostgreSQL accuracy/performance measurement before becoming a code change.
- o51r15 `9e564ff9e` claims a ticker-reset data race but has no failing reproduction. Fold
  any proven scheduling issue into a separately tested queue change.
- o51r15 `a2ee2958e` removes `router.bittorrent.cloud`; that node is already absent from
  `trunk`.
- Upstream [#514](https://github.com/bitmagnet-io/bitmagnet/pull/514) makes the per-IP DHT
  limiter configurable and remains open. Avoid carrying a duplicate unless upstream
  stalls or the global concurrency work establishes a combined configuration design.
