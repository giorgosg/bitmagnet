---
title: Reprocess existing torrents
description: Apply classification changes to a selected part of your index.
---

Changing classifier configuration affects subsequent classification. Existing torrents
need reprocessing to apply those rules again. A reprocessing job operates on stored
metadata; it does not crawl the network for missing files.

## Use queue administration

If discovery continued while the classification queue was full, some saved torrents
can have no searchable content record. Let the backlog drain before scheduling a
reprocessing batch for those records. See [crawler queue limits](../crawler-resources/)
for why those jobs were skipped.

The UI's queue administration page can schedule a reprocessing batch. It needs both
queue read and mutate Permissions. Choose the content types and whether to restrict
the batch to torrents without a searchable content record. Review the selection and
whether you are requesting a rematch before submitting.

Follow the resulting jobs in Queue jobs and the queue statistics. A submitted batch is
queued work, not a completed update. The queue server must be running to process it.
Purging jobs is a separate destructive operation; it is not required for every batch.

## Use the CLI

The CLI runs on a machine with access to the configured database. It queues work directly;
it does not use a browser session or HTTP API key. With the Docker walkthrough:

```sh
docker compose -f compose.magnes.yml exec magnes bitmagnet reprocess --help
```

Use `bitmagnet` below as shorthand for that command prefix. Start with a narrow selection:

```sh
bitmagnet reprocess --contentType=movie --apisDisabled
```

| Option                   | Meaning                                                                     |
| ------------------------ | --------------------------------------------------------------------------- |
| `--contentType=movie`    | Restrict selection by content type; the option can be repeated.             |
| `--contentType=null`     | Select content rows with unknown/null content type.                         |
| `--orphans`              | Select torrents with no `torrent_contents` record.                          |
| `--classifyMode=default` | Retain existing matches and attempt matching previously unmatched torrents. |
| `--classifyMode=rematch` | Ignore an existing match when constructing the classification input.        |
| `--apisDisabled`         | Disable external classifier API calls for this batch.                       |
| `--localSearchDisabled`  | Disable local content search for this batch.                                |
| `--classifierFlags`      | Supply JSON-encoded runtime classifier flags.                               |
| `--batchSize`            | Torrents per processing batch; default 100.                                 |
| `--chunkSize`            | Selection chunk size; default 10,000.                                       |

`null` and orphans describe different states. Use the selector that matches the problem.
Calling `bitmagnet reprocess` without filters selects the entire index. A rematch can
still match local content or call an external API unless those lookups are disabled.

## Check one torrent first

To process a stored infohash synchronously rather than queueing an index-wide batch:

```sh
bitmagnet process --infoHash=YOUR_40_CHARACTER_INFOHASH --apisDisabled
```

Replace the placeholder with an infohash already in the database. The `process` command
always rematches and can be given multiple `--infoHash` options. It writes classification
results; it is not a dry run. Test new rules against a restored database when you need a
reversible experiment.

On a large index, monitor queue growth, database load, and API limits before widening
the selection. Configured classifier deletion actions also run during reprocessing.
