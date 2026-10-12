---
title: Troubleshooting
description: Diagnose common startup, browser, and integration problems.
---

## The UI is missing

Confirm that you built with `task build` or this fork's Dockerfile. Plain `go build`
is backend-only. Check `http_server.static.bundled`, remove an unwanted
`http_server.static.dir` override, and ensure an explicit HTTP options list includes
`static`. The default UI path is `/ui/`.

## Registration works but sign-in does not persist

Use HTTPS. Check that the UI and GraphQL share the same browser-visible origin and
that the proxy preserves `Host`. A changed `auth.jwt_secret` invalidates existing
sessions. Check the browser's cookie warnings and network response before changing
CORS settings; same-origin requests do not need a CORS allowance.

## Search requires authentication

A fresh `anon` Role has no catalogue Permissions. Sign in with a User that has them,
or have an administrator deliberately grant anonymous reads. Setting
`auth.anonymous_access: true` alone does not grant catalogue access.

## Search or charts are empty

Check worker status and backend logs. Confirm the crawler and queue server are running,
that the database is reachable, and that the selected filters or time window include
data. Classification can lag behind discovery; a new instance starts with an empty index.

If the classification queue reached `dht_crawler.max_queue_depth`, discovery can
continue without new classification jobs. After resolving the backlog, selectively
reprocess unclassified torrents. See [crawling and resource use](../crawler-resources/)
for the threshold's behaviour.

## The host is running out of memory or disk

Check host memory and swap alongside both Magnes and PostgreSQL. Reduce crawler
concurrency before increasing it, and change one setting at a time. Builds, restores
and large reprocessing batches also compete with the running instance for resources.
Check free database and backup space before starting those operations.

The crawler's settings do not enforce a total storage cap. See
[resource use](../crawler-resources/) and [backups and upgrades](../backup-upgrades/).

## Torznab rejects the client

Check the client-visible URL, API key, its expiry and scope, the owning User's enabled
state, and that User's Permissions. Use the client's connection test. A successful
browser login does not validate its machine credential.

## Collect useful evidence

Record the deployed commit, build method, relevant configuration keys, worker state,
and the exact error. Remove passwords, JWT secrets, Invitation codes and API keys
before sharing configuration or logs. `bitmagnet config show` includes current values,
so review its output rather than posting it in full.
