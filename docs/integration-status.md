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
| [#21](https://github.com/giorgosg/bitmagnet/pull/21) | Normalize IPv4-mapped DHT socket addresses                                  | upstream #510 `20f99d59d`                        | Socket conversion regression observed red                  |
| [#22](https://github.com/giorgosg/bitmagnet/pull/22) | Parse alternative `S4 - 02` episode syntax                                  | upstream #500 `da043e421`, adapted               | Parser regression observed red; omitted call site repaired |
| [#23](https://github.com/giorgosg/bitmagnet/pull/23) | Add AV1 codec support                                                       | upstream #515 `605de43a3`                        | Inference regression observed red; generators verified     |
| [#24](https://github.com/giorgosg/bitmagnet/pull/24) | Cache Go module downloads in the Docker build                               | upstream #513 `ba737ad25`                        | Equivalent build and full CI passed                        |

## Next candidates

In suggested evaluation order:

1. lodestone `751a09607` / integration candidate `f32ebd2f0` — race metadata requests
   across a bounded number of peers. Keep the idea, but redesign the implementation: the
   candidate delays result consumption while filling semaphore slots and its cancellation
   branch does not exit the enclosing loop. Cover prompt first-success return, the global
   concurrency bound, cancellation, all-failure behavior, and banned metadata.
2. Map and port the `upstream/next` auth architecture onto the `main` lineage. Preserve
   its identity, user, API-key, JWT, and RBAC boundaries; add a focused Torznab API-key
   adapter because `next` does not currently wire auth into that surface. Use the two
   main-lineage implementations as integration-test references, not as the design basis;
   see [auth.md](auth.md).

## Deferred or already resolved

- niklas2233 `0b3f1480b` edits only `categories.gen.go`; its generator was removed from
  the repository. Do not hand-edit the generated file. Revisit only with a maintained
  source-of-truth and reproducible generator.
- kawaii-not-kawaii `236c129d7` assumes `copyMagnet` clipboard call sites that do not
  exist on this UI baseline. Porting it would introduce a new feature, not repair a
  present behavior.
- lodestone `2e31cae01` fixes a slice-index map lookup in a later triage refactor.
  `trunk` already iterates the keyed hash map correctly, so this candidate is obsolete.
- lodestone `e8dd5f02a` wraps the single triage SELECT in a transaction for a "consistent
  snapshot". Under READ COMMITTED — the server default here, and what GORM's `Transaction`
  inherits when called without `*sql.TxOptions` — one statement already snapshots at
  statement start, so the wrapper gains no consistency. Nor could any isolation level
  deliver its stated goal of not missing recently persisted torrents: a transaction's
  snapshot is only ever the same age or older, never fresher. It does add a BEGIN/COMMIT
  round trip per batch and pins a pooled connection for the query. Rejected; revisit only
  for a multi-statement query or an explicit isolation change.
- niklas2233 `a6f69bf23` raises the aggregation budget tenfold. It needs a representative
  PostgreSQL accuracy/performance measurement before becoming a code change.
- o51r15 `9e564ff9e` claims a ticker-reset data race but has no failing reproduction. Fold
  any proven scheduling issue into a separately tested queue change.
- o51r15 `a2ee2958e` removes `router.bittorrent.cloud`; that node is already absent from
  `trunk`.
- Upstream [#514](https://github.com/bitmagnet-io/bitmagnet/pull/514) makes the per-IP DHT
  limiter configurable and remains open. Avoid carrying a duplicate unless upstream
  stalls or the global concurrency work establishes a combined configuration design.
- lodestone `81970ec13` resets the crawler's in-memory stable Bloom filter after a fixed
  insertion count. The existing `StableBloomFilter` already continuously evicts stale
  entries and converges on a bounded false-positive rate; a full reset would instead
  forget every recently seen hash at once. Rejected unless a reproducible failure shows
  the library's stable-point behavior is insufficient.
- lodestone `73d9f5223` sums seeder and leecher counts across sources. DHT scrapes,
  trackers, and imports can observe overlapping peer populations, so independence cannot
  be assumed and the sum can inflate rankings. Keep the conservative maximum unless a
  deduplication model or representative measurement supports another aggregation.
- lodestone `279f9731f` changes the fixed Levenshtein threshold to a proportional one.
  This is a classification-policy change, not merely a performance optimization. Compare
  it against the authoritative classifier corpus and inspect every changed result before
  deciding.
