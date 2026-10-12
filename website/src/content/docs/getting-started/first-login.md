---
title: HTTPS and first login
description: Set up the browser origin and register the first administrator.
---

## Serve one HTTPS origin

Serve `/ui/` and `/graphql` on the same HTTPS origin, including port. The browser
credential is an HTTP-only Secure cookie. Plain HTTP is sufficient for the health
check in the installation guide, but it does not support browser authentication.

For a host-installed Caddy proxy, a minimal configuration is:

```text
magnes.example.com {
    reverse_proxy 127.0.0.1:3333
}
```

Replace the hostname with your own, point DNS at the host, and make the proxy's
certificate and networking work before continuing. Forward all paths and preserve
the browser-facing `Host`, including a nonstandard port if you use one. Do not rewrite
`/graphql` to an unrelated origin.

Set `http_server.trusted_proxies` to only the proxy's actual address or CIDR so login
rate limiting sees the client address. For a native backend reached over loopback:

```yaml
http_server:
  trusted_proxies:
    - 127.0.0.1/32
```

With Docker Compose on Linux, a host proxy usually reaches the container from the
Docker network's gateway, even when it forwards to `127.0.0.1:3333`. Find that address:

```sh
docker inspect --format '{{range .NetworkSettings.Networks}}{{.Gateway}}{{end}}' \
  "$(docker compose -f compose.magnes.yml ps -q magnes)"
```

Use the returned address with `/32` in `trusted_proxies` instead of `127.0.0.1/32`.
A containerised proxy needs its own address or narrowly scoped network CIDR.
Save the YAML in `config/config.yml` when using the installation guide's volume mount
and restart Magnes. Confirm that request logs show the client address rather than the
proxy address; temporarily setting `log.level: debug` enables HTTP request logs.
Keep `auth.jwt_secret` fixed across restarts and replicas.

## Register the first User

The `auth_initial_invitation` worker creates an Invitation when no enabled administrator
exists. The installation guide starts it through `--all`. Its code appears in the log
once when created. To retrieve an outstanding code later:

```sh
docker compose -f compose.magnes.yml exec magnes bitmagnet auth initial-invitation
```

Open `https://magnes.example.com/ui/`, choose registration, and enter the Invitation
code with your username and password. Then sign in. Registration and sign-in are
separate steps. The bootstrap Invitation creates an administrator User.

If the CLI reports no outstanding Invitation, check that the workers have started.
If an administrator already exists, ask them for an Invitation rather than trying to
bootstrap a second one.

## Decide who can search

A fresh installation requires credentials for catalogue access. Although
`auth.anonymous_access` defaults to `true`, the `anon` Role starts with no catalogue
Permissions. Administrators can assign the read actions they want to that Role.
Setting `auth.anonymous_access: false` denies those actions regardless of its stored
Permissions. Health and version remain readable to support the login screen.

Use [authentication](../../reference/authentication/) to plan Users, Roles, and API keys.
