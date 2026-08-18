# bitmagnet — working notes

Notes on the state of [bitmagnet](https://github.com/bitmagnet-io/bitmagnet) and its
ecosystem, written for anyone trying to get into hacking on it.

Upstream development is slow-moving, and a fair amount of real work has accumulated in
community forks and unmerged PRs instead. These pages map that out: where the code is,
what's already been fixed by someone, and what's worth picking up.

They are _working notes_, not user documentation — the user docs live in `bitmagnet.io/`
and at [bitmagnet.io](https://bitmagnet.io).

## Start here

| Doc                                      | What's in it                                                          |
| ---------------------------------------- | --------------------------------------------------------------------- |
| [hacking.md](hacking.md)                 | Making your first change: toolchain, generated code, adding a feature |
| [architecture.md](architecture.md)       | How the Go application is wired together                              |
| [upstream-status.md](upstream-status.md) | State of upstream `main`, the `next` rewrite, and the PR queue        |
| [forks/](forks/README.md)                | What the community forks contain, and which parts are worth taking    |
| [auth.md](auth.md)                       | bitmagnet has no authentication — the three available implementations |
| [git-workflow.md](git-workflow.md)       | Reviewing forks and PRs without drowning in generated diffs           |

## If you're using an LLM assistant

Two pages carry most of the value, because they cover the things an assistant will
otherwise get confidently wrong:

- [hacking.md](hacking.md#the-single-most-important-thing-generated-code) — the table of
  generated files. Assistants routinely edit `gql.gen.go` by hand to make a schema change
  compile; the next `task gen` silently undoes it.
- [git-workflow.md](git-workflow.md#three-analysis-gotchas) — why GitHub's ahead/behind
  counts, three-dot diffs, and raw diffstats all give wrong answers for these particular
  forks.

## Status

Fork and PR survey carried out 2026-08-18, against upstream `main` at `e31b30d`
(2026-05-21). Divergence figures are patch-equivalence based and were accurate that day;
the active forks move, so re-run the commands in
[git-workflow.md](git-workflow.md) rather than trusting the numbers indefinitely.

Anything named `*.local.md` is gitignored at any depth, which is where
deployment-specific measurements and personal plans go. That keeps the pages above
generally useful while leaving somewhere obvious to put notes that aren't. Nothing is
missing from this directory as a result — the local files are purely additive.
