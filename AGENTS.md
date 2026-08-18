# Working on bitmagnet

Repository instructions for coding agents. This is the short operational guide; use the
documents under [`docs/`](docs/README.md) for the reasoning and longer examples.

## Before changing anything

1. Run `git status --short` and `git branch --show-current`. Preserve unrelated and
   untracked work.
2. Read the task-relevant documentation:
   - [`docs/hacking.md`](docs/hacking.md) for the toolchain, generators, and feature work.
   - [`docs/architecture.md`](docs/architecture.md) for packages and `uber-go/fx` wiring.
   - [`docs/git-workflow.md`](docs/git-workflow.md) before reviewing forks or PRs.
   - [`docs/upstream-status.md`](docs/upstream-status.md) and
     [`docs/forks/`](docs/forks/README.md) before choosing or porting community work.
   - [`docs/auth.md`](docs/auth.md) for anything that exposes or protects an interface.
3. If `CLAUDE.local.md` exists, read it for checkout-specific environment details and
   operator authority. Consult other `*.local.md` files only when relevant. These files
   are private working notes: never stage, commit, quote, or copy them into tracked files.

The dated fork and upstream surveys are snapshots. Re-run the commands in
[`docs/git-workflow.md`](docs/git-workflow.md) before relying on their counts or status.

## Generated files: change the source, never the output

The generated-files CI job regenerates everything, formats Go, extracts translations,
builds the web UI, and fails if the worktree changes. Use the narrow generator while
iterating and `task gen` when several outputs are involved.

| Do not hand-edit                           | Authoritative source                               | Refresh with                 |
| ------------------------------------------ | -------------------------------------------------- | ---------------------------- |
| `internal/gql/gql.gen.go`                  | `graphql/**/*.graphqls`, `internal/gql/gqlgen.yml` | `task gen-gql`               |
| `internal/database/dao/*.gen.go`           | `internal/model` and DAO generator config          | `task gen-gorm`              |
| `internal/gql/enums/enums.go`              | Go enum types                                      | `task gen-gql-enums`         |
| `internal/protobuf/bitmagnet.pb.go`        | `internal/protobuf/bitmagnet.proto`                | `task gen-protoc`            |
| `internal/**/mocks/*.go`                   | Interfaces and `.mockery.yml`                      | `task gen-mockery`           |
| `bitmagnet.io/schemas/classifier-0.1.json` | Classifier types                                   | `task gen-classifier-schema` |
| `webui/src/app/graphql/generated/**`       | GraphQL schema and web UI operations               | `task gen-webui-graphql`     |
| `webui/src/app/i18n/translations/*.json`   | `i18n` markup in web UI templates                  | `task i18n-extract`          |
| `webui/dist/**`                            | `webui/src`                                        | `task build-webui`           |

`webui/dist/**` is both ignored for new files and already committed to Git. Every web UI
change therefore needs a rebuilt `dist` committed. Its noisy diff, along with generated
GraphQL, translations, mocks, protobufs, and `*.gen.go`, should be excluded during the
first pass of a review but checked separately for consistency.

Markdown is checked by the repository's pinned Prettier. Use the installed copy, not a
fresh `npx prettier@3` resolution:

```bash
./webui/node_modules/.bin/prettier --write AGENTS.md
./webui/node_modules/.bin/prettier --check AGENTS.md
```

## Implementation workflow

Every observable behaviour change must ship with a test that was seen failing against
the unfixed code. Follow red, green, refactor: reproduce first, apply the smallest fix,
then run the focused test and the relevant broader suite. Do not weaken a test to make a
port or cherry-pick pass.

Large, lightly covered areas (`dhtcrawler`, `importer`, `processor`, and `blocking`) need
particular care. A clean cherry-pick, successful compile, or green existing suite is not
evidence that a concurrency or cache change is correct; add a focused regression test or
standalone reproduction.

For PostgreSQL integration tests, use `internal/database/dbtest`:

```go
db := dbtest.New(t) // db.Gorm, db.Query, db.Pool, db.DSN, db.Name
```

