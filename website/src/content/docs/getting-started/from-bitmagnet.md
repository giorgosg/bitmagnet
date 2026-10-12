---
title: Moving from Bitmagnet
description: Plan a migration from upstream or an older fork deployment.
---

Magnes shares Bitmagnet's lineage, but its deployment and access rules have changed.
Make a database backup and test the upgrade against a restored copy before replacing
an existing instance. Do not run two different versions against the same database
while evaluating the migration.

## What changes

- Docker and release builds include the Elm UI at `/ui/`, with a root redirect.
- The backend executable, database default, and configuration directory still use
  `bitmagnet`; existing configuration names have not been renamed to `magnes`.
- Catalogue and Torznab access require credentials until an administrator explicitly
  grants anonymous read Permissions. The first startup with the empty-anonymous-role
  migration clears old `anon` grants once; later restarts preserve new grants.
- Cross-origin browser access defaults to denied. The bundled UI shares the API origin
  and needs no CORS allowance.
- Browser sessions require HTTPS and a stable JWT signing secret.

## Review your configuration

Replace an explicit `http_server.options` entry named `webui` with `static`.
The default `['*']` includes the static component already. An external directory in
`http_server.static.dir` overrides the bundled UI, so remove that override if you want
the included Elm interface. `/` is not a valid static mount; use `/ui` or another
non-root path.

Replace upstream image references with a build of this fork. Preserve the PostgreSQL
volume and connection settings. Start the administrator Invitation worker and follow
[first login](../first-login/), then give Servarr clients their own API keys.

## Validate before switching

Check health, sign-in, catalogue searches, UI deep links, and the Torznab connection test.
Review the startup logs for migrations and access changes. Keep the old application
artifact and a pre-upgrade database backup together. Do not assume an older application
can safely use a database after newer migrations have run.
