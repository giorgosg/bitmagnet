# bitmagnet — working notes

Reference for hacking on [bitmagnet](https://github.com/bitmagnet-io/bitmagnet) and for
mining its ecosystem. The rules that apply to every change live one level up in
[AGENTS.md](../AGENTS.md); these pages carry the reasoning, the measurements, and the
longer examples behind them.

Upstream development is slow-moving, and a fair amount of real work has accumulated in
community forks and unmerged PRs instead. Much of what follows maps that out: where the
code is, what someone has already fixed, and what is worth picking up.

These are _working notes_. The user documentation lives in `bitmagnet.io/` and at
[bitmagnet.io](https://bitmagnet.io).

## Which page, and when

| Read                                           | When                                                                                  |
| ---------------------------------------------- | ------------------------------------------------------------------------------------- |
| [hacking.md](hacking.md)                       | Making a change: toolchain, feature shape, GraphQL, migrations, testing at real scale |
| [architecture.md](architecture.md)             | Working out what a package does, or how `fx` assembles the binary                     |
| [porting.md](porting.md)                       | Measuring, reviewing, or cherry-picking from a fork or an upstream PR                 |
| [forks/](forks/README.md)                      | Choosing which downstream repo a change should come from                              |
| [upstream-status.md](upstream-status.md)       | Sending a change upstream, or weighing whether the `next` rewrite makes it moot       |
| [integration-status.md](integration-status.md) | Checking whether a candidate is already merged, queued, or rejected                   |
| [auth.md](auth.md)                             | Adding or protecting an externally reachable interface                                |
| [auth-port.md](auth-port.md)                   | Porting more of `next`, or reviewing a security-sensitive change                      |
| [agents/](agents/issue-tracker.md)             | Filing an issue, applying a triage label, or looking for domain docs                  |

## Snapshots expire

The fork survey, the upstream survey, and every divergence count on these pages are
**snapshots**, taken 2026-08-18 against upstream `main` at `e31b30d` (2026-05-21). The
figures were patch-equivalence based and accurate that day; the active forks move.
Re-measure with the commands in [porting.md](porting.md) before acting on a number.

Pages carrying snapshots say so at the top, with their date.
