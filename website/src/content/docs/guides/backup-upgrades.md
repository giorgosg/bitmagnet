---
title: Backups and upgrades
description: Preserve your index and access settings when upgrading.
---

## Back up PostgreSQL

The database contains your catalogue, Users, Roles, Invitations, API keys, and queue.
Back up your configuration and stable JWT secret separately. With the installation
guide's Compose stack, a logical backup can be made with:

```sh
umask 077
docker compose -f compose.magnes.yml exec -T postgres \
  pg_dump -U postgres -d bitmagnet -Fc > magnes.dump
```

Check the command's exit status and test restoring the dump into a separate database.
A backup is only useful when you can restore it. Store it securely because it contains
authentication records as well as catalogue data.

For large indexes, plan backup time, storage, and restore time against your actual
hardware. Copying a live PostgreSQL data directory without a database-aware backup
procedure is not a substitute for a consistent backup.

## Test a restore

Use the PostgreSQL service from the installation guide to restore into a new, empty
database. These commands leave the running `bitmagnet` database untouched:

```sh
docker compose -f compose.magnes.yml exec -T postgres \
  pg_restore --list < magnes.dump
docker compose -f compose.magnes.yml exec -T postgres \
  createdb -U postgres -T template0 magnes_restore_check
docker compose -f compose.magnes.yml exec -T postgres \
  pg_restore --exit-on-error -U postgres -d magnes_restore_check < magnes.dump
```

The first command checks that the archive can be read; the final command restores its
schema and data. Check the exit status of each command. Choose an unused database name
if `magnes_restore_check` already exists. Use tools from the same PostgreSQL major
version as the source deployment, as this example does.

A successful restore command is the first check. To verify application behaviour, run
a separate Magnes instance against the restored database with the revision that made
the backup. Give it a separate HTTP listener and start only `http_server` initially:

```sh
bitmagnet worker run --keys=http_server
```

Configure `postgres.name: magnes_restore_check`, the database connection, HTTP address,
and HTTPS proxy for that test instance before starting it. Confirm sign-in and a few
known searches. Starting only the HTTP worker avoids crawling or processing the restored
queue during the check; startup still applies migrations. Keep the test instance private
because the restored database contains real Users, Invitations and API keys.

Restore time and space depend on the catalogue. A restore on the production database
server competes for its disk, CPU and memory; use another server when that load would
disrupt the running instance. Stop the test instance when finished and remove only its
test database after checking the name.

## Upgrade a source-built Docker deployment

Read the changes for the revision you intend to deploy and record your current commit.
Take and verify a backup before updating the checkout.

```sh
git switch trunk
git pull --ff-only
docker build -t magnes:local .
docker compose -f compose.magnes.yml up -d --force-recreate magnes
docker compose -f compose.magnes.yml logs magnes
```

These commands assume a clean deployment checkout, not a development branch with local
changes. Magnes runs database migrations during application startup. Verify health,
sign-in, search, and each integration after the upgrade. Keep the JWT secret unchanged
so restarting the backend does not invalidate every browser session.

Rollback may require restoring the pre-upgrade database along with the old application.
Do not run an older executable against a newer schema without checking compatibility.
Stop Magnes before a rollback, restore into an empty database, and select that database
in the old version's configuration. Recheck sign-in and searches before letting its
crawler and queue resume. Restoring a pre-upgrade backup loses changes made after that
backup; keep the failed-upgrade database until you have decided what to recover from it.
