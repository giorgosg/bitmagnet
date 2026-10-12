---
title: Servarr integration
description: Connect Prowlarr, Sonarr, and Radarr through Torznab.
---

Magnes exposes Torznab at `/torznab`. Create an API key from the signed-in UI's API
keys page for each integration. Give it the required Torznab read Permission, within
the owning User's Permissions. Anonymous access is not a substitute for a machine
credential on a fresh installation.

In Prowlarr, add a **Generic Torznab** indexer:

| Field   | Value                                                                           |
| ------- | ------------------------------------------------------------------------------- |
| Name    | Magnes                                                                          |
| URL     | Your instance URL with `/torznab`, such as `https://magnes.example.com/torznab` |
| API key | The API key created for this client                                             |

Use the address reachable from the client. A client in another container cannot reach
Magnes through its own `localhost`. If both services share a Docker network, an internal
address such as `http://magnes:3333/torznab` can be used on that trusted network.

Run Prowlarr's connection test, then configure its application sync for Sonarr or Radarr.
You can also add the Torznab indexer directly to a compatible application.

A rejected key can mean it was revoked, expired, belongs to a disabled User, or lacks
the required Permission. A browser session is not a Torznab API key. Avoid including
API-key-bearing URLs in support reports or proxy access logs.
