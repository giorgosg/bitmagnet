# Phase 1 — the UI, with no server

The README describes Magnes as it should end up: a proxy server in front of bitmagnet,
guest permission levels, accounts. None of that is needed to build the interface, and
building it first would mean designing an access model before there is anything to
access.

Phase 1 is therefore the browser client alone. **No server, no database, no accounts, no
mutations, no dashboard.** The bitmagnet URL is a runtime flag, so when the proxy does
arrive it changes one string.

## This works because bitmagnet allows cross-origin requests

Verified against a live v0.10.0 instance — both the POST and the preflight:

```
Access-Control-Allow-Origin: *
Access-Control-Allow-Methods: POST
Access-Control-Allow-Headers: content-type
```

So the browser can query bitmagnet directly. This is a fact about bitmagnet, not a
decision, and it is also the reason the proxy eventually matters: a permissive API on a
reachable host is exactly what the proxy exists to cover. Phase 1 accepts that, because a
UI with no mutation code cannot delete anything regardless of what the network allows.

Only two things are actually deferred by dropping the server: unmatched-path fallback for
deep links (a dev-server setting until deployment), and any access control (nothing to
control yet).

## Scope

In: search, filters, sort, infinite scroll, row expansion, `/torrent/<hash>`.

Out: `Mutation` in any form — no delete, no tagging, no reprocess. The generated API
modules will contain them; nothing calls them. Also out: dashboard, metrics, queue,
workers, health, accounts, login.

## The row

One line, three things: **name, size, magnet link.**

The name truncates with an ellipsis and takes all the space left over. The size is right
of it. The magnet is an icon at the end — an `<a href="magnet:…">`, a real link, so it
opens a client on click and can be copied by right-click. No checkboxes, no per-row
buttons; there are no bulk operations without mutations.

A disclosure chevron sits to the left of the name and is the only thing that expands the
row. The name stays a plain link to `/torrent/<hash>`, so clicking it navigates and
nothing about the row is ambiguous — the alternative, making the whole line a toggle,
would have made a left-click on the name mean something different from a middle-click on
it. The chevron costs about 26px of name width.

Everything else — dates, seeders, content type, file list — lives in the expansion, not
the line. This is tighter than the README's "one dense line with the name truncated"
implies, and it is deliberate: 88% of rows on a real index have no content metadata, so
any column of metadata is a column of blanks.

Clean look: one typeface, a single accent colour, generous line height, no borders
between rows (hover shading instead), no chrome around the list. The search field is the
only thing above the results.

## Steps

Each step ends somewhere the app still compiles and runs. Steps 1–8 are built and were
driven against the live instance; 9 is not started.

**1. Toolchain and codegen.** ✅ `elm`, `elm-format`, `@dillonkearns/elm-graphql` as dev
dependencies; `elm init`; generate `src/Magnes/Api` from the live schema. Map `DateTime`
and `Date` to `Time.Posix` and `Year` to `Int` in `ScalarCodecs.elm` before anything
imports them. Done when the generated code compiles.

**2. Shell.** ✅ `Browser.application`, `Route` = `Search` | `Torrent String` | `NotFound`,
with the `toHref` inverse written alongside the parser so a new route breaks the link
builder at compile time. bitmagnet's URL arrives as a flag from `index.html`. Dev serving
needs unmatched-path fallback or a deep link 404s on refresh.

**3. One query, no styling.** ✅ `torrentContent.search` rendered as a flat list. Always set
`totalCount: true` and `hasNextPage: true` — omitting them fails silently and plausibly
(see the API reference). Done when real titles appear.

**4. The row and the look.** ✅ As above, plus byte and date formatting. Fixed row height,
because virtualization depends on it.

**5. Search state in the URL.** ✅ for `q` and `sort` — facets join them at step 9. The model
derives from the URL rather than the reverse: typing writes the URL after a 300ms quiet
period with `replaceUrl`, and the resulting `onUrlChange` is what issues the query. Enter
uses `pushUrl`, so the back button walks searches you committed to rather than every
keystroke.

**6. Infinite scroll.** ✅ `FabienHenon/elm-infinite-list-view` plus one hand-written scroll
decoder that both drives the virtualizer and fires the next fetch near the bottom — one
`on "scroll"` handler, because Elm allows only one. Append with **dedupe by `id`**:
offset paging over a live crawler re-serves rows. Expansion state must live on the record
stored in the model, or the package's internal `lazy3` compares fresh records every
render and virtualization stops helping.

**7. Expansion.** ✅ Level one is metadata; level two is the file list from `torrent.files`,
offered **only when `filesStatus == multi`** — about one row in eight. Each state has a
known height.

**8. `/torrent/<hash>`.** ✅ There is no get-by-hash query; call `search` with
`infoHashes: [hash]` and take the one item. Renders the same expanded row, standalone.

**9. Facets.** `contentType` with counts. Everything else as a filter without counts —
see the caveat below.

## Decisions taken here

**Plain `elm/html` and a hand-written stylesheet, not `elm-ui`.** The row depends on
`text-overflow: ellipsis` inside a flex child that shrinks, and on exact fixed heights
the virtualizer can be told about. Both are things elm-ui makes indirect — it has no real
truncation story, and heights are expressed through its own layout algebra rather than as
the pixel numbers `InfiniteList` needs. A stylesheet is also how "clean" gets achieved
cheaply. This was flagged in the package survey as expensive to reverse; it is settled.

