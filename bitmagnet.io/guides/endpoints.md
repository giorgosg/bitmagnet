---
title: Endpoints
parent: Guides
layout: default
nav_order: 1
redirect_from:
  - /endpoints.html
---

# **bitmagnet** Endpoints

**bitmagnet** exposes functionality on a number of endpoints. This fork ships no bundled UI or root redirect. To serve a separately built UI, set `http_server.static.dir`; its default mount is `/ui`, and `/webui` can be configured explicitly:

- `/graphql` - GraphQL API (`POST`); the GraphiQL browser interface (`GET`) requires `graphql.playground: true`
- `/torznab/*` - Torznab API for integration compatible applications
- `/import` - Import API for adding new content to the library (see [the importing guide](/guides/import.html))
- `/metrics` - Prometheus metrics (see [the observability guide](/guides/observability-telemetry.html))
- `/debug/pprof/*` - Go pprof profiling endpoints (see [the observability guide](/guides/observability-telemetry.html))
- `/status` - Health check/status endpoint
