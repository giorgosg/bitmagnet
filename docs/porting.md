# Porting from forks and upstream PRs

How to measure a fork honestly, read its diff without drowning in generated output, and
get one coherent change onto a topic branch. [AGENTS.md](../AGENTS.md#branches) owns the
branch rules and the red-test bar; this page is the commands.

## Remotes

Configured locally, not part of the repo — re-run on a fresh clone:

```bash
git remote add upstream https://github.com/bitmagnet-io/bitmagnet.git
git remote add o51r15            https://github.com/o51r15/bitmagnet.git
git remote add lodestone         https://github.com/ghobs91/lodestone.git
git remote add niklas2233        https://github.com/niklas2233/bitmagnet.git
git remote add kawaii-not-kawaii https://github.com/kawaii-not-kawaii/bitmagnet.git
git remote add gabriel20xx       https://github.com/gabriel20xx/bitmagnet.git
git remote add nigowl            https://github.com/nigowl/bitmagnet.git
git remote add dashed            https://github.com/dashed/bitmagnet.git
```

All upstream PR heads, without a remote per contributor:

```bash
git config --add remote.upstream.fetch '+refs/pull/*/head:refs/remotes/upstream-pr/*'
git fetch upstream
# every open PR is now available as upstream-pr/<number>
git log --oneline upstream/main..upstream-pr/510
```

`.git` is ~900 MB with all forks fetched.

## The three measurements that lie

All three have the same root cause: **most of these forks rebase onto upstream rather
than merging**, so their commits carry new SHAs and their merge-base with `main` is the
original 2023 fork point. Each of these has cost real time.

### 1. GitHub's ahead/behind

`behind_by` counts SHA reachability, so a fork that rebased shows as hundreds of commits
behind while containing all of upstream's work. lodestone reported _209 behind_ and was
missing exactly one upstream commit.

Measure patch equivalence instead:

```bash
git cherry upstream/main FORK/main | grep -c '^+'   # genuinely new work
git cherry FORK/main upstream/main | grep -c '^+'   # genuinely missing from upstream
```

### 2. Three-dot diffs

`git diff upstream/main...FORK/main` diffs from the **merge base**, which for these forks
is `ce8909db` (2023-10-05). You get three years of upstream history mixed into the fork's
changes.

**Use two-dot** — `git diff upstream/main FORK/main` — for every fork comparison.

### 3. Raw diffstats

Whitespace and line endings inflate everything. o51r15 converted the whole tree to CRLF:
its Go diff reads as 507 files / +77k / −72k, and **normalized** it is 65 files /
+4,920 / −26. lodestone renamed the Go module, so ~300 of its 319 changed Go files are
import-line churn.

Always pass `-w --ignore-cr-at-eol`.

## The review command

Go only, generated output excluded:

```bash
git diff -w --ignore-cr-at-eol --stat upstream/main FORK/main -- \
  '*.go' ':(exclude)*.gen.go' ':(exclude)*/mocks/*' ':(exclude)*.pb.go'
```

Everything including the UI, with the committed build output excluded:

```bash
git diff -w --ignore-cr-at-eol --stat upstream/main FORK/main -- \
  . ':(exclude)webui/dist' ':(exclude)webui/src/app/i18n/translations' \
    ':(exclude)go.sum' ':(exclude)*package-lock.json'
```

Worth an alias:

```bash
git config alias.forkdiff '!f() { git diff -w --ignore-cr-at-eol "${2:-upstream/main}" "$1" -- . ":(exclude)webui/dist" ":(exclude)webui/src/app/i18n/translations" ":(exclude)go.sum" ":(exclude)*package-lock.json"; }; f'
# git forkdiff lodestone/main
```

Read the whole substantive diff that comes back. A fork's commit message states intent,
not behaviour.

## Taking the change

One topic branch per feature, based on `upstream/main`:

```bash
git switch -c feat/dht-rate-limit upstream/main
git cherry-pick -x <sha>          # -x records the source commit
```

When the fork carries CRLF or module-rename churn, take the patch rather than the commit
— cherry-picking drags the churn with it:

```bash
git diff -w upstream/main o51r15/main -- internal/prowlarr | git apply -3
```

Then renumber any migration the fork added, and give the change a test seen **red**
before it goes anywhere. Record the outcome — accepted, deferred, or rejected with the
reason — in [integration-status.md](integration-status.md).