**Facet counts come from `contentType` only.** On the live instance, `torrentFileType`
aggregation returned ~1918 for every bucket on a query whose `totalCount` was 673, and
the numbers drifted between identical calls. The counts are not query-scoped and cannot
be shown. The *filter* works correctly, so file type remains a filter with no number next
to it. `contentType` aggregates correctly, including a `null` "Unknown" bucket that is
usually the largest one.

**`totalCountIsEstimate` is read per query, not assumed.** A whole-index count came back
as an estimate (2.87M, `true`); a narrow query came back exact (2715, `false`). Render
"~2.9M results" or "2,715 results" depending on the flag rather than hedging everything.

**The file-list affordance keys off `filesStatus`, never `filesCount`.** An
`over_threshold` torrent reports a real count — 189, 222, 489 were all observed — while
having no indexed files. Trusting the count draws an expander that opens onto nothing.
Confirmed in the running UI: a 222-file `over_threshold` row draws metadata and no file
expander, while a 23-file `multi` row beside it draws both.

**Sorting is a menu of single orderings, not a sort builder.** bitmagnet's `orderBy` is a
list of field/direction pairs and composes arbitrarily, but a UI for composing them is one
nobody reads. Seven named orderings sit next to the search box — relevance, newest, oldest,
largest, smallest, most seeders, name — each a single field. `Sort` is one type shared by
the URL, the query and the menu, so adding an ordering is a compile error in all three
places at once. The default is left out of the URL, so an ordinary search stays a bare
`?q=`, and an unknown `sort=` degrades to the default rather than failing the route.

Changing the ordering uses `pushUrl`, unlike the keystrokes `replaceUrl` collapses:
choosing a sort is a deliberate act and is worth a history entry. Verified — selecting one
adds exactly one entry, and back restores both the previous ordering and the menu.

**Rows are centred one by one, not by centring their container.** The container is
`InfiniteList`'s, and it writes `margin: 0; padding: 0` as an *inline* style, which beats
any rule in the stylesheet — so a centred wrapper silently does nothing and the column
sits flush left. The rule goes on `.item` instead.

Two pixel-level corrections came out of measuring rather than looking: the scrollbar is
taken out of one side of the scrollport only, which pulled rows a few pixels left of the
header until `scrollbar-gutter: stable both-edges` made it symmetric; and a row's own
0.5rem of hover padding is subtracted from the wrapper, so row text lands on the same
column as the header while only the hover highlight bleeds past it.

**Padding files are dropped from expanded file lists.** They are alignment filler, not
content, and they can outnumber the real files — a ten-episode season came back as 19
entries, 9 of them padding. Two conventions are matched: BEP-47's `.pad/<size>` directory,
which is what this index actually contains (a sweep of 120 multi-file torrents found 24
distinct padding paths, all of that shape), and the older µTorrent `_____padding_file…`
name. The list says how many it hid rather than hiding the hiding.

**The list is the only scrolling element, and it reaches the bottom of the window.** The
document itself does not scroll, so there is one scrollbar and it sits where the
browser's own would. This is not a preference — Elm has no window-scroll subscription,
and `InfiniteList` reads `event.target.scrollTop`, which the document does not provide, so
a scrolling element is required. Making it the only one is how it stops looking like a
nested pane. The stylesheet sizes it as a flex child and the virtualizer is handed the
window height, an overestimate the package tolerates, rather than a second copy of the
layout arithmetic.

**A row with no file list says why, when it owes an explanation.** `over_threshold`
reports a file count and then offers nothing, which reads as a missing feature; it now
says bitmagnet declined to index them. `no_info` has no count to explain away and `single`
is a torrent that *is* its one file, so both stay silent.

**`/torrent/<hash>` only matches 40 hex characters.** bitmagnet answers a malformed hash
with `encoding/hex: odd length hex string`, which reached the page as-is before the route
was tightened. Anything else is now `NotFound`, and the segment is lowercased on the way
in so a link and a lookup always agree.

## Known gaps

Not bugs, but things a reader should not mistake for oversights.

- **Every expanded height is a constant in both `Main.elm` and `styles.css`.** The
  virtualizer positions rows from those numbers, so nothing inside an expanded panel may
  size itself to its content. Heights may still *vary per row*, since they are computed
  from each row's own data — the raw-name line is drawn only when it differs from the
  title, and the panel is 48px or 66px accordingly. Verified in the DOM: expanded rows
  measured 108px against a computed 108, and an expanded row with 23 files measured 632px
  against a computed 632.
- **The browser pane freezes the DOM, and it is not a bug in the app.** The preview tab
  reports `visibilityState: "hidden"`, so `requestAnimationFrame` never fires and Elm's
  virtual DOM stops patching while the model keeps updating. Reading the DOM after a
  navigation returns whatever was painted last — usually `Searching…`. Take a screenshot
  first to force a frame, then read.
- **Going back to a search refetches from offset 0.** Scroll depth is not addressable —
  that was the trade accepted for infinite scroll — but neither is the accumulated list,
  so a back press costs the rows you had already pulled.
- **A `+` in `?q=` is a literal plus, not a space.** `Url.Parser.Query` percent-decodes but
  does not honour the form-encoding convention. Links Magnes builds are unaffected — they
  encode a space as `%20` — but a hand-written `?q=a+b` searches for `a+b`.
