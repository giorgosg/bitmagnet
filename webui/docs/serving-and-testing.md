# Serving Magnes against a real bitmagnet

Three ways to serve the UI, what each is for, and what has to be true of the instance
behind it. Facts about bitmagnet, checked against `trunk` `77fdb9de7` on 2026-08-24.

Which instances exist, where they are and how to reach them is deliberately not here —
that belongs in a gitignored `docs/*.local.md`. This page assumes you know which one you
are pointing at.

## What the instance has to be

Two capabilities matter, and they arrived separately:

| Needed for | Capability | How to check |
| --- | --- | --- |
| Anything to do with accounts | the auth port | `{self{identity{user{username}}}}` resolves rather than failing validation |
| Same-origin serving (way 2) | `http_server.static` (PR #48) | the configured static path returns something other than 404 |

An instance predating the auth port has no `self` field, and asking for it is a
**document validation error that fails the whole request** — which is why identity must
never be bundled into the search query. That is world 1 in
[accounts-plan.md](accounts-plan.md).

## Three ways to serve the UI

### 1. The dev server, cross-origin — what `npm run dev` does

```bash
npm install
cp public/config.example.js public/config.js   # then point it at your instance
npm run dev                                    # builds, serves public/ on :8000
```

`dev.js` serves `public/` and falls back to `index.html` for extensionless paths, so
`/torrent/<hash>` survives a refresh. The page queries bitmagnet **directly from the
browser**, so the address in `config.js` must be reachable from the browser, not from
wherever the dev server runs. If the instance is behind an SSH tunnel, the tunnel has to
be up in the session running the browser, and the address is the local end of it.

`config.example.js` defaults to `http://localhost:3333/graphql`, bitmagnet's own default.
That is a real address on whatever machine the browser is on, so an unedited copy fails in
a way that looks like the server is down rather than like the config is wrong.

This arrangement depends on bitmagnet's CORS defaults:

- `AllowedOrigins` is `["*"]` by default, deliberately, because narrowing it breaks any UI
  served from another origin — which is exactly this arrangement. bitmagnet's
  `docs/issues/0009` proposes narrowing it; if that lands, this needs
  `http_server.cors.allowed_origins` set explicitly.
- `AllowedHeaders` is, since PR #45, the four headers the server actually reads —
  `Content-Type`, `Authorization`, `X-Api-Key`, `X-Import-Id` — rather than reflecting
  whatever was asked for. **[verified]** each of the four is allowed individually, and
  `authorization,content-type,x-api-key` passes preflight as a set. Bearer tokens work
  cross-origin. Any *other* header Magnes invents will now fail preflight until it is
  added to `http_server.cors.allowed_headers`.

One trap, if you ever hand-test a preflight with curl: `rs/cors` compares the requested
header list against its own **sorted** list in a single pass, so an *unsorted*
`Access-Control-Request-Headers` is rejected. **[verified]** `content-type,authorization`
is refused while `authorization,content-type` is allowed. Browsers always send that header
sorted and lowercased, per the Fetch spec, so this never bites a real page — only a
hand-written probe. Sort your test headers rather than filing a bug.

Fastest loop, and the one to use while iterating on the UI. Good enough for login and
token handling, since `Authorization` is allowed cross-origin. Not the arrangement to sign
off on, because it is not how the thing will be deployed.

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
`config.js`: a **relative** endpoint, same origin, no CORS in the picture at all.

This is the deployment the account work should be signed off against, because it is the
one where the browser sends credentials the way a same-origin page does.

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
# then write config.js at the destination:
window.MAGNES_API_URL = "/graphql";
```

That last step is not optional. `public/config.js` is gitignored and holds *your*
development address, so rsync either copies the wrong endpoint or, with `--delete` and no
local copy, leaves none at all — and a page with no config falls back to
`http://localhost:3333/graphql`, which in a browser means the *viewer's* own machine. The
relative `/graphql` is the entire point of this deployment.

Serving from a container means the directory must be bind-mounted into it. Read-only is
right: bitmagnet only ever reads these files, and the build is copied in from outside.

### 3. A Magnes server

Described in the README, not built, and its original justification has expired — see the
decision at the top of [accounts-plan.md](accounts-plan.md). Way 2 now works on a real
instance, which weakens the case further. Do not start on it without raising that.

## Turning authentication on

Authentication is off by default: `auth.anonymous_access` defaults to `true`, which grants
the anonymous identity every registered object action except the two in the `auth`
namespace. World 3 in [accounts-plan.md](accounts-plan.md) — where an unauthenticated
caller cannot even search — needs:

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

## Regenerating the client

```bash
BITMAGNET_URL=http://your-bitmagnet:3333 npm run codegen
npm run format
```

Be explicit about the URL rather than relying on the `http://localhost:3333` default, and
generate against an instance that **has the auth port** — otherwise the result silently
lacks every account type and nothing in [accounts-plan.md](accounts-plan.md) compiles.
Confirm before committing: the schema should introspect to include `Self`, `User`, `Role`,
`APIKey`, `Invitation`, `AuthQuery` and `AuthMutation`.

Commit the result — a checkout should build without reaching an instance.
