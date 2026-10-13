---
title: Configuration
description: Set backend, database, and bundled UI configuration.
---

Configuration key names retain their `bitmagnet` compatibility. Use YAML for nested
configuration and environment variables for scalar or list values.

```yaml
postgres:
  host: postgres
  name: bitmagnet
http_server:
  static:
    bundled: true
    path: /ui
  trusted_proxies:
    - 127.0.0.1/32
auth:
  anonymous_access: false
```

This is a shape example, not a complete deployment configuration. Supply the database
password and a persistent JWT secret separately. Dots in configuration keys become
nesting in YAML, or uppercase underscores in environment variables:

```sh
AUTH_ANONYMOUS_ACCESS=false LOG_LEVEL=debug ./bitmagnet config show
```

## Important settings

| Key                                | Default     | Purpose                                               |
| ---------------------------------- | ----------- | ----------------------------------------------------- |
| `postgres.host`                    | `localhost` | Database hostname                                     |
| `postgres.port`                    | `5432`      | Database port                                         |
| `postgres.name`                    | `bitmagnet` | Database name                                         |
| `postgres.user`                    | `postgres`  | Database User                                         |
| `postgres.dsn`                     | empty       | Overrides individual connection fields when set       |
| `http_server.local_address`        | `:3333`     | Backend HTTP listener                                 |
| `http_server.static.bundled`       | `true`      | Serve included UI in a UI-enabled build               |
| `http_server.static.path`          | `/ui`       | UI mount; cannot be `/`                               |
| `http_server.static.dir`           | empty       | External UI directory overriding the bundle           |
| `http_server.options`              | `['*']`     | Enabled HTTP components; include `static` for the UI  |
| `http_server.cors.allowed_origins` | `[]`        | Explicit cross-origin browser allowances              |
| `http_server.trusted_proxies`      | `[]`        | Proxy addresses trusted for forwarded client IPs      |
| `auth.anonymous_access`            | `true`      | Allow configured anonymous Permissions to apply       |
| `auth.invitation_required`         | `true`      | Require an Invitation for registration                |
| `moderation.automatic_ban`         | `false`     | Ban new harmful-keyword matches instead of quarantine |
| `auth.jwt_duration`                | `24h`       | Session token lifetime                                |

Set `auth.jwt_secret` to a stable random secret. An empty value generates a process-local
secret, invalidating sessions across restarts and failing to share them across replicas.

## Precedence

From highest to lowest: environment variables; files listed in `EXTRA_CONFIG_FILES`;
`config.yml` in the working directory; `config.yml` in the User's XDG configuration
location for `bitmagnet`; defaults. The Docker walkthrough mounts that configuration
location at `/root/.config/bitmagnet`.

Use `bitmagnet config show` to inspect available keys, current and default values,
and their sources. Its output may include secrets.

## Workers and classification

`bitmagnet worker run --all` starts all registered workers, including bootstrap
Invitations. A selected-worker deployment must include the workers its features need;
`http_server`, `queue_server`, and `dht_crawler` are the core serving, processing, and
discovery workers.

Use `tmdb.api_key` for your own TMDB credentials when classifying movies and TV.
`tmdb.enabled: false` disables the TMDB integration. Tune crawler throughput only after
measuring database, CPU, storage, and network behaviour on your instance.

See [crawling and resource use](../../guides/crawler-resources/) for the crawler's
concurrency, file-retention and classification queue settings.
