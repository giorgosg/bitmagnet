# Magnes

An alternative web UI for [bitmagnet](https://bitmagnet.io), written in
[Elm](https://elm-lang.org).

Bitmagnet is a self-hosted BitTorrent DHT crawler and indexer. It ships with
its own UI; Magnes is a second one, built against the same GraphQL API.

## Status

Runs. The first milestone is the UI alone — no server, no accounts, no mutations — which
works because bitmagnet allows cross-origin requests, so the browser can query it
directly. Search, sort, facet filters, infinite scroll, row expansion down to a file tree
and `/torrent/<hash>` are all built — that is the whole of the first milestone. What is
not built is everything the milestone deliberately left out: the proxy server, accounts,
and any mutation at all. See [docs/plan.md](docs/plan.md). The design decisions below
describe where Magnes is going, not everything that runs today.

## Running it

```
npm install
cp public/config.example.js public/config.js   # point it at your bitmagnet
npm run dev                                    # builds, serves public/ on :8000
```

`public/config.js` is gitignored and optional; without it Magnes uses bitmagnet's
default address on the same machine, `http://localhost:3333/graphql`. The address is
read at runtime, so one build works against any instance — and against the proxy
described below, when there is one.

It has to be reachable **from the browser** rather than from wherever the dev server
runs, because the page queries bitmagnet directly. That also means bitmagnet must allow
the origin; it sends `Access-Control-Allow-Origin: *` by default, which is what makes a
serverless UI possible at all.

## Approach

Bitmagnet's entire API is GraphQL, so the client is generated rather than
hand-written: [`dillonkearns/elm-graphql`](https://github.com/dillonkearns/elm-graphql)
introspects the live schema and emits type-safe Elm for every query, object,
input and enum. A schema change upstream becomes a compile error here, not a
runtime surprise.

The generated modules are committed under `src/Magnes/Api`, so a checkout builds without
reaching an instance. Regenerate against one when bitmagnet is upgraded:

```
BITMAGNET_URL=http://your-bitmagnet:3333 npm run codegen
```

## Design decisions

### A server sits in front

Magnes is not a purely static client. A small server holds a SQLite database of
users, and all bitmagnet API traffic is routed through it rather than going from
the browser to bitmagnet directly.

Bitmagnet's API has no notion of users or authentication — anything that can
reach it can do everything. Putting the server in the path gives one place to
enforce access, and keeps bitmagnet itself off any network the browser is on.

Code generation still introspects bitmagnet directly, since the schema is the
same either way. Only runtime traffic is proxied.

Likely JavaScript, eventually running on Cloudflare Workers — which also decides
the database: D1 is SQLite, so the same schema and queries work locally and
deployed.

### Guests are a permission level, not a flag

What an unauthenticated visitor can do is configured per server: nothing, search
only, or search plus the dashboard. Access is checked at the proxy, so a
permission level is a statement about which API operations are reachable, not
about which buttons the UI draws.

This is the setting that decides whether accounts matter at all. A server that
grants guests everything has, in effect, disabled accounts — that is a legitimate
way to run a private instance, and it should be one config value rather than a
separate mode.

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

Routes are ordinary paths (`/search?q=…`, `/torrent/<hash>`), which requires the
server to serve the app for unmatched paths so a deep link survives a refresh.
Since there is a server anyway, this costs nothing, and it avoids the `#/`
in every URL.

## The name

"Magnet" comes from Magnesia, a region in Thessaly where lodestone was found —
and from Magnes, the shepherd of the legend whose iron-nailed sandals stuck to
the rock.

## License

[MIT](LICENSE), matching bitmagnet's own licence — a UI for a project should not be
harder to use than the project.
