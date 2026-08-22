# Working on bitmagnet

**This file is the single source of truth for working in this repo** — for a human, and
for any coding agent regardless of which one. Read it before changing anything: it is
short, and it carries the traps that decide whether a change survives `task gen` and CI,
the ones that look like they worked locally and are silently undone later.

Everything that does not belong in every session lives under [`docs/`](docs/README.md),
reached by the pointers below.

Agent harnesses look for different filenames — `AGENTS.md`, `CLAUDE.md`, and others. In
this repo **only this file has content**; every other such file is a one-line pointer
here. If you are adding or correcting an instruction, add it here. Do not copy guidance
into a harness-specific file, and do not let one drift into holding rules of its own: two
files that disagree are worse than one file nobody read.

## Which doc, and when

**Starting cold, or coming back to a part of the tree you have not touched?** Read
[docs/architecture/](docs/architecture/README.md) first. It is the map: one page per
subsystem, describing what each one does, how the pieces connect, and where a new thing
goes. It is written to get an agent oriented in one pass, and it is kept current with the
code.

| Read                                                     | When                                                                                  |
| -------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| [docs/hacking.md](docs/hacking.md)                       | Making a change: toolchain, feature shape, GraphQL, migrations, testing at real scale |
| [docs/architecture/](docs/architecture/README.md)        | Getting oriented: what each subsystem is, how it connects, where a change goes        |
| [docs/porting.md](docs/porting.md)                       | Measuring, reviewing, or cherry-picking from a fork or an upstream PR                 |
| [docs/forks/](docs/forks/README.md)                      | Choosing which downstream repo a change should come from                              |
| [docs/upstream-status.md](docs/upstream-status.md)       | Sending a change upstream, or weighing whether the `next` rewrite makes it moot       |
| [docs/integration-status.md](docs/integration-status.md) | Checking whether a candidate is already merged, queued, or rejected                   |
| [docs/auth.md](docs/auth.md)                             | Configuring or deploying authentication, or running behind a reverse proxy            |
| [docs/agents/](docs/agents/issue-tracker.md)             | Filing an issue, applying a triage label, or looking for domain docs                  |

Several of those pages are dated **snapshots** — the fork survey, the upstream survey,
every divergence count. Re-measure with the commands in [docs/porting.md](docs/porting.md)
before acting on a number.

`*.local.md` is gitignored at any depth and holds this checkout's private notes:
environment details, unreviewed findings, and whatever authority the operator has granted.
At the repo root, look for a local counterpart to this file — `AGENTS.local.md` or
`CLAUDE.local.md`, whichever the checkout uses — and read it if it exists; it is where
standing permissions such as "you may merge a reviewed PR" are recorded, and nothing
grants those in tracked files. Consult the others under `docs/` when the topic matches.
Leave all of them unstaged, unquoted, and uncopied into tracked files.

## Before the first edit

Run `git status --short` and `git branch --show-current`, and preserve unrelated and
untracked work you find.

## Trap 1: most of this repo is generated

Change the source and regenerate. Editing the output _looks_ like it worked — it
compiles, it passes locally — and the next `task gen` reverts it. CI regenerates
everything and fails on any resulting diff.

| Generated output                           | Source                                             | Regenerate with              |
| ------------------------------------------ | -------------------------------------------------- | ---------------------------- |
| `internal/gql/gql.gen.go` (~24k lines)     | `graphql/**/*.graphqls`, `internal/gql/gqlgen.yml` | `task gen-gql`               |
| `internal/database/dao/*.gen.go`           | `internal/model`                                   | `task gen-gorm`              |
| `internal/gql/enums/enums.go`              | Go enum types                                      | `task gen-gql-enums`         |
| `internal/protobuf/bitmagnet.pb.go`        | `internal/protobuf/bitmagnet.proto`                | `task gen-protoc`            |
| `internal/**/mocks/*.go`                   | interfaces and `.mockery.yml`                      | `task gen-mockery`           |
| `bitmagnet.io/schemas/classifier-0.1.json` | classifier types                                   | `task gen-classifier-schema` |
| `webui/src/app/graphql/generated/**`       | the GraphQL schema and web UI operations           | `task gen-webui-graphql`     |
| `webui/src/app/i18n/translations/*.json`   | `i18n` markup in web UI templates                  | `task i18n-extract`          |
| `webui/dist/**` — **committed to git**     | `webui/src`                                        | `task build-webui`           |

`task gen` runs every generator in order; use the narrow target while iterating.

`webui/dist` being tracked is the second half of this trap: every web UI change ships a
rebuilt `dist`, and that rebuild produces a huge, meaningless-looking diff. Exclude it,
the translations, and the rest of the generated output on a first review pass, then check
them separately for consistency.

## Trap 2: Prettier is pinned

`task lint` runs Prettier over the whole tree, and `.prettierignore` does not exclude
`*.md`, so markdown is linted too. Formatting differs between 3.x releases, so a stray
version passes locally and fails CI.

Always use the repo's own copy — the web UI's `^3.3.3`, installed by `task install-webui`.
`task lint-prettier` calls it by path for exactly this reason, and it is deliberately
absent from the Nix shell so nothing else can win on `PATH`. `npx prettier@3` resolves to
a later 3.x and is the specific mistake to avoid.

