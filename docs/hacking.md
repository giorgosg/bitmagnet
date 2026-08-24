# Hacking on bitmagnet

Orientation for a first change. [AGENTS.md](../AGENTS.md) has the rules — the generated
files, the pinned Prettier, the migration numbers, the red test — and this page has the
detail behind them. [architecture/](architecture/README.md) covers _what_ the pieces are;
this covers _how to work on them_.

## Toolchain

`flake.nix` provides a dev shell — Go, go-task, Node 22, golangci-lint, protoc,
Ruby/Jekyll for the docsite, and Chromium on Linux. Prettier is deliberately not in it;
the web UI's pinned copy is the only one that should ever run. `direnv allow` or `nix develop` gets
you a working environment. Without Nix you need Go, go-task, Node 22, PostgreSQL, and —
only if you touch protobufs — `protoc`.

Two things the shell does **not** supply: PostgreSQL, which most targets want and
`docker-compose.yml` at the root brings up, and a `goose` binary, which only
`task create-migration` needs.

Everything is driven by [`Taskfile.yml`](../Taskfile.yml) (go-task, not `make`). Two
targets worth knowing that AGENTS.md doesn't list:

```bash
task serve-webui     # Angular dev server on :3334, proxying a running bitmagnet
task serve-docsite   # the Jekyll site under bitmagnet.io/
```

The web UI has no standalone test target. Its former Karma specs only instantiated each
component and asserted that it existed, so they duplicated the production build while
providing no behavioral coverage. `task lint-webui` checks the TypeScript and templates;
`task build-webui` is the compile-time and bundling check run by CI.

## CI cost controls

The checks workflows cancel an older run when a newer commit arrives on the same ref.
GitHub pull requests are checked as synthetic merge commits, so the push produced by the
GitHub merge button skips the duplicate checks; a direct push still runs them.

Container builds are deliberately separate. They run when a Docker build input changes
and once a week to catch image or registry drift, rather than spending roughly ten runner
minutes on every source-only pull request. CodeQL scans Go and TypeScript on pull requests
and weekly; the docsite's only Ruby source is the 14-line schema-copy plugin and does not
justify a runner for every scan.

`serve-webui` is how to iterate on the web UI without rebuilding the committed
`webui/dist` on every change.

## Adding a subsystem

The codebase is uniformly wired with `uber-go/fx`, and following the existing shape is
much easier than fighting it. AGENTS.md gives the four files and the one registration
line; the parts that are easy to get wrong:

- **Config is picked up automatically.** The struct in `config.go` is registered with
  `configfx` by the module; defaults belong in its constructor, not in a separate
  defaults file.
- **An HTTP surface is its own package.** Add `<feature>/httpserver/` that registers a
  handler, following `internal/torznab/httpserver/`.
- **Copy the closest existing module rather than the smallest.** `internal/tmdb` for an
  external API client with rate limiting and a lazy requester; `internal/torznab` for an
  HTTP-facing subsystem; `internal/worker` for something long-running.

`internal/prowlarr/`, `internal/rssfeed/`, `internal/omdb/` and `internal/tpdb/` in the
[o51r15 fork](forks/o51r15.md) are textbook applications of this shape — worth reading
before writing your own, whatever you think of the features.

## Changing the GraphQL API

Beyond the schema-edit, `task gen-gql`, resolver, `task gen-webui-graphql` cycle in
AGENTS.md: any third-party client generated from introspection needs regenerating too.
The schema is the contract, and nothing outside this repo learns that it moved.

## Testing at realistic scale

A dev database with a few thousand torrents behaves nothing like a real one. Query plans
in particular flip over completely: on a real instance `torrent_contents` is tens of
millions of rows with ~15 GB of indexes, and roughly half of those indexes are never
scanned at all. A change that looks like a win on synthetic data can be measuring an
entirely different plan.

If you can point at a populated instance, `EXPLAIN` — not `EXPLAIN ANALYZE` — is free and
tells you far more than a synthetic dataset. `task export-data` produces a portable
data-only dump for seeding a smaller one.

Be careful with instances you do not own. bitmagnet is frequently self-hosted on slow
hardware, where a single `count(*)` on `torrents` is genuinely disruptive.

## Where the interesting problems are

The fork survey in [forks/](forks/README.md) is a map of what the community found worth
fixing. The recurring themes, arrived at independently by several people:

- **DHT crawl throughput** — rate limiting, bootstrap node health, bloom filter
  behaviour, concurrency caps on in-flight UDP
- **Queue contention** — `queue_jobs` bloat, garbage collection, polling vs LISTEN/NOTIFY
- **Over-indexing** — the default index set is expensive and largely unused for
  self-hosted workloads
- **Search and classification quality** — full-text lexemes, torznab category mapping,
  fuzzy matching cost
- **Authentication** — there is none; see [auth.md](auth.md)

[integration-status.md](integration-status.md) records which of these already reached
`trunk` and which were tried and rejected. Read it before starting: several plausible
patches are there with the reason they were turned down.

To send work upstream instead, see [upstream-status.md](upstream-status.md).
