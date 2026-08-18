# Working on bitmagnet

Guidance for AI assistants and new contributors. The long form lives in
[docs/](docs/README.md); this file is the part worth loading every time.

## Read first

- [docs/hacking.md](docs/hacking.md) — toolchain, generated code, adding a feature
- [docs/architecture.md](docs/architecture.md) — how the fx wiring fits together

## Traps, in order of how often they bite

**1. Much of this repo is generated. Never hand-edit it.**

`internal/gql/gql.gen.go` (~24k lines), `internal/database/dao/*.gen.go`,
`internal/protobuf/bitmagnet.pb.go`, `internal/**/mocks/*.go`, `webui/dist/**`, and
`webui/src/assets/i18n/*.json` are all build output. Change the source and run the
generator — `task gen` for all of it, or the individual targets in
[docs/hacking.md](docs/hacking.md#the-single-most-important-thing-generated-code).

Editing `gql.gen.go` directly to make a schema change compile _appears_ to work and is
silently reverted by the next `task gen`. CI runs the generators and fails on any diff.

**2. `webui/dist` is committed to git.** Any UI change needs a rebuilt `dist` committed,
which produces a huge and meaningless-looking diff. Exclude it when reading someone
else's branch.

**3. Markdown is linted.** `task lint` runs `prettier --check .` and `.prettierignore`
does not exclude `*.md`. Run `npx --yes prettier@3 --write` on docs before committing.

**4. Adding a subsystem means adding an fx module.** `internal/<feature>/` with
`config.go`, `factory.go`, `<feature>fx/module.go`, then one line in
`internal/app/appfx/module.go`. Follow the existing shape; `internal/tmdb` and
`internal/torznab` are good models.

**5. Migration numbers collide across forks.** Upstream is at `00020`; forks in
circulation add `00023` and `00033`. Renumber anything you cherry-pick.

## Commands

```bash
task build              # go build with version ldflags
task test               # go test ./... + webui tests
task gen                # run every code generator
task lint               # webui eslint + prettier
task migrate            # goose migrations up
go build ./... && go vet ./...   # fastest sanity check
```

A PostgreSQL instance is needed for most of it; `docker-compose.yml` brings one up.

## Repository conventions

- **`main` is a pristine mirror of upstream.** Never commit to it, so
  `git merge --ff-only upstream/main` always works.
- **`trunk` is the integration branch** — docs plus accepted patches.
- **Topic branches are cut from `main`**, not `trunk`, so anything destined for an
  upstream PR contains only that change.
- **`*.local.md` is gitignored at any depth.** Notes that shouldn't be published from a
  fork of a public repo go in `<topic>.local.md`.

## Reviewing forks and PRs

This repo has an unusually messy fork landscape, and the obvious commands give wrong
answers — GitHub's ahead/behind counts, three-dot diffs, and raw diffstats are all
misleading for forks that rebase or reformat. Read
[docs/git-workflow.md](docs/git-workflow.md#three-analysis-gotchas) before comparing
anything.

**A clean cherry-pick is not evidence of correctness.** Fork commit messages describe
intent, not behaviour; `perf:` has been observed on changes that alter semantics. Compile
it, run the tests, read the whole diff, and write a standalone repro for any concurrency
claim you are unsure about.
