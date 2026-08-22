# Conventions

The patterns to copy, and the two that will silently undo your work if you do not know
them. [AGENTS.md](../../AGENTS.md) is the operative version of the rules; this page
explains the shapes behind them.

## A subsystem

```
internal/<feature>/
    config.go               # config struct + NewDefaultConfig, registered via configfx
    factory.go              # the fx.Provide constructors
    <feature>.go            # the implementation
    <feature>fx/module.go   # fx.Module("<feature>", ...) — the public entry point
```

Plus **one line** in [`internal/app/appfx/module.go`](../../internal/app/appfx/module.go).

Three worked examples, each a different shape:

- `internal/tmdb` — an external API client.
- `internal/torznab` — an HTTP surface, including a nested `httpserver/` package that
  contributes an `httpserver.Option`.
- `internal/worker` + `internal/dhtcrawler` — a long-running worker.
- `internal/auth/authfx` — a module made of several sub-packages, using value groups so
  other modules contribute to it.

The consequence worth carrying into a review: **fork features cherry-pick far more
cleanly than their raw diff sizes suggest**, because a feature is usually a new directory
plus a registration line, not edits threaded through existing code.

## Idioms you will meet constantly

- `lazy.Lazy[T]` — defer construction to `OnStart`, so CLI commands do not open databases.
- fx value groups — the wiring is by tag, not by reference. Grep for the tag string, not
  for a caller.
- `internal/concurrency` — `BatchingChannel` (accumulate then emit a slice),
  `BufferedConcurrentChannel` (buffer + semaphore + goroutine per item), keyed limiters.
- `internal/model` null and enum types — `NullUint`, `NullString`, `NullContentType`, and
  the generated `*_enum.go` files. Every signature in the tree uses them.
- `internal/slice` and `internal/maps` — `Map`, `FlatMap`, and ordered map helpers, used
  in place of hand-rolled loops.
- Errors are wrapped with sentinel chains (`%w: %w: %w`) so a caller can ask both "which
  subsystem" and "which failure" — see `auth/api_key/errors.go` and how
  `identity/authenticator_api_key.go` interrogates them.

## Trap 1: most of this repo is generated

Change the source and regenerate. Editing the output _looks_ like it worked — it compiles
and passes locally — and the next `task gen` reverts it. CI regenerates everything and
fails on any diff.

| Generated                                                   | Source                                             | Command                      |
| ----------------------------------------------------------- | -------------------------------------------------- | ---------------------------- |
| `internal/gql/gql.gen.go` (~24k lines)                      | `graphql/**/*.graphqls`, `internal/gql/gqlgen.yml` | `task gen-gql`               |
| `internal/database/dao/*.gen.go`, `internal/model/*.gen.go` | `internal/model`                                   | `task gen-gorm`              |
| `internal/gql/enums/enums.go`                               | Go enum types                                      | `task gen-gql-enums`         |
| `internal/protobuf/bitmagnet.pb.go`                         | `internal/protobuf/bitmagnet.proto`                | `task gen-protoc`            |
| `internal/**/mocks/*.go`                                    | interfaces and `.mockery.yml`                      | `task gen-mockery`           |
| `bitmagnet.io/schemas/classifier-0.1.json`                  | classifier types                                   | `task gen-classifier-schema` |
| `webui/src/app/graphql/generated/**`                        | the schema and web UI operations                   | `task gen-webui-graphql`     |
| `webui/src/app/i18n/translations/*.json`                    | `i18n` markup in web UI templates                  | `task i18n-extract`          |
| `webui/dist/**` — **committed to git**                      | `webui/src`                                        | `task build-webui`           |

`task gen` runs them all in order; use the narrow target while iterating.

## Trap 2: Prettier is pinned, and it lints markdown

`.prettierignore` does not exclude `*.md`, and formatting differs between 3.x releases, so
a stray version passes locally and fails CI. Use the repo's own copy — deliberately absent
from the Nix shell so nothing else wins on `PATH`:

```bash
./webui/node_modules/.bin/prettier --write AGENTS.md 'docs/**/*.md'
```

## Trap 3: migration numbers collide across forks

See [data.md](data.md#migrations).

## Testing

Every observable behaviour change ships with a test that was **watched failing** against
the unfixed code. The bar exists because it has been earned: cherry-picks here have
compiled, vetted and passed the whole suite while carrying a shutdown deadlock and a cache
that answered the wrong question.

`dhtcrawler`, `importer`, `processor` and `blocking` have little coverage, so a change
there gets a focused regression test or a standalone reproduction.

Database tests **skip silently** without `TEST_POSTGRES_DSN`:

```bash
TEST_POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/postgres' go test ./...
```

## Lint

`golangci-lint` must come from `nix develop` — it has to be built with a Go at least as
new as the toolchain compiling the code, and a version string does not settle it. A clean
tree reports **0 issues**, so every finding is yours.
