---
title: Installation
description: Build and run Magnes with its bundled Elm UI.
---

Magnes runs a Go backend and a PostgreSQL database. Docker builds and `task build`
include the Elm UI. The executable and database still use the name `bitmagnet`.

## Build the Docker image

Install Git, Docker and Docker Compose, then check out the Magnes integration branch:

```sh
git clone --branch trunk https://github.com/giorgosg/magnes.git
cd magnes
docker build -t magnes:local .
```

This builds this fork, including the UI. The upstream Bitmagnet image does not contain
Magnes's changes. For reproducible deployments, record the commit you build with
`git rev-parse HEAD`.

## Start the backend and database

Save the following as `compose.magnes.yml` in the checkout. Set `POSTGRES_PASSWORD`
and `AUTH_JWT_SECRET` in a `.env` file beside it. Generate independent random values,
for example with `openssl rand -hex 32`. Keep the JWT secret stable across restarts.

```yaml
services:
  magnes:
    image: magnes:local
    restart: unless-stopped
    ports:
      - "127.0.0.1:3333:3333"
      - "3334:3334/tcp"
      - "3334:3334/udp"
    environment:
      POSTGRES_HOST: postgres
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD}
      AUTH_JWT_SECRET: ${AUTH_JWT_SECRET:?set AUTH_JWT_SECRET}
    volumes:
      - ./config:/root/.config/bitmagnet
    command: [worker, run, --all]
    depends_on:
      postgres:
        condition: service_healthy
  postgres:
    image: postgres:16-alpine
    restart: unless-stopped
    environment:
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD}
      POSTGRES_DB: bitmagnet
    volumes:
      - magnes-postgres:/var/lib/postgresql/data
    healthcheck:
      test: [CMD-SHELL, "pg_isready -U postgres -d bitmagnet"]
      interval: 10s
      timeout: 5s
      retries: 10
volumes:
  magnes-postgres:
```

On hosts with SELinux enforcing, such as Fedora, change the config mount to
`./config:/root/.config/bitmagnet:Z`. This lets the container read files added to the
config directory. Without the label, startup can fail with `config.yml: permission denied`.

Create the config directory before starting Compose so it belongs to your user:

```sh
mkdir -p config
docker compose -f compose.magnes.yml up -d
curl --fail --retry 12 --retry-delay 5 --retry-all-errors http://127.0.0.1:3333/status
docker compose -f compose.magnes.yml logs magnes
```

The health check retries while Magnes starts and applies migrations. If it still fails,
inspect the logs before continuing.

The HTTP port is bound to loopback for a host reverse proxy. A containerised proxy
must instead reach Magnes through a shared Docker network.

The bundled UI is mounted at `/ui/`; the root redirects there. Browser sign-in needs
HTTPS, so continue with [first login](../first-login/) before registering.
The crawler discovers metadata over time; a new database starts empty.
Use [crawling and resource use](../../guides/crawler-resources/) to understand its
storage growth, concurrency settings and classification backlog.

## Native build

Use the development toolchain described in the repository. From the checkout:

```sh
nix develop --command task build
POSTGRES_HOST=localhost POSTGRES_PASSWORD=your-password ./bitmagnet worker run --all
```

Create the `bitmagnet` database first. Configure a persistent JWT secret and HTTPS
as described in the next guide. `task build` installs locked UI dependencies and
embeds the optimized UI. `task build-backend` and plain `go build` produce a
backend without the UI. Run `task build-ui` before invoking `go build -tags webui` yourself.

See [configuration](../../reference/configuration/) for database and worker settings.