```bash
./webui/node_modules/.bin/prettier --write AGENTS.md 'docs/**/*.md'
```

## Trap 3: migration numbers collide across forks

Upstream is at `00020`; forks in circulation add `00023` and `00033`. Renumber anything
cherry-picked to the next free slot — `ls migrations/` for the current high-water mark,
which moves as this tree adds its own — and keep every number that has already been
applied to a live database.

```bash
task create-migration NAME=add_my_thing   # goose -s create, in ./migrations
```

That target shells out to a `goose` binary, which the Nix shell does **not** provide —
install it separately. `task migrate` needs no binary; it runs goose as a library through
`./internal/dev`.

Run `task gen-gorm` afterwards when the change also touches `internal/model`.

## Every change lands with a test seen **red**

An observable behaviour change ships with a test that was watched failing against the
unfixed code. Reproduce first, apply the smallest fix, then run the focused test and the
suite around it. A test written after the fix and never seen red proves nothing, and
weakening an existing test to make a port pass is not an option.

This bar exists because it has already been earned: cherry-picks in this repo have
compiled, vetted, and passed the whole suite while carrying a shutdown deadlock and a
cache that answered the wrong question. `dhtcrawler`, `importer`, `processor`, and
`blocking` have little coverage, so a change there gets a focused regression test or a
standalone reproduction.

PostgreSQL tests use `internal/database/dbtest`, which creates and drops an isolated,
fully migrated database per test:

```go
db := dbtest.New(t) // db.Gorm, db.Query, db.Pool, db.DSN, db.Name
```

They **skip** when `TEST_POSTGRES_DSN` is unset, so a bare `go test ./...` stays offline
and proves less than it appears to. CI supplies a PostgreSQL 16 service.

```bash
TEST_POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/postgres' go test ./...
```

## Shapes to follow

- **A subsystem** is `internal/<feature>/` with `config.go`, `factory.go`, the
  implementation, and `<feature>fx/module.go`, plus one registration line in
  `internal/app/appfx/module.go`. Copy `internal/tmdb` (external API client),
  `internal/torznab` (HTTP surface), or `internal/worker` (long-running).
- **A GraphQL change** edits `graphql/schema/*.graphqls`, runs `task gen-gql`, implements
  the resolver in `internal/gql/resolvers/`, then runs `task gen-webui-graphql` if the web
  UI consumes it.
- **A new interface is externally reachable.** Upstream `main` has no authentication and
  allows `*` CORS, so treat any new HTTP, GraphQL, or Torznab surface as open unless the
  deployment supplies its own boundary. See [docs/auth.md](docs/auth.md).

## Commands

[`Taskfile.yml`](Taskfile.yml) is authoritative — this lists only the behaviour it will
not tell you.

```bash
task test-go          # DB tests skip silently without TEST_POSTGRES_DSN
task test-webui       # Angular; needs Chromium and `task install-webui` first
task lint             # golangci-lint, web UI ESLint, and Prettier
task lint-golangci    # just the Go linter; must end in "0 issues."
task migrate          # needs a running PostgreSQL; docker-compose.yml brings one up
go build ./... && go vet ./...   # quickest Go sanity check
```

`flake.nix` supplies Go, go-task, Node 22, golangci-lint, protoc, and Chromium on Linux;
reach for `nix develop` or `direnv allow` when the host lacks them. PostgreSQL and `goose`
are separate, and so is Prettier — see Trap 2.

**Lint from the Nix shell, never from a golangci-lint on your `PATH`.** The linter has
to be built with a Go at least as new as the toolchain compiling the code, so the flake
pins both together and CI runs `task lint` inside `nix develop` rather than installing a
linter of its own. A mismatch does not produce a tidy error: too old and it refuses the
module outright, too new for the compiler and it panics mid-run. Version strings do not
settle it either — a `go install`ed binary reports the same version as the official
release while being built with a different Go.

Before handing off, run the smallest relevant checks plus everything covering the touched
area. After running a generator, reproduce CI's clean-tree expectation with `git diff
--exit-code`, excluding only `webui/dist/bitmagnet/3rdpartylicenses.txt` as CI does.
Report every check that skipped or could not run, and why.

## Branches

- **`main` is a pristine mirror of `upstream/main`.** Leave it untouched so
  `git merge --ff-only upstream/main` always works.
- **`trunk` is this fork's integration branch** — docs plus accepted patches. Current work
  is integration: evaluate selected commits from forks and upstream PRs, port each
  coherent change through a focused PR, merge the reviewed result here.
- **Cut a topic branch from `main`**, not `trunk`, so anything destined for an upstream PR
  contains only that change. `git cherry-pick -x` records provenance.
- **Committing, pushing, and merging wait for the user to ask**, or for standing authority
  recorded in a `*.local.md` file. Absent that, stop before merging and say so.

Treat an imported commit as a candidate, not a trusted patch: a fork's commit message
describes intent, not behaviour, and `perf:` has been observed on changes that alter
semantics. Read the whole substantive diff, verify the claimed behaviour, and give it the
red test above. [docs/porting.md](docs/porting.md) has the commands that make that diff
readable.
