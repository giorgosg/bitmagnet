# Processing — queue, processor, classifier

The half of the system that decides what a torrent _is_. A crawled torrent is a name, a
size and a file list; a processed one is a `torrent_contents` row with a content type, a
title, episodes, video attributes, languages and a full-text vector.

## The queue

`internal/queue` is a job queue in a single Postgres table, `queue_jobs`. There is no
Redis, no AMQP, and no separate process.

- **`queue/manager`** — enqueues. A job carries `queue`, `payload`, `priority`,
  `run_after`, `max_retries`, `deadline`, `archival_duration`, and a **fingerprint** with
  a unique constraint, so re-enqueueing identical pending work is a no-op via
  `ON CONFLICT DO NOTHING`.
- **`queue/handler`** — a `Handler` is a queue name, a concurrency (default `NumCPU`), a
  check interval (default 30 s), a job timeout (default 30 s) and a `Func`.
  `handler.Exec` runs the `Func` in a goroutine behind that timeout and **recovers
  panics**, turning them into job errors with a file:line. Note that a job which exceeds
  its timeout has its goroutine abandoned, not cancelled — the `Func` must honour ctx.
- **`queue/server`** ([`server.go`](../../internal/queue/server/server.go)) — the worker.

The server is a hybrid: a `LISTEN`/`NOTIFY` connection wakes a handler the instant a job
is inserted, and a ticker polls at `CheckInterval` as a safety net. A handler that finds a
job immediately resets its ticker to fire again, so a backlog drains at full speed.

Claiming a job is `SELECT … FOR UPDATE SKIP LOCKED` inside a transaction, which is what
makes multiple bitmagnet processes against one database safe. **The handler runs inside
that transaction** — see the issue notes below for why that matters
under load.

Failed jobs go to `retry` with `queue.CalculateBackoff(retries)` until `max_retries`, then
`failed`. A garbage collector deletes `processed`/`failed` rows past their
`archival_duration` in batches of 1000.

## The processor

`internal/processor` handles the `classification` queue. `Process(ctx, MessageParams)`
takes a batch of infohashes and:

1. Loads the torrents with files, hint and sources preloaded, plus any existing
   `torrent_contents` rows. Hashes that do not exist come back as `MissingInfoHashes`.
2. Carries forward a prior _sourced_ match (one with a `content_source`, i.e. TMDB) as a
   hint, so re-processing does not re-query external APIs. A rule-inferred type with no
   source is retained only when there is no stored file list to re-derive it from.
3. Runs the classifier per torrent, **one goroutine each**, collecting results under a
   mutex.
4. Deletes superseded `torrent_contents` rows (the row ID is derived from the content
   identity, so a re-classification that changes the match changes the ID), honours
   `ErrDeleteTorrent`, and applies tags.
5. Re-enqueues everything that failed, as a fresh job.

`ClassifyMode` (`rematch`) and `ClassifierFlags` on the message are how the `reprocess`
CLI command and the GraphQL `torrent.reprocess` mutation steer this.

## The classifier

`internal/classifier` is a small interpreted rule engine. It is the most unusual package
in the tree and the one most worth reading before changing.

- **The workflow** is declarative YAML. `classifier.core.yml` is embedded and is the
  default; an operator's file is merged over it. `Config.Workflow` names the entry point.
- **Actions** are the statements: `if_else`, `run_workflow`, `set_content_type`,
  `parse_video_content`, `parse_date`, `find_match`, `attach_tmdb_content_by_search`,
  `attach_local_content_by_id`, `add_tag`, `delete`, … one file per action, all named
  `action_*.go`.
- **Conditions** are the expressions: `and`, `or`, `not`, and `expression`, which is
  [CEL](https://cel.dev) (`cel_env.go`, `cel_lists.go`). The torrent is exposed to CEL as
  a protobuf message built by `internal/protobuf/transformer.go`, which is why that
  package exists.
- **Flags** are typed, declared in the workflow, given values at compile time and
  overridable per job (`flag.go`, `flag_type.go`).
- **The JSON schema** for the whole configuration is generated from these types into
  `bitmagnet.io/schemas/classifier-0.1.json` (`task gen-classifier-schema`), which is what
  gives operators editor completion.

Two semaphores bound the work: `Concurrency` (default 10) over rule execution and
`SearchConcurrency` (default 5) over external searches — `runner_semaphore.go` and
`search_semaphore.go`. `internal/tmdb` is the external client behind the latter.

`internal/keywords`, `internal/model/episodes_parser.go` and `classifier/parsers/` hold
the actual parsing: release groups, episode ranges, dates, resolutions, codecs.

---

_Known defects and improvement ideas referenced above are kept as untracked `docs/issues/*.local.md` notes, which a given checkout may or may not have._
