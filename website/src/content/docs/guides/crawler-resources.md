---
title: Crawling and resource use
description: Understand discovery, classification backlogs, storage growth and crawler tuning.
---

Magnes discovers torrent metadata through the DHT, saves it in PostgreSQL, and queues
classification work. Discovery and classification run at different rates. A growing
catalogue does not mean every torrent is already searchable, and a healthy process can
still have a processing backlog.

## Establish a baseline

Start with the defaults and watch the instance over time. Use Status, Queue jobs and
statistics alongside your host's CPU, available memory, swap, free disk space and disk
latency. With Docker, `docker stats --no-stream` gives a snapshot of container resource
use. Monitor PostgreSQL as well as Magnes: its data, indexes and write-ahead log consume
storage, and it shares the machine's memory and I/O capacity.

There is no fixed catalogue size or memory figure that fits every deployment. File
metadata, classification, concurrent clients and the age of the index change the load.
Leave room for backups and upgrades rather than filling the database disk.

## Network and workers

The installation walkthrough publishes port 3334 for TCP and UDP; the DHT server's
default port is `dht_server.port: 3334`. Crawling also needs outbound access to bootstrap
nodes and peers. Confirm your host, container and router networking before interpreting
an idle crawler as a database problem. The HTTP listener on port 3333 serves the UI and
APIs; it is separate from the crawler's network traffic.

The `dht_crawler` worker discovers metadata and `queue_server` processes queued work.
The installation guide starts both with `--all`. For a deliberate serving-only instance,
start `http_server` instead; existing catalogue searches remain available, but it does
not discover new torrents or process queued classification jobs.

## Settings that affect load

| Key                                 | Default | Effect                                                                                                               |
| ----------------------------------- | ------- | -------------------------------------------------------------------------------------------------------------------- |
| `dht_crawler.scaling_factor`        | `10`    | Multiplies concurrency and buffer sizes in several crawler stages.                                                   |
| `dht_server.max_concurrent_queries` | `512`   | Caps in-flight outbound DHT queries.                                                                                 |
| `dht_crawler.save_files_threshold`  | `100`   | Maximum non-padding file records saved per multi-file torrent.                                                       |
| `dht_crawler.save_pieces`           | `false` | Whether piece metadata is persisted; enabling it adds storage.                                                       |
| `dht_crawler.max_queue_depth`       | `50000` | Pending and retry queue jobs at which the crawler stops enqueueing new classification work; `0` disables this check. |
| `dht_crawler.rescrape_threshold`    | `720h`  | Minimum age before an existing torrent is eligible for another peer-count scrape.                                    |

These are throughput and retention controls, not a total memory or disk limit. If a
small machine is under pressure, try a lower positive `scaling_factor`, such as `4`,
and compare throughput and resource use before changing another setting. Save the
configuration in `config/config.yml` with the Docker walkthrough and restart Magnes
to apply it. The value `4` is a trial setting, not a measured hardware requirement.

The file threshold keeps a partial list for larger torrents and records that the
threshold was exceeded. Raising it affects future metadata retrieval; it does not
immediately fill every existing partial list. Reprocessing classification alone does
not fetch those missing files. Peer counts are stored observations, not live checks
when you open a result.

## When classification falls behind

At the queue-depth threshold, the crawler continues saving torrent metadata while
skipping new classification jobs. The check uses a periodically refreshed count of
pending and retry jobs; it is not a strict queue-size ceiling or a torrent count.
Previously skipped torrents are not automatically queued just because the backlog
later shrinks.

Check that `queue_server` is running and inspect retry errors before increasing the
limit. Database load or a failing external classification API can cause the backlog.
Once the underlying issue is fixed and the queue has drained, use a narrow
[reprocessing selection](../reprocessing/) to pick up unclassified torrents. Avoid
queueing the entire catalogue while you are still diagnosing the bottleneck.

See [monitoring](../monitoring/), [configuration](../../reference/configuration/),
and [backups](../backup-upgrades/) for the related operator workflows.
