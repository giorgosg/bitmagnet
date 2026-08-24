# Magnes

An alternative web UI for [bitmagnet](https://bitmagnet.io), written in
[Elm](https://elm-lang.org).

Bitmagnet is a self-hosted BitTorrent DHT crawler and indexer. It ships with
its own UI; Magnes is a second one, built against the same GraphQL API.

## Status

Runs. Search, sort, facet filters, infinite scroll, row expansion down to a file tree and
`/torrent/<hash>` are built. Identity and permission work is now in progress against the
fork's browser-cookie contract. See [docs/plan.md](docs/plan.md) for the completed first
milestone and [docs/accounts-plan.md](docs/accounts-plan.md) for the current phase.

Accounts are next, and [the bitmagnet fork](https://github.com/giorgosg/bitmagnet) now
has them — along with an option to serve a UI like this one from its own origin. Both are
live on the instance this is developed against. The next phase is worked out in
[docs/](docs/README.md).

## Running it

```
npm install
BITMAGNET_URL=http://your-bitmagnet:3333 npm run dev
```

The development server uses HTTPS and proxies same-origin `/graphql` requests to
`BITMAGNET_URL`, allowing bitmagnet's Secure, HttpOnly browser cookie to behave as it does
in production. It creates a self-signed localhost certificate on first use; accept it once
in the development browser. The API address and static mount path remain runtime values,
so one compiled bundle works against any instance.

The fork can also serve Magnes itself, from the API's own origin, which removes CORS from
the picture entirely. Point `http_server.static.dir` at a build of `public/`, set the
deployed HTML base and `window.MAGNES_BASE_PATH` to the mount path, and set
`window.MAGNES_API_URL = "/graphql"`. See
[docs/serving-and-testing.md](docs/serving-and-testing.md).

## Approach

Bitmagnet's entire API is GraphQL, so the client is generated rather than
hand-written: [`dillonkearns/elm-graphql`](https://github.com/dillonkearns/elm-graphql)
introspects the live schema and emits type-safe Elm for every query, object,
input and enum. A schema change in the fork becomes a compile error here, not a
runtime surprise.

The generated modules are committed under `src/Magnes/Api`, so a checkout builds without
reaching an instance. Regenerate against one when bitmagnet is upgraded:

```
BITMAGNET_URL=http://your-bitmagnet:3333 npm run codegen
```

## Design decisions

### Bitmagnet serves Magnes and enforces access

The current deployment is the static Elm bundle served by bitmagnet's
`http_server.static` mount. Runtime GraphQL traffic stays on that origin, and the fork
enforces its permission model on every top-level field. Magnes reads those permissions to
present only reachable controls; bitmagnet remains the protection boundary.

A separate Magnes server is deferred. It would be justified by state bitmagnet does not
hold, such as saved searches, or by an independently hosted origin—not by access control
the fork already provides. Raise that decision before adding a server. See
[docs/accounts-plan.md](docs/accounts-plan.md).

### Anonymous access is a permission set

`auth.anonymous_access` decides what an unauthenticated identity may reach. When enabled,
bitmagnet grants that identity its configured object actions; when disabled, search waits
for login. The UI derives its navigation and controls from `self.identity.permissions`,
while the server enforces the same permissions regardless of what the UI draws.

### Search state lives in the URL

Search terms, filters and sort order are query parameters, not just fields in
the Elm model. A search is therefore a link — shareable, bookmarkable, and the
back button walks the queries you actually ran instead of doing nothing.

The cost is that every filter change round-trips through `onUrlChange`. Rapid
changes use `replaceUrl`, so a back press doesn't have to unwind one history
entry per keystroke.

### Results load on scroll

No page numbers. Results append as you reach the bottom.

Browsing a DHT index is scanning, not lookup; page 47 of 900 is not a place
anyone means to go. The trade-off is that scroll depth is not addressable — a
shared link restores the query, not your position five hundred results down.
That seems like the right thing to give up.

Because scanning means long scroll sessions, the list is virtualized from the
start rather than as a later optimization: only visible rows exist in the DOM.
Retrofitting that is awkward, since the code that decides *what to render* and
the code that decides *when to fetch more* both want to own the scroll handler.

### Rows expand in place

A result row is one dense line with the name truncated. Expanding it reveals
metadata; expanding further reveals the torrent's files — where there are any. On
a real index most torrents have no file information at all, so the second level
is offered conditionally rather than always drawn and then found empty. Most
torrents are also unclassified, so a row has to look right with nothing but a
title, a size and a date.

The files are a directory tree, not a list of paths: the last line of an expanded
row is the torrent's own folder, and opening it opens every folder beneath at
once. Folders then close one at a time. bitmagnet returns flat paths, so the tree
is reconstructed client-side.

Detail is therefore progressive and doesn't interrupt scanning — no round trip
to a detail page and back just to see a file list. Each state has a known
height, so virtualization stays exact without measuring anything. File lists are
fetched on demand and capped, since a single torrent can contain thousands of
files.

### A torrent also has its own address

`/torrent/<hash>` is a real route, and every row links to it — so middle-click
and "open in new tab" work, and a torrent can be shared without sharing the
search that found it. It renders the same expanded row the list does, standalone,
fetching that one torrent when opened cold.

A plain left-click on that link does *not* follow it: it expands the row where it
is. Browsing an index means opening many rows in passing, and a page load for
each one is the wrong default — so the ordinary click is the cheap thing and the
address is what the deliberate gestures reach.

Expansion itself is *not* in the URL. Scanning means expanding several rows in
passing, and if each one pushed a history entry the back button would fill with
them — the same problem `replaceUrl` solves for filter churn. So expansion is
local state, and the route is an address you can navigate to deliberately. The
two are independent, and multiple rows can be open at once.

### Real paths, not fragments

Routes are ordinary paths (`/search?q=…`, `/torrent/<hash>`), which requires whatever
serves the app to fall back to `index.html` on unmatched paths, so a deep link survives a
refresh. Everything that serves Magnes already does: `dev.js` in development, and
bitmagnet's own static mount in deployment. So this costs nothing, and it avoids the `#/`
in every URL.

## The name

"Magnet" comes from Magnesia, a region in Thessaly where lodestone was found —
and from Magnes, the shepherd of the legend whose iron-nailed sandals stuck to
the rock.

## License

[MIT](LICENSE), matching bitmagnet's own licence — a UI for a project should not be
harder to use than the project.
