# Serving Magnes against a real bitmagnet

Three ways to serve the UI, what each is for, and what has to be true of the instance
behind it. Facts about bitmagnet, checked against `trunk` `77fdb9de7` on 2026-08-24.

Which instances exist, where they are and how to reach them is deliberately not here —
that belongs in a gitignored `docs/*.local.md`. This page assumes you know which one you
are pointing at.

## What the instance has to be

The target fork needs these two capabilities:

| Needed for | Capability | How to check |
| --- | --- | --- |
| Identity and permission work | the fork's auth surface | `{self{identity{user{username}}}}` resolves |
| Same-origin serving (way 2) | `http_server.static` (PR #48) | the configured static path returns something other than 404 |

[auth-api.md](auth-api.md#the-shape-of-it) is authoritative for the two supported
anonymous-access configurations. The serving check here answers only whether this
instance exposes the capability needed by the task.

## Three ways to serve the UI

### 1. The same-origin HTTPS development proxy — what `npm run dev` does

```bash
npm install
BITMAGNET_URL=http://your-bitmagnet:3333 npm run dev
```

`dev.js` serves `public/`, falls back to `index.html` for extensionless Elm routes, and
proxies the browser's same-origin `/graphql` requests to `BITMAGNET_URL`. This is required
for browser authentication: bitmagnet's credential is an HttpOnly, Secure, SameSite cookie,
and cookie-authenticated mutations require an exact same-origin HTTPS `Origin`. A direct
cross-origin request cannot satisfy that contract.

The server creates a 30-day self-signed localhost certificate under the gitignored `.dev/`
directory on first use. Accept it once in the development browser. To use a locally trusted
certificate instead, set both `MAGNES_DEV_CERT` and `MAGNES_DEV_KEY` to existing PEM files,
or drop a trusted pair at the default paths:

```bash
mkcert -install                                    # once; adds a local CA to your trust store
mkcert -cert-file .dev/localhost.pem -key-file .dev/localhost-key.pem localhost 127.0.0.1 ::1
```

A trusted certificate is worth the two commands: an automated browser will not click
through a certificate warning, so a self-signed one blocks any scripted checking of the UI.
`PORT` defaults to `8000`.

The default upstream is `http://localhost:3333`; always set `BITMAGNET_URL` explicitly when
developing against another instance. Rather than typing it every time, put it in the
gitignored `.dev/env`, which `dev.js` reads before falling back to the default:

```
BITMAGNET_URL=http://your-bitmagnet:3333
```

One `KEY=value` per line, `#` comments allowed. The real environment still wins, so a
one-off `BITMAGNET_URL=… npm run dev` overrides the file. Nobody else's host belongs in the
repository, which is why this file is ignored rather than committed.

The value is consumed by Node, not exposed to the browser. `public/config.js` should
normally be absent during development so the bundle uses the same-origin `/graphql`
default — an absolute cross-origin address there defeats the whole arrangement, and from an
HTTPS page a plain-HTTP one is blocked outright as mixed content.

The proxy preserves the browser-facing `Host` and `Origin` headers. This lets bitmagnet
verify the request as same-origin even though the proxy-to-bitmagnet hop may use plain HTTP.
Production sign-off still uses way 2 below, where bitmagnet serves both endpoints itself.

### 2. Served by bitmagnet itself, same-origin — what to test accounts against

The fork's `static` http server option (PR #48) mounts a directory of files at a
configured path:

```yaml
http_server:
  static:
    dir: /path/to/magnes/public   # empty (the default) disables the mount entirely
    path: /ui                     # the default; any non-reserved path works
```

Unknown paths beneath it fall back to `index.html`, so Magnes keeps its own routing and
`dev.js` becomes unnecessary. Set `window.MAGNES_API_URL = "/graphql"` in the deployed
`config.js`: a **relative** endpoint, same origin, no CORS in the picture at all. Magnes
also needs the mount path twice: as `window.MAGNES_BASE_PATH` without a trailing slash,
so Elm parses and builds routes beneath it, and in `index.html`'s `<base href>` with a
trailing slash, so assets load from it even on a deep link.

This is the deployment the account work should be signed off against, because it is the
one where the browser sends credentials the way a same-origin page does.

#### It has to be HTTPS, and the failure is silent

bitmagnet cannot serve TLS: `internal/httpserver/config.go` has no certificate option and
the package never calls `ListenAndServeTLS`. Serve this mount over plain HTTP anyway and
registration works while login quietly does not — the mutation succeeds, bitmagnet sets
its `__Secure-` cookie marked `Secure`, and the browser discards it because the origin is
insecure. Nothing is reported; the person simply stays Anonymous. **[verified 2026-08-29
on a plain-HTTP mount, which is what prompted this section.]**

So way 2 needs a TLS-terminating proxy in front of it, exactly as way 1 does. One header is
load-bearing there: the proxy must pass the browser's `Host` through *with its port*
(nginx's `$http_host`, not `$host`), because bitmagnet compares the `Origin` header's host
against the request's `Host` for every cookie-authenticated mutation, and a stripped port
makes them differ.

Note also that a proxy makes every request appear to come from it, and bitmagnet's login
throttle is keyed on the client address — set `http_server.trusted_proxies` or the whole
network is throttled as one caller.

Notes from `../bitmagnet/internal/httpserver/static/static.go`:

- A configured directory that does not exist is a **startup error**, not a silent 404. So
  the directory has to be created before the config references it.
- `path` refuses `/` and `/webui` — two options claiming one route make gin panic during
  startup rather than report a conflict.
- The mount reads from disk on each request, so a rebuild is picked up without restarting
  bitmagnet.
- The bundled Angular UI can be turned off independently by omitting `webui` from
  `http_server.options` (default `["*"]`).

#### An empty directory reads as success

Worth knowing before it costs an hour. If the mount is configured but the directory has no
files in it, the results look contradictory:

```
GET <path>/              → 200, 81 bytes
GET <path>/index.html    → 404
GET <path>/anything/else → 404
```

The 200 is not the UI. Those 81 bytes are Go's `http.FileServer` **directory listing of an
empty directory** — `<!doctype html>`, a viewport meta and an empty `<pre>`. And the SPA
fallback cannot help, because it falls back to `/index.html`, which does not exist either,
so it re-raises `fs.ErrNotExist` and gin serves its bare 404. Deep links start working the
moment a build lands there. Do not read this as PR #48 being broken.

#### Deploying

For a local instance, point `static.dir` straight at the checkout's `public/` directory
and there is nothing to copy. For a remote one it is a file copy into whatever directory
the mount reads, plus one extra step:

```bash
npm run build:optimize
rsync -a --delete public/ <host>:<dir>/
```

Set these two values in the deployed `config.js`:

```js
window.MAGNES_API_URL = "/graphql";
window.MAGNES_BASE_PATH = "/ui";
```

The deployed `index.html` needs the matching trailing-slash base:

```html
<base href="/ui/" />
```

Those deployment values are required. `public/config.js` is gitignored and may hold local
runtime values, so rsync can otherwise copy the wrong mount configuration. The bundle's
API fallback is already relative `/graphql`; the deployed config records that choice
explicitly and supplies the base path. Without the base-path values, the HTML requests
assets from the origin root and Elm reads `/ui` as an application route.

Serving from a container means the directory must be bind-mounted into it. Read-only is
right: bitmagnet only ever reads these files, and the build is copied in from outside.

### 3. A Magnes server

Described in the README, not built, and its original justification has expired — see the
decision at the top of [accounts-plan.md](accounts-plan.md). Way 2 now works on a real
instance, which weakens the case further. Do not start on it without raising that.

## Turning authentication on

Authentication is off by default: `auth.anonymous_access` defaults to `true`, which grants
the anonymous identity every registered object action except the two in the `auth`
namespace. Requiring a User for search needs:

```yaml
auth:
  anonymous_access: false      # this is the switch that turns authentication on
  jwt_secret: <anything fixed> # otherwise every restart logs everyone out
```

Flipping it changes what **every** existing client of that instance can do, so it is the
user's call, not a step to take unasked — and not something to do to an instance anyone
else depends on. A local bitmagnet from the fork's `docker-compose.yml` is the alternative
if the shared instance should stay open.

**Set `jwt_secret`.** Unset, it is generated per process, and a restart invalidates every
token — which during development means constantly, and looks like a client bug.

The first administrator registers with the invitation code the `auth_initial_invitation`
startup worker writes to the log. It is idempotent: a restart finds the unclaimed
invitation rather than issuing another.

If bitmagnet runs behind anything that proxies, set `http_server.trusted_proxies` to that
proxy's CIDR — otherwise every request is attributed to the proxy and shares one login
throttle bucket. Empty (the default) means believe nobody and use the real peer, which is
correct for a directly reachable dev instance.

## A fourth arrangement: the one the tests bring themselves

The three ways above all point Magnes at an instance someone maintains. The credentialed
end-to-end suite does not: `npm run test:e2e:credentialed` starts a bitmagnet of its own, on
a clone of the `../btm-testdb` seed template, registers a throwaway administrator through
the bootstrap Invitation that instance prints, and drops all of it afterwards. It is way 1
underneath — the same `dev.js` proxy, for the same reason, since the cookie is `Secure` and
the origin has to be — with the upstream chosen per run instead of read from `.dev/env`.

That is the only supported way to exercise a signed-in Magnes automatically, and the reason
it exists is the paragraph above about turning authentication on: doing it to a shared
instance changes what every other client of that instance can do. See `e2e/README.md`.

## Regenerating the client

```bash
BITMAGNET_URL=http://your-bitmagnet:3333 npm run codegen
npm run format
```

Be explicit about the URL rather than relying on the `http://localhost:3333` default, and
generate against the target fork. Confirm before committing: the schema should introspect
to include `Self`, `User`, `Role`, `APIKey`, `Invitation`, `AuthQuery` and `AuthMutation`.

Commit the result — a checkout should build without reaching an instance.

### Introspection is off by default

`npm run codegen` reads the schema by introspection, and bitmagnet gates that behind
`graphql.introspection`, which defaults to `false` (see the fork's `docs/auth.md`). Against
an instance that has not been told otherwise, `__schema` answers `INTERNAL_SERVER_ERROR`
while ordinary queries answer normally — so codegen fails in a way that looks like the
instance is broken rather than like a setting is off.

The key gates developer surface, not access: introspection carries no authorization of its
own, so on an anonymously reachable instance it hands out a full map of the API. Turn it on
for the regeneration and turn it back off, rather than leaving it on.

The fixture server from `e2e/` does not help here — it leaves the same config at its zero
value, so it is gated too.
