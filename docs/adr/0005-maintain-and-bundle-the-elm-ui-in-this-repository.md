# Maintain and bundle the Elm UI in this repository

Magnes replaces Angular as this fork's default UI. Its source now lives under `webui/`
so API and UI changes, tests and releases can be reviewed together. The separately
developed UI's 91 commits reachable from `main` at
`fd5be8fb58ea0679c080f81eff593514cc225f33` were imported from
<https://github.com/giorgosg/magnes-ui> using `git filter-repo --to-subdirectory-filter webui`
in a temporary clone, then merged with unrelated histories allowed. Authors, dates,
messages and merge structure are retained; commit IDs change because the historical
paths now include `webui/`. The rewritten tip is
`e589689b504124e248a45418aa491a89f70191b6`. The source repository is unchanged and can be
archived after this migration is merged. Future UI development belongs here.

Commit references within imported messages are remapped by filter-repo. Original commit
signatures cannot survive the rewrite. Merge this migration with a merge commit rather
than squash or rebase so the imported history remains reachable.

This supersedes the independent UI maintenance and deployment choice in
[ADR 0004](0004-deploy-browser-ui-separately.md). The GraphQL schema and browser cookie
contract remain the boundary; API authorization and same-origin CORS defaults stay in
force. Imported agent harness files are removed in favor of the root `AGENTS.md`.

`task build`, Docker and GoReleaser build an optimized Elm bundle before compiling Go
with the `webui` tag. Embedding only application assets gives every executable its
matching UI without runtime filesystem dependencies. Generated JavaScript is ignored.
Backend-only builds remain available without Node or Elm, including for generators.

The static option serves the bundle at `/ui/` by default, adjusts its runtime mount
configuration, and redirects `/` there. Operators can disable it, omit the static
option, or override it with an external directory. External-directory serving preserves
its previous behavior, including no root redirect. Browser authentication still needs
HTTPS through a reverse proxy.