It creates and drops an isolated, fully migrated database. Tests skip when
`TEST_POSTGRES_DSN` is unset; CI supplies a PostgreSQL 16 service so they run there.

```bash
TEST_POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/postgres' go test ./...
```

### Project shapes to preserve

- A new subsystem normally consists of `internal/<feature>/config.go`, `factory.go`, its
  implementation, and `<feature>fx/module.go`, then one registration in
  `internal/app/appfx/module.go`. Copy `internal/tmdb`, `internal/torznab`, or
  `internal/worker` according to the feature shape.
- For GraphQL changes, edit `graphql/schema/*.graphqls`, run `task gen-gql`, implement the
  resolver under `internal/gql/resolvers`, and run `task gen-webui-graphql` if the UI
  consumes the change.
- Create migrations with `task create-migration NAME=<name>`. Never reuse a migration
  number already applied to a live database; fork migrations frequently collide. Run
  `task gen-gorm` when model changes affect generated DAOs.
- Upstream `main` has no authentication and permits `*` CORS. Treat any new HTTP,
  GraphQL, or Torznab surface as externally reachable unless the deployment provides a
  separate boundary.

## Commands and checks

`Taskfile.yml` is authoritative. `flake.nix` supplies Go, Node 22, golangci-lint, protoc,
Chromium on Linux, and the documentation toolchain; use `nix develop` or `direnv allow`
when the host lacks them. PostgreSQL is separate. Install web dependencies with
`task install-webui` before web checks.

```bash
task build            # Go build with version ldflags
task test-go          # go test -v ./...; DB tests skip without TEST_POSTGRES_DSN
task test-webui       # Angular tests; expects Chromium
task test             # Go and web UI tests
task lint             # web UI ESLint and Prettier
task lint-golangci    # separate; not included in task lint
task gen              # all code generators
task i18n-extract     # regenerate translations
task build-webui      # refresh committed webui/dist
task migrate          # apply Goose migrations; requires PostgreSQL
go build ./... && go vet ./...  # quick Go sanity check
```

Before handing off, run the smallest relevant checks plus all checks for the touched
area. For changes affecting generators, reproduce CI's clean-tree expectation after
generation (`git diff --exit-code`, excluding only
`webui/dist/bitmagnet/3rdpartylicenses.txt` as CI does). Report tests that could not run
or that skipped because PostgreSQL, Chromium, Node modules, or generator tooling was
unavailable.

CI additionally runs golangci-lint v2.1.6. A newer local version can report pre-existing
findings, so compare the changed-file or branch delta rather than "fixing" unrelated
code.

## Branches, ports, and reviews

- The current work is integration on `trunk`: evaluate selected commits from external
  forks and repositories, port each coherent change through a focused PR, and merge the
  reviewed result into `trunk`.
- Treat imported commits as candidates, not trusted patches. Review the substantive
  diff, verify the claimed behaviour, and add regression or integration tests whenever
  needed. Every observable behaviour change still follows the red-green requirement
  above.
- `main` is a pristine mirror of `upstream/main`; never commit directly to it.
- `trunk` is this fork's integration branch.
- Cut upstreamable topic branches from `main`, not `trunk`, so they contain only the
  intended change. Use `git cherry-pick -x` to retain provenance.
- Do not commit, push, or merge unless the user asks or grants standing authority in a
  local instruction file. Merge policy for this checkout, if any, is recorded in
  `CLAUDE.local.md`; absent explicit authority, stop before merging.
- For fork review, use patch equivalence (`git cherry`) and a whitespace-normalized,
  two-dot diff. GitHub ahead/behind, three-dot diffs, and raw diffstats are misleading
  for the rebased and CRLF-converted forks in this checkout.

```bash
git cherry upstream/main FORK/main
git diff -w --ignore-cr-at-eol upstream/main FORK/main -- \
  . ':(exclude)webui/dist' ':(exclude)webui/src/app/i18n/translations' \
  ':(exclude)go.sum' ':(exclude)*package-lock.json'
```

Read the entire substantive diff, not just commit messages. Keep ports focused, preserve
original attribution, renumber colliding migrations, and test each behavioural change
independently before integration.
