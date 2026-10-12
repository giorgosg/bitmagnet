---
title: Monitoring and profiling
description: Inspect workers, logs, authenticated metrics, and Go profiles.
---

Start with the bundled UI's Status, Queue jobs, and statistics pages. They show the
application's health, worker state, queued work, and activity over a selected interval.
These are often enough to distinguish an idle crawler from a growing processing backlog.

## Logs and health

With the installation guide's Compose stack:

```sh
docker compose -f compose.magnes.yml logs --tail=200 magnes
curl --fail http://127.0.0.1:3333/status
```

Set `log.json: true` when shipping structured logs to a collector. `log.level: debug`
adds detail but can generate substantial output; use it for a bounded investigation.
The health endpoint being reachable does not prove all workers are making progress.

## Prometheus

`/metrics` requires `http:metrics:query`. Create a dedicated API key for the scraper,
with an owning User that has that Permission. Keep the `prometheus` HTTP component
in an explicit `http_server.options` list.

For a scraper on the same trusted Docker network, a job can look like:

```yaml
scrape_configs:
  - job_name: magnes
    metrics_path: /metrics
    authorization:
      type: Bearer
      credentials_file: /run/secrets/magnes-metrics-api-key
    static_configs:
      - targets: ["magnes:3333"]
```

This uses Prometheus's [HTTP authorization configuration](https://prometheus.io/docs/prometheus/latest/configuration/).
Mount the key file into Prometheus with owner-only access. Adapt the target to the
scraper's network; use your HTTPS endpoint and normal certificate validation when the
scrape crosses an untrusted network.

The repository's `observability/` directory contains dashboard and deployment examples.
Audit those against your deployment before using them: the older scrape configuration
does not include the credential now required by Magnes. Its Grafana Agent example is
legacy: Agent reached end of life on November 1, 2025. Use Grafana Alloy for a new
collector deployment, following [Grafana's migration guidance](https://grafana.com/docs/agent/latest/).

## Go profiling

`/debug/pprof/*` requires `http:pprof:query`, and the HTTP component is named `pprof`.
Download a profile with a dedicated API key before opening it locally:

```sh
{
  printf 'header = "Authorization: Bearer '
  tr -d '\r\n' < /secure/path/profiling-api-key
  printf '"\n'
} | curl --config - --fail \
  -o heap.pprof https://magnes.example.com/debug/pprof/heap
go tool pprof heap.pprof
```

Profiles can contain process internals. Keep them private and review them before sharing.
CPU profiling also adds work to the running process; use a bounded collection period
when investigating a busy instance.
