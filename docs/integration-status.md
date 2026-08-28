# Integration status

The ledger of downstream work evaluated for `trunk`: what was accepted, what is queued
next, and why plausible patches were turned down. Read the last section before starting
on a fork commit — several obvious-looking candidates are there with the measurement that
killed them. The fork pages under [forks/](forks/README.md) describe the source
repositories themselves.

Update this page whenever a candidate is merged, rejected, or superseded. A source commit
listed here is attribution and provenance, not permission to cherry-pick it without the
review in [porting.md](porting.md) and a test seen **red**.

## Integrated into trunk

| PR                                                   | Change                                                                      | Downstream source                                                    | Verification note                                              |
| ---------------------------------------------------- | --------------------------------------------------------------------------- | -------------------------------------------------------------------- | -------------------------------------------------------------- |
| [#1](https://github.com/giorgosg/bitmagnet/pull/1)   | CI action upgrades and manual trigger                                       | niklas2233 `6be9151cf`, `152d9a405`, `b09aacad5`                     | Full fork CI passed                                            |
| [#2](https://github.com/giorgosg/bitmagnet/pull/2)   | PostgreSQL pool tuning, hybrid queue dispatch, batched queue GC, TMDB cache | selected ghobs91/lodestone commits                                   | Added queue, cache, and PostgreSQL regression coverage         |
| [#3](https://github.com/giorgosg/bitmagnet/pull/3)   | Queue fetch expression index                                                | o51r15 `6ee9ec545`                                                   | PostgreSQL plan test observed red before the migration         |
| [#4](https://github.com/giorgosg/bitmagnet/pull/4)   | Torznab category disjunction fix                                            | niklas2233 `77b7920f4`                                               | PostgreSQL regression observed red                             |
| [#5](https://github.com/giorgosg/bitmagnet/pull/5)   | DHT bootstrap interval and empty-table recovery                             | o51r15 `9711ecbbb`, substantially rewritten                          | Deterministic config and routing-table tests observed red      |
| [#6](https://github.com/giorgosg/bitmagnet/pull/6)   | Integration-fork purpose in the README                                      | local                                                                | Documentation only                                             |
| [#7](https://github.com/giorgosg/bitmagnet/pull/7)   | Standalone season and episode search lexemes                                | niklas2233 `fbbcd4388`                                               | Search regression observed red                                 |
| [#8](https://github.com/giorgosg/bitmagnet/pull/8)   | Torznab resolution categories                                               | niklas2233 `1ae29df84`                                               | Category mapping regressions observed red                      |
| [#9](https://github.com/giorgosg/bitmagnet/pull/9)   | Canonical-ID Torznab searches and `tvdbid`                                  | niklas2233 `eb843dacc`                                               | Both behaviors observed red                                    |
| [#10](https://github.com/giorgosg/bitmagnet/pull/10) | Ignore duplicate importer queue jobs                                        | o51r15 `dd314bf47`                                                   | PostgreSQL regression observed red                             |
| [#11](https://github.com/giorgosg/bitmagnet/pull/11) | Exclude BEP-47 padding from the file threshold                              | o51r15 `5efcbd1c5`, rewritten                                        | Regression observed red and passed under the race detector     |
| [#12](https://github.com/giorgosg/bitmagnet/pull/12) | Start this ledger, and the fork survey it points at                         | local                                                                | Documentation only                                             |
| [#13](https://github.com/giorgosg/bitmagnet/pull/13) | Scope null-content reprocessing correctly                                   | kawaii-not-kawaii `f5f027a45`, adapted                               | PostgreSQL behavior regression observed red                    |
| [#14](https://github.com/giorgosg/bitmagnet/pull/14) | Classify title-only video torrents                                          | niklas2233 `dca622876`, adapted                                      | Video and non-video title-only fixtures observed red           |
| [#15](https://github.com/giorgosg/bitmagnet/pull/15) | Preserve rule-derived types without file evidence                           | kawaii-not-kawaii `68bcddf43`, adapted                               | Processor integration regressions observed red                 |
| [#16](https://github.com/giorgosg/bitmagnet/pull/16) | Cap global in-flight DHT queries                                            | o51r15 `727328128`, redesigned                                       | Deterministic concurrency and fallback tests observed red      |
| [#17](https://github.com/giorgosg/bitmagnet/pull/17) | Pause DHT classification at queue capacity                                  | o51r15 `f7cb97d4b`, adapted                                          | PostgreSQL threshold, drain-and-resume tests observed red      |
| [#18](https://github.com/giorgosg/bitmagnet/pull/18) | Record the second integration batch                                         | local                                                                | Documentation only                                             |
| [#19](https://github.com/giorgosg/bitmagnet/pull/19) | Allow concurrent local classifier searches                                  | lodestone `4785219e4`, adapted                                       | Configured-capacity and invalid-value tests observed red       |
| [#20](https://github.com/giorgosg/bitmagnet/pull/20) | Record the classifier concurrency integration                               | local                                                                | Documentation only                                             |
| [#21](https://github.com/giorgosg/bitmagnet/pull/21) | Normalize IPv4-mapped DHT socket addresses                                  | upstream #510 `20f99d59d`                                            | Socket conversion regression observed red                      |
| [#22](https://github.com/giorgosg/bitmagnet/pull/22) | Parse alternative `S4 - 02` episode syntax                                  | upstream #500 `da043e421`, adapted                                   | Parser regression observed red; omitted call site repaired     |
| [#23](https://github.com/giorgosg/bitmagnet/pull/23) | Add AV1 codec support                                                       | upstream #515 `605de43a3`                                            | Inference regression observed red; generators verified         |
| [#24](https://github.com/giorgosg/bitmagnet/pull/24) | Cache Go module downloads in the Docker build                               | upstream #513 `ba737ad25`                                            | Equivalent build and full CI passed                            |
| [#25](https://github.com/giorgosg/bitmagnet/pull/25) | Record the upstream improvement batch                                       | local                                                                | Documentation only                                             |
| [#26](https://github.com/giorgosg/bitmagnet/pull/26) | Preserve compact season ranges                                              | local — regression from #22                                          | Parser regression observed red                                 |
| [#27](https://github.com/giorgosg/bitmagnet/pull/27) | Correct the `next` auth dependency map in docs/auth.md                      | local                                                                | Documentation only                                             |
| [#28](https://github.com/giorgosg/bitmagnet/pull/28) | Port the `upstream/next` auth stack                                         | `upstream/next`, adapted; Torznab from kawaii-not-kawaii `172a784d3` | Three review passes; every fix observed red                    |
| [#29](https://github.com/giorgosg/bitmagnet/pull/29) | Update the Nix dev shell to nixpkgs 26.05                                   | local                                                                | Full CI passed                                                 |
| [#30](https://github.com/giorgosg/bitmagnet/pull/30) | Run checks and CodeQL on trunk pushes and PRs                               | local                                                                | CI configuration; verified by its own run                      |
| [#31](https://github.com/giorgosg/bitmagnet/pull/31) | Close every called vulnerability; Go floor to 1.25                          | local                                                                | `govulncheck` clean; full suite passed                         |
| [#32](https://github.com/giorgosg/bitmagnet/pull/32) | Source golangci-lint from the pinned toolchain                              | local                                                                | Fixes a linter/compiler mismatch CI hit                        |
| [#33](https://github.com/giorgosg/bitmagnet/pull/33) | Build both container images on pull requests                                | local                                                                | CI configuration; verified by its own run                      |
| [#34](https://github.com/giorgosg/bitmagnet/pull/34) | Migrate wsl to wsl_v5                                                       | local                                                                | Lint only; 0 issues                                            |
| [#35](https://github.com/giorgosg/bitmagnet/pull/35) | Bump the routine Go dependencies                                            | local                                                                | Full suite passed                                              |
| [#36](https://github.com/giorgosg/bitmagnet/pull/36) | Bump the code generation tools and regenerate                               | local                                                                | Generators re-run; clean-tree diff verified                    |
| [#37](https://github.com/giorgosg/bitmagnet/pull/37) | Resolve the three open CodeQL alerts                                        | local                                                                | CodeQL clean                                                   |
| [#38](https://github.com/giorgosg/bitmagnet/pull/38) | Validate the peer-supplied metainfo piece index                             | local — review finding 0001, 0008a                                   | Out-of-range piece **panicked** before the fix                 |
| [#39](https://github.com/giorgosg/bitmagnet/pull/39) | Return the delete error from `rbac.DeleteRole`                              | local — review finding 0005                                          | Returned nil on a failed delete; observed red                  |
| [#40](https://github.com/giorgosg/bitmagnet/pull/40) | Remove the unreachable `activeImport.ImportErrors`                          | local — review finding 0008f                                         | Dead code; no behaviour change                                 |
| [#41](https://github.com/giorgosg/bitmagnet/pull/41) | Shut down via fx instead of panicking on a serve error                      | local — review finding 0008d                                         | Panicked on the error path; observed red                       |
| [#42](https://github.com/giorgosg/bitmagnet/pull/42) | Stop rewriting the bloom filter on the blocklist read path                  | local — review finding 0006                                          | Large-object row versions changed; observed red                |
| [#43](https://github.com/giorgosg/bitmagnet/pull/43) | Bound the queue metrics query with a deadline                               | local — review finding 0008c                                         | Ran without a deadline; observed red                           |
| [#44](https://github.com/giorgosg/bitmagnet/pull/44) | Exclude padding files from `files_count`                                    | local — review finding 0007                                          | Counted 100 where 50 was correct; observed red                 |
| [#45](https://github.com/giorgosg/bitmagnet/pull/45) | Narrow the default CORS headers and turn debug off                          | local — review finding 0009, in part                                 | Invented header was allowed; observed red                      |
| [#46](https://github.com/giorgosg/bitmagnet/pull/46) | Record PRs #29-#45 in this ledger                                           | local                                                                | Documentation only                                             |
| [#47](https://github.com/giorgosg/bitmagnet/pull/47) | Document how `docs/` is used; require a docs update per PR                  | local                                                                | Documentation only                                             |
| [#48](https://github.com/giorgosg/bitmagnet/pull/48) | Serve an alternative web UI from a configured directory                     | local                                                                | Tests written first; verified against a real external UI build |
| [#49](https://github.com/giorgosg/bitmagnet/pull/49) | Disable browser caching for the alternative UI mount                        | local                                                                | Asset and SPA fallback regressions observed red                |
| [#50](https://github.com/giorgosg/bitmagnet/pull/50) | Remove redundant tests and reduce CI runner usage                           | local                                                                | Full suite and revised workflows passed                        |
| [#51](https://github.com/giorgosg/bitmagnet/pull/51) | Add the browser session cookie contract                                     | local                                                                | Real Gin/gqlgen/PostgreSQL regression observed red             |
| [#52](https://github.com/giorgosg/bitmagnet/pull/52) | Resolve browser cookie credentials safely                                   | local                                                                | Gin/gqlgen/PostgreSQL regressions observed red                 |
| [#53](https://github.com/giorgosg/bitmagnet/pull/53) | Protect cookie-authenticated GraphQL mutations from CSRF                    | local                                                                | Credential-source and transport regressions observed red       |
| [#54](https://github.com/giorgosg/bitmagnet/pull/54) | Make GraphQL `self` a recovery boundary                                     | local                                                                | Identity/permission/logout regressions observed red            |
| [#55](https://github.com/giorgosg/bitmagnet/pull/55) | Add stable GraphQL authentication error codes                               | local                                                                | Error-code/metadata/redaction regressions observed red         |
| [#56](https://github.com/giorgosg/bitmagnet/pull/56) | Return the current `lastLoginAt` from login                                 | local                                                                | Login/identity timestamp mismatch observed red                 |
| [#57](https://github.com/giorgosg/bitmagnet/pull/57) | Sign off the browser authentication contract                                | local                                                                | Full matrix passed; regressions covered by #51–#56             |
| [#58](https://github.com/giorgosg/bitmagnet/pull/58) | Close the browser authentication integration ledger                         | local                                                                | Documentation only                                             |
| [#59](https://github.com/giorgosg/bitmagnet/pull/59) | Correct four stale doc pages found by the fork review                       | local                                                                | Documentation only                                             |
| [#60](https://github.com/giorgosg/bitmagnet/pull/60) | Reload the blocklist bloom filter off the manager mutex                     | local — completes #42                                                | Concurrency regression observed red                            |
| [#62](https://github.com/giorgosg/bitmagnet/pull/62) | Stop re-logging the bootstrap admin invitation on every boot                | local                                                                | Credential-in-log regression observed red                      |

## In flight

Nothing currently in flight.

## The static review findings

A full static review of the Go tree (2026-08-22) produced eleven findings, kept as
untracked notes under `docs/issues/`. PRs #38-#45 above close six of them, plus four of
the small defects collected in 0008. What remains open there, hardest last:
`files_count`/`size` consistency for rows already written, the breaking half of the CORS
decision (same-origin default), the crawler and importer shutdown
paths, the queue job that runs inside its claiming transaction, the search SQL
re-execution, and the two serialisation points in the auth path.

One finding was measured and rejected rather than fixed: the crawler's periodic queue
depth `COUNT` costs 0.065 ms in steady state on the reference instance, and a
`LIMIT`-bounded rewrite measures 11.3 ms against 12.9 ms at 120,000 pending rows - it
still scans `max_queue_depth + 1` index entries, so it is not constant-time where it
would matter.

## Next candidate

lodestone `751a09607` / integration candidate `f32ebd2f0` — race metadata requests across
a bounded number of peers. Keep the idea, but redesign the implementation: the candidate
delays result consumption while filling semaphore slots, and its cancellation branch does
not exit the enclosing loop. Cover prompt first-success return, the global concurrency
bound, cancellation, all-failure behavior, and banned metadata.

## Rejected, deferred, or already resolved

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
