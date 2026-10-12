---
title: Installation
description: Installation instructions for bitmagnet
parent: Setup
layout: default
nav_order: 1
---

# Installation

## Docker

Run this fork with [Docker Compose](https://docs.docker.com/compose/). From a checkout of
this fork at the version you want to run, first build its image:

```sh
docker build -t bitmagnet-fork:local .
```

The upstream `ghcr.io/bitmagnet-io/bitmagnet` image installs upstream, including its
Angular UI; it does not contain this fork's changes. The following `docker-compose.yml`
uses the local fork image. For VPN routing and observability services, adapt the
[full compose example](https://github.com/giorgosg/bitmagnet/blob/trunk/docker-compose.yml)
to use the same image.

```yml
services:
  bitmagnet:
    image: bitmagnet-fork:local
    container_name: bitmagnet
    ports:
      # API port:
      - "3333:3333"
      # BitTorrent ports:
      - "3334:3334/tcp"
      - "3334:3334/udp"
    restart: unless-stopped
    environment:
      - POSTGRES_HOST=postgres
      - POSTGRES_PASSWORD=postgres
    #      - TMDB_API_KEY=your_api_key
    volumes:
      - ./config:/root/.config/bitmagnet
    command:
      - worker
      - run
      - --keys=http_server
      - --keys=queue_server
      # disable the next line to run without DHT crawler
      - --keys=dht_crawler
    depends_on:
      postgres:
        condition: service_healthy

  postgres:
    image: postgres:16-alpine
    container_name: bitmagnet-postgres
    volumes:
      - ./data/postgres:/var/lib/postgresql/data
    #    ports:
    #      - "5432:5432" Expose this port if you'd like to dig around in the database
    restart: unless-stopped
    environment:
      - POSTGRES_PASSWORD=postgres
      - POSTGRES_DB=bitmagnet
      - PGUSER=postgres
    shm_size: 1g
    healthcheck:
      test:
        - CMD-SHELL
        - pg_isready
      start_period: 20s
      interval: 10s
```

Run `docker compose up -d` and check the backend health endpoint:

```sh
curl --fail http://localhost:3333/status
```

This fork has no bundled UI or root redirect: `http://localhost:3333/` returns 404.
Use the GraphQL API at `/graphql` or deploy a browser UI separately. To serve a built UI
from disk, mount its directory into the container and set `http_server.static.dir` to
that container path; the default URL is `/ui/`. See [configuration]({% link setup/configuration.md %})
for static serving and cross-origin settings.

To upgrade, update your fork checkout to the desired version, rebuild the image, and recreate the container:

```sh
docker build -t bitmagnet-fork:local .
docker compose up -d --force-recreate bitmagnet
```

## Native Go build

Build the checked-out fork instead of installing the upstream module:

```sh
go build -o bitmagnet .
```

You will need to [configure]({% link setup/configuration.md %}) a Postgres instance for
bitmagnet to connect to.

## Running the CLI

The **bitmagnet** CLI is the entrypoint into the application. Take note of the command needed to run the CLI, depending on your installation method.

- If you are using the docker-compose example above, you can run the CLI (while the stack is started) with `docker exec -it bitmagnet bitmagnet`.
- If you built the fork natively, run the CLI with `./bitmagnet` from the checkout.

When referring to CLI commands in the rest of the documentation, for simplicity we will use `bitmagnet`; please substitute this for the correct command. For example, to show the CLI help, run:

```sh
bitmagnet --help
```

## Starting **bitmagnet**

**bitmagnet** runs as multiple worker processes that can be started either individually or all at once. To start all workers, run:

```sh
bitmagnet worker run --all
```

Alternatively, specify individual workers to start:

```sh
bitmagnet worker run --keys=http_server,queue_server,dht_crawler
```
