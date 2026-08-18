# Hacking on bitmagnet

Orientation for someone making their first change — with or without an LLM assistant.
Read [architecture.md](architecture.md) alongside this; that covers _what_ the pieces
are, this covers _how to work on them_.

## Toolchain

`flake.nix` provides a dev shell (Go, Node 22, golangci-lint, goose, protoc). `direnv
allow` or `nix develop` gets you a working environment. Without Nix you need Go, Node 22,
PostgreSQL, and — only if you touch protobufs — `protoc`.

Everything is driven by [`Taskfile.yml`](../Taskfile.yml) (go-task, not `make`).

```bash
task build           # go build with version ldflags
task test            # test-go + test-webui
task test-go         # go test -v ./...
task lint            # webui + prettier
task lint-golangci   # commented out of `task lint` — the Nix package was broken
task migrate         # goose migrations up
task serve-webui     # Angular dev server on :3334, proxying a running bitmagnet
```

A running PostgreSQL is required for most of it. `docker-compose.yml` at the root brings
one up.

## The single most important thing: generated code

**A large fraction of this repo is generated, and several first-time PRs fail CI purely
because generated output wasn't refreshed.** Never hand-edit these; change the source and
regenerate.

| Generated                                  | Source                                              | Regenerate                   |
| ------------------------------------------ | --------------------------------------------------- | ---------------------------- |
| `internal/gql/gql.gen.go` (~24k lines)     | `graphql/**/*.graphqls` + `internal/gql/gqlgen.yml` | `task gen-gql`               |
| `internal/database/dao/*.gen.go`           | `internal/model`                                    | `task gen-gorm`              |
| `internal/gql/enums/*`                     | Go enum types                                       | `task gen-gql-enums`         |
| `internal/protobuf/bitmagnet.pb.go`        | `internal/protobuf/bitmagnet.proto`                 | `task gen-protoc`            |
| `internal/**/mocks/*.go`                   | interfaces + `.mockery.yml`                         | `task gen-mockery`           |
| `bitmagnet.io/schemas/classifier-0.1.json` | classifier types                                    | `task gen-classifier-schema` |
| `webui/src/app/graphql/generated/**`       | the GraphQL schema                                  | `task gen-webui-graphql`     |
| `webui/src/app/i18n/translations/*.json`   | `i18n` markup in templates                          | `task i18n-extract`          |
| `webui/dist/**` — **committed to git**     | `webui/src`                                         | `task build-webui`           |

`task gen` runs all of the code generators in the right order.

> **If you're using an LLM assistant, put this table in front of it.** The most common
> failure mode is an assistant editing `gql.gen.go` directly to make a schema change
> "work" — it compiles, it passes locally, and the next `task gen` silently reverts it.

`webui/dist` being tracked is the other trap: any UI change needs a rebuilt `dist`
committed, and that rebuild produces a large, meaningless-looking diff. When reviewing
someone else's branch, exclude it (see [git-workflow.md](git-workflow.md)).

## Adding a feature

The codebase is uniformly wired with `uber-go/fx`, and following the existing shape is
much easier than fighting it. To add a subsystem:

1. Create `internal/<feature>/` with `config.go`, `factory.go`, the implementation, and
   `<feature>fx/module.go` exporting an `fx.Module`.
2. Register it in `internal/app/appfx/module.go` — one line.
3. If it needs config, the struct in `config.go` is picked up automatically; defaults go
   in its constructor.
4. If it needs an HTTP surface, add a `<feature>/httpserver/` package that registers a
   handler, following `internal/torznab/httpserver/`.

`internal/prowlarr/`, `internal/rssfeed/`, `internal/omdb/` and `internal/tpdb/` in the
[o51r15 fork](forks/o51r15.md) are all textbook examples of this — worth reading before
writing your own, whatever you think of the features.

**Good models to copy:** `internal/tmdb` for an external API client with rate limiting
and a lazy requester; `internal/torznab` for an HTTP-facing subsystem; `internal/worker`
for something long-running.

## Changing the GraphQL API

1. Edit `graphql/schema/*.graphqls`.
2. `task gen-gql` — regenerates the Go server.
3. Implement the new resolver in `internal/gql/resolvers/`.
4. `task gen-webui-graphql` if the web UI consumes it.

Any third-party client generated from introspection will also need regenerating.

## Database changes

```bash
task create-migration NAME=add_my_thing   # goose -s create, in ./migrations
task migrate                              # apply
```

Then `task gen-gorm` if you changed `internal/model`.

⚠️ **Migration numbers collide across forks.** Upstream is at `00020`; forks in circulation
add `00023` and `00033`. If you cherry-pick a fork's migration, renumber it to the next
free slot in your own tree, and never reuse a number that has already been applied to a
live database.

## Testing at realistic scale

A dev database with a few thousand torrents behaves nothing like a real one. Query plans
in particular flip over completely: on a real instance `torrent_contents` is tens of
millions of rows with ~15 GB of indexes, and roughly half of those indexes are never
scanned at all.

If you can point at a populated instance, `EXPLAIN` (not `EXPLAIN ANALYZE`) is free and
tells you far more than a synthetic dataset. `task export-data` produces a portable
data-only dump if you want to seed a smaller one.

Be careful with instances you don't own — bitmagnet is frequently self-hosted on slow
hardware where a single `count(*)` on `torrents` is genuinely disruptive.

## Where the interesting problems are

If you're looking for something worth working on, the fork survey in
[forks/](forks/README.md) is essentially a map of what the community found worth fixing.
The recurring themes, independently arrived at by several people:

- **DHT crawl throughput** — rate limiting, bootstrap node health, bloom filter
  behaviour, concurrency caps on in-flight UDP
- **Queue contention** — `queue_jobs` bloat, garbage collection, polling vs LISTEN/NOTIFY
- **Over-indexing** — the default index set is expensive and largely unused for
  self-hosted workloads
- **Search and classification quality** — full-text lexemes, torznab category mapping,
  fuzzy matching cost
- **Authentication** — there is none; see [auth.md](auth.md)

## Contributing upstream

Upstream is slow-moving — see [upstream-status.md](upstream-status.md). Small, focused,
single-purpose PRs have historically fared best; several large ones have sat open for
over a year. Check the 30 open PRs before starting, since duplicate work is common
(Prowlarr integration has been implemented independently at least twice).

Note the `next` branch: a large in-flight rewrite that has been dormant since 2026-04. It
introduces a plugin system, a WASM runtime, and an auth stack. Worth knowing about before
investing in a large change to `main`.
