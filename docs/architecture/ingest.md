# Ingest — the DHT crawler

Everything in `internal/dhtcrawler` plus the protocol packages it drives. This is the
path that turns "a stranger on the internet mentioned a hash" into rows in `torrents`.

**It has little test coverage.** AGENTS.md names it explicitly. A change here gets a
focused regression test or a standalone reproduction.

## The pipeline

`crawler.start()` ([`crawler.go`](../../internal/dhtcrawler/crawler.go)) launches
seventeen goroutines that pass work between typed channels from `internal/concurrency`.
Two channel kinds do all the work:

- **`BatchingChannel[T]`** — accumulates until a count or a timeout, then emits `[]T`.
  Used where the consumer wants a batch: node discovery, triage, both persist stages.
- **`BufferedConcurrentChannel[T]`** — a buffered channel plus a semaphore; `Run` spawns
  up to `concurrency` goroutines. Used where the consumer does network I/O.

Every buffer and concurrency figure is `Config.ScalingFactor` (default 10) multiplied by
a constant in [`factory.go`](../../internal/dhtcrawler/factory.go). That is the one knob
for the crawler's resource usage.

```
 kTable ──▶ getNodesForFindNode ────▶ nodesForFindNode ──▶ find_node ──┐
 kTable ──▶ getNodesForSample… ─────▶ nodesForSampleInfoHashes ────────┤
 DHT server + responses ────────────▶ discoveredNodes (batching) ──────┘
                                             │
                    sample_infohashes yields infohashes
                                             ▼
                                  ignoreHashes (in-memory bloom)
                                             │ not seen before
                                             ▼
                                  infoHashTriage (batching, 20s)
                        ┌────────────────────┼────────────────────┐
                   need metainfo         need scrape          discard
                        ▼                    ▼
                    getPeers ──▶ requestMetaInfo        scrape (BEP 33)
                                      ▼                      ▼
                             persistTorrents (batch)   persistSources (batch)
```

## The four filters, in order

An infohash is dropped at the first of these that claims it. Knowing the order explains
most "why was this torrent not indexed" questions:

1. **`ignoreHashes`** — a process-local `boom.StableBloomFilter` (10M cells) in
   `crawler.go`. `testAndAdd` under a mutex. Purely an optimisation to keep load off the
   database, but note that it is _stable_: entries evict over time, which is the only
   reason a hash dropped here is ever reconsidered.
2. **`blockingManager.Filter`** — the persisted blocklist, `internal/blocking`. Backed by
   a bloom filter stored as a Postgres large object, reloaded every five minutes to pick
   up blocks made by another process. That reload allocates and transfers 25 MB, so it
   happens off the manager's mutex: a caller that finds one already in flight is answered
   from the filter it has. Five minutes is already the staleness this filter permits.
3. **Triage** ([`infohash_triage.go`](../../internal/dhtcrawler/infohash_triage.go)) — one
   query joining `torrents` to `torrents_torrent_sources`. Four outcomes, documented in
   the function's doc comment: fetch metainfo, rescrape, or discard.
4. **`banning.Checker`** — applied to the _fetched_ metainfo in
   [`request_meta_info.go`](../../internal/dhtcrawler/request_meta_info.go). A ban adds
   the hash to the blocklist.

## Fetching metainfo

`internal/protocol/metainfo/metainforequester` implements BEP 9 over a raw TCP
connection: BitTorrent handshake, extension handshake, request every 16 KiB piece, read
them back, bencode-parse the result. `maxMetadataSize` is 10 MiB and bounds both the
declared metadata size and any single message.

Everything this package reads is attacker-controlled. It is the only place in the tree
where a remote party's bytes become array indices, and it runs on a crawler goroutine with
**no `recover()` anywhere in the path** — `BufferedConcurrentChannel.Run` does not install
one. A panic here takes the process down. See
the issue notes below.

## Persistence

[`persist.go`](../../internal/dhtcrawler/persist.go) is where the pipeline meets the
database, and it does more than its name suggests:

- `persistTorrentBatch` de-duplicates the batch, builds `Torrent`, `TorrentFile`,
  `TorrentsTorrentSource` and optionally `TorrentPieces` rows, and creates
  `queue_jobs` rows in batches of `classifyBatchSize` (100) — all in **one transaction**.
  On success it forwards each hash to the scrape channel.
- Files whose display path starts with `.pad/` are skipped (BEP 47 padding), while
  `FilesCount` is still derived from the full metainfo file list.
- `SaveFilesThreshold` (default 100) caps stored file rows; exceeding it sets
  `FilesStatus = over_threshold`, which triage reads back.
- `MaxQueueDepth` (default 50 000) is a backpressure valve: when `queue_jobs` has that
  many pending/retry rows, torrents are still persisted but **no classification job is
  created**. A `reprocess` run recovers them later. The depth is refreshed by a `COUNT`
  every 30 s.

`createTorrentSourceModel` derives seeders and leechers from the BEP 33 scrape bloom
filters' `ApproximatedSize()` — they are estimates, not counts.

## The routing table

`internal/protocol/dht/ktable` is a btree-backed Kademlia routing table fed by
`BatchCommand` (`PutNode`, `DropNode`, `PutHash`, `DropAddr`) from every stage above. The
crawler asks it for nodes to ping, to `find_node`, and to `sample_infohashes`;
`soughtNodeID` is a random target rotated every 10 seconds so the crawl walks the whole
keyspace rather than settling near the node's own ID.

`internal/protocol/dht/server` answers inbound DHT requests, and feeds nodes it learns
about into the same `discoveredNodes` channel — which is why that channel is provided
separately in `discovered_nodes.go`, to break the circular dependency.

---

_Known defects and improvement ideas referenced above are kept as untracked `docs/issues/*.local.md` notes, which a given checkout may or may not have._
