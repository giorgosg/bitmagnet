# Architecture

Notes on how upstream `main` is put together. Accurate as of `e31b30d` (2026-05-21).

## Shape

A single Go binary (`main.go`) that runs several long-lived workers plus an HTTP
server, backed by PostgreSQL. The Angular web UI is compiled and embedded into the
binary via `webui/embed.go`.

## Dependency injection: uber-go/fx

Everything is wired with [`uber-go/fx`](https://github.com/uber-go/fx). The convention
is rigid and worth internalising, because every fork that adds a feature follows it:

```
internal/<feature>/
    config.go          # config struct + defaults, registered with configfx
    factory.go         # fx.Provide constructors
    <feature>.go       # the actual implementation
    <feature>fx/module.go   # fx.Module("<feature>", ...) — the public entry point
```

Modules are assembled in `internal/app/appfx/module.go`. As of upstream `main`:

```
blockingfx  classifierfx  configfx    databasefx  dhtcrawlerfx  dhtfx
gqlfx       healthfx      httpserverfx importerfx loggingfx     metainfofx
metricsfx   processorfx   queuefx     telemetryfx tmdbfx        torznabfx
validationfx versionfx    workerfx
```

**Adding a feature = adding a `<feature>fx` module and one line in `appfx/module.go`.**
This is why fork features cherry-pick more cleanly than the raw diff sizes suggest —
they tend to be new directories plus a one-line registration, not edits threaded
through existing code.

## Packages

`internal/`:

| Package        | Role                                                                              |
| -------------- | --------------------------------------------------------------------------------- |
| `dhtcrawler`   | DHT crawling — the ingest path. Bloom-filtered infohash triage, metainfo requests |
| `protocol/dht` | BitTorrent DHT protocol: ktable, server, message encoding                         |
| `blocking`     | Bloom-filter-backed "have we seen this" manager                                   |
| `bloom`        | Stable bloom filter implementation                                                |
| `importer`     | Takes crawled torrents into the DB                                                |
| `processor`    | Post-import processing pipeline                                                   |
| `classifier`   | Rule-driven content classification (the `.ryml` workflow engine)                  |
| `tmdb`         | TMDB API client for metadata enrichment                                           |
| `queue`        | Postgres-backed job queue (`queue_jobs` table) with a manager and server          |
| `worker`       | Generic long-running worker abstraction                                           |
| `database`     | GORM setup; `database/dao` and `database/query` are **generated**                 |
| `model`        | GORM models                                                                       |
| `gql`          | gqlgen GraphQL server; `gql/gql.gen.go` is **generated** (~24k lines)             |
| `torznab`      | Torznab/Newznab indexer API — what Sonarr/Radarr/Prowlarr talk to                 |
| `httpserver`   | HTTP server plumbing; features register their own handlers                        |
| `webui`        | Embeds the built Angular app                                                      |
| `concurrency`  | Channel and limiter helpers — batching, keyed limiters, buffered channels         |
| `lazy`         | `lazy.Lazy[T]` — deferred construction, used heavily by fx factories              |

## Generated code

Do not hand-edit; regenerate instead. These files dominate diffs and are the main
source of review noise:

- `internal/gql/gql.gen.go` (~24k lines) — gqlgen
- `internal/database/dao/*.gen.go` — gorm/gen
- `internal/protobuf/bitmagnet.pb.go` — protoc
- `internal/**/mocks/*.go` — mockery (`.mockery.yml`)
- `webui/dist/**` — **65 build artifacts are committed to git**
- `webui/src/assets/i18n/*.json` — transloco extraction output

Exclude the last two from every fork diff or they drown everything else. See
[git-workflow.md](git-workflow.md).

## Interfaces

- **GraphQL** at `/graphql` — what the web UI uses. Schema in `graphql/schema/*.graphqls`,
  operations in `graphql/queries/` and `graphql/mutations/`.
- **Torznab** — the \*arr integration surface.
- **Web UI** — Angular 18, embedded in the binary.

There is **no authentication of any kind** on upstream `main`. Every interface is
open. See [auth.md](auth.md).

## Database

PostgreSQL only. Goose migrations in `migrations/` — 20 on upstream `main`, latest
`00020_bloom_filters_large_object.sql`.

⚠️ **Forks number migrations independently and they collide.** o51r15 adds `00023`,
gabriel20xx adds `00033`. Any two forks you merge will need their migrations
renumbered.

## CLI

`internal/app/cmd/`: `worker`, `process`, `reprocess`, `classifier`, `config`.
