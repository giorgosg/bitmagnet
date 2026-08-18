# Git workflow for fork review

## Remotes

Configured locally (not part of the repo — re-run on a fresh clone):

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

All upstream PR heads, without adding a remote per contributor:

```bash
git config --add remote.upstream.fetch '+refs/pull/*/head:refs/remotes/upstream-pr/*'
git fetch upstream
# every open PR is now available as upstream-pr/<number>
git log --oneline upstream/main..upstream-pr/510
```

`.git` is ~900 MB with all forks fetched.

## Three analysis gotchas

These cost real time. All three come from the same root cause: **most of these forks
rebase onto upstream rather than merging**, so their commits have new SHAs and their
merge-base with `main` is the original 2023 fork point.

### 1. GitHub's ahead/behind is wrong for rebased forks

`behind_by` counts SHA reachability. A fork that rebased shows as hundreds of commits
behind while containing all of upstream's work. lodestone reported _209 behind_ but was
missing exactly one upstream commit.

Use patch-equivalence instead:

```bash
git cherry upstream/main FORK/main | grep -c '^+'   # genuinely new work
git cherry FORK/main upstream/main | grep -c '^+'   # genuinely missing from upstream
```

### 2. Three-dot diffs are useless here

`git diff upstream/main...FORK/main` diffs from the **merge base**, which for these
forks is `ce8909db` (2023-10-05). You get three years of upstream history mixed into
the fork's changes.

**Always use two-dot** `git diff upstream/main FORK/main` for fork review.

### 3. Whitespace and line endings inflate everything

o51r15 converted the whole tree to CRLF. Its Go diff reads as 507 files / +77k / −72k;
normalized it is **65 files / +4,920 / −26**. lodestone renamed the Go module, so all
319 changed Go files include ~300 that are import-line churn.

Always pass `-w --ignore-cr-at-eol`.

## The review command

```bash
git diff -w --ignore-cr-at-eol --stat upstream/main FORK/main -- \
  '*.go' ':(exclude)*.gen.go' ':(exclude)*/mocks/*' ':(exclude)*.pb.go'
```

For everything including the UI, exclude the committed build output:

```bash
git diff -w --ignore-cr-at-eol --stat upstream/main FORK/main -- \
  . ':(exclude)webui/dist' ':(exclude)webui/src/app/i18n/translations' \
    ':(exclude)go.sum' ':(exclude)*package-lock.json'
```

Suggested alias:

```bash
git config alias.forkdiff '!f() { git diff -w --ignore-cr-at-eol "${2:-upstream/main}" "$1" -- . ":(exclude)webui/dist" ":(exclude)webui/src/app/i18n/translations" ":(exclude)go.sum" ":(exclude)*package-lock.json"; }; f'
# git forkdiff lodestone/main
```

## Integration approach

Base stays `upstream/main`. One topic branch per feature, cherry-picked:

```bash
git switch -c feat/dht-rate-limit upstream/main
git cherry-pick -x <sha>          # -x records the source commit
```

For a fork with CRLF or module-rename churn, take the patch rather than the commit:

```bash
git diff -w upstream/main o51r15/main -- internal/prowlarr | git apply -3
```

Renumber any migration the fork added — see [architecture.md](architecture.md#database).
