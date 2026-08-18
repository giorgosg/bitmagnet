# ghobs91/lodestone

<https://github.com/ghobs91/lodestone> · remote `lodestone` · branch `main`

**41 unique commits, 1 upstream commit missing** (`e31b30d`, the go-resty/TMDB fix).
Active through 2026-08-06 — the most recently active fork with substantive work.

A rebranded downstream product (Go module is `github.com/ghobs91/lodestone`), but the
rename is confined to a handful of commits. The other ~30 commits are **pure
performance work on the upstream architecture** and are the best material available.

## Reading the diff

GitHub reports 209 commits behind — false. It rebases onto upstream rather than
merging, so SHAs differ; 208 of 209 upstream commits are present as equivalent patches.

The module rename touches import lines in ~300 Go files. Filter it out:

```bash
git diff --numstat -w upstream/main lodestone/main -- '*.go' | awk '$1+$2>15'
```

That leaves ~20 files of real change.

## The performance work

### Concurrency and locking

| Commit theme                                                            | Files                                                               |
| ----------------------------------------------------------------------- | ------------------------------------------------------------------- |
| Reduce lock contention, fix ticker race, use a worker pool              | `concurrency/`, `processor/`                                        |
| Batching channel and buffered concurrent channel rework                 | `concurrency/batching_channel.go`, `buffered_concurrent_channel.go` |
| Keyed limiter changes                                                   | `concurrency/keyed_limiter.go`                                      |
| Global random source for queue backoff (avoids `rand` mutex contention) | `queue/`                                                            |

### DHT crawler

- **Shard the bloom filter** + add a triage cache + non-blocking sends —
  `dhtcrawler/infohash_triage.go` (+113/−23), `blocking/manager.go` (+91/−45)
- **Global outbound rate limiters** for DHT and metainfo requests —
  `dhtcrawler/request_meta_info.go`, `crawler.go`
- **Reduce default crawler `ScalingFactor` from 10 to 2**
- Separate bloom filter _load_ from _persist_ to cut DB I/O
- Bloom filter rotation to stop false-positive degradation over time
- Parallelize peer metadata fetching
- `fix(dht)`: range-over-slice using index as map key in `infohash_triage` — a real bug

### Database and queue

- **Re-enable hybrid LISTEN/NOTIFY for queue job dispatch** — `queue/server/server.go`
  (+166/−…), the largest single change
- Batch queue job garbage collection to prevent lock contention
- Configure the Postgres connection pool size — `database/postgres/postgres.go`
- Wrap the triage DB query in a transaction for consistent reads
- **Fixed a `process_torrent` infinite loop bottleneck**

### Query and classifier

- **Sequential CTE strategy to avoid a double database query** — `gql/resolvers/`
- Hash-map lookup + cached Levenshtein normalization — `classifier/util.go` (+63/−5)
- Proportional Levenshtein threshold for content matching
- Aggregate seeder/leecher counts across independent sources — `model/torrents.go`
- In-process LRU cache for TMDB responses — `tmdb/client_cached.go` (new, 119 lines)
- Batch importer items inline instead of per-item goroutines — `importer/importer.go`

### Also present (probably skip)

- `internal/settings` — runtime settings service with an HTTP handler, backing a
  classifier config GUI and a TMDB API key page
- `internal/importer/httpserver/sqlite_import.go` — overlaps o51r15's `dbimport`
- Rename, README/branding, docker-compose changes, prettier/eslint formatting sweeps

## Assessment

**Start here.** These are the changes with the clearest value and the least product
opinion attached — no new dependencies, no schema changes, no UI coupling. They target
exactly the parts of bitmagnet people complain about: DHT crawl throughput, queue
contention, and search query cost.

Caveats: none of it appears to have benchmarks or tests attached, so the claims are
unverified. The concurrency changes in particular (ticker race, worker pool, non-blocking
sends) deserve careful review before trusting — they are the kind of change that is easy
to get subtly wrong. Take them one at a time on separate topic branches.

Note the overlap: lodestone's global DHT rate limiter, o51r15's concurrency semaphore,
and open PR #514's configurable rate limit are three answers to the same problem.
