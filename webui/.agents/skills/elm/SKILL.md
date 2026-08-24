---
name: elm
description: Elm language, Elm Architecture, routing, and elm-graphql conventions for the Magnes codebase. Use when writing or reviewing any .elm file, wiring Browser.application/routing, building or regenerating the bitmagnet GraphQL client, decoding API data, or debugging Elm compiler errors.
---

# Elm in Magnes

Magnes is a single-page Elm app talking to bitmagnet's GraphQL API. The API client
is **generated, not hand-written** — treat `src/Magnes/Api/` as build output.

At runtime the endpoint is **read from `window.MAGNES_API_URL`**, set by
`public/config.js`, so one build works against any instance. Never hard-code it in Elm.

There is no Magnes server. An earlier draft of this file said requests were proxied
through one; that was never built, and the reason for it has expired — the bitmagnet fork
now enforces permissions on every GraphQL field itself. Two arrangements are real:

- **Cross-origin** — the endpoint is an absolute bitmagnet URL and the browser queries it
  directly. This works because bitmagnet's CORS defaults are permissive.
- **Same-origin** — bitmagnet serves the built `public/` itself via `http_server.static`,
  and the endpoint is the relative `/graphql`. No CORS at all.

Elm does not care which: it reads one string. Code generation always points at bitmagnet
directly, since the schema is identical either way. See `docs/serving-and-testing.md`.

## Toolchain

`elm` is not installed globally. Run everything through `npx`:

```bash
npx elm make src/Main.elm --output=dist/main.js   # dev build
npx elm make src/Main.elm --optimize --output=dist/main.js
npx elm repl
npx elm-format src/ --yes                          # canonical formatting, no debate
npx elm-test
```

Regenerate the API client from a live bitmagnet instance:

```bash
npx @dillonkearns/elm-graphql http://<host>:3333/graphql --base Magnes.Api --output src
```

This writes `src/Magnes/Api/{Query,Mutation,Object,InputObject,Enum,Scalar,...}.elm`.
Never hand-edit those files — a regeneration overwrites them. If a generated type is
awkward to use, wrap it in your own module rather than patching the generator output.

The one exception is `Magnes/Api/ScalarCodecs.elm`, which is generated *to be* edited and
survives regeneration; that's where custom scalars get mapped to real Elm types. bitmagnet
declares six: `Hash20`, `Date`, `DateTime`, `Duration`, `Void`, `Year`.

**The schema is documented in [references/bitmagnet-api.md](references/bitmagnet-api.md)** —
read it before writing a query. bitmagnet publishes no API reference, so that file is
transcribed from the schema files in its repo.
Generated code is committed (so builds don't need a live server) and excluded from
`elm-review` via `Review.Rule.ignoreErrorsForDirectories [ "src/Magnes/Api" ]`.

## Check for a package before building it

Do this **before** writing a feature, not after. Elm's ecosystem is small enough that the
registry is exhaustively searchable in a few seconds, and its packages are unusually safe
to adopt: enforced semantic versioning means a patch bump cannot change an API, and no
package can perform side effects the type system doesn't declare.

The whole registry is one 275KB JSON file — grep it locally rather than guessing names:

```bash
curl -s --compressed https://package.elm-lang.org/search.json -o /tmp/pkgs.json
python3 -c "
import json
d = json.load(open('/tmp/pkgs.json'))
for p in d:
    if 'scroll' in p['name'].lower() or 'scroll' in p['summary'].lower():
        print(p['name'], p['version'], '|', p['summary'])
"
```

The `--compressed` flag is required; the server rejects requests without it. ~2000
packages total.

Then vet the candidate before adding it:

1. **Check `elm.json` for the Elm version bound** — `"0.19.0 <= v < 0.20.0"`. Elm 0.19
   was a breaking change and plenty of registry entries are 0.18-only. This is the single
   most common reason a promising-looking package is unusable.
2. **Read the source, not just the README.** Elm packages are small; reading them is
   usually faster than integrating blind. Doc comments in particular go stale — several
   still show 0.18 syntax like `style [ ("height", "300px") ]` where 0.19 wants
   `style "height" "300px"`.
3. Prefer `elm/*` and `elm-community/*` where they cover the need.

Read the docs from raw GitHub sources — `package.elm-lang.org` is itself an Elm SPA and
returns an empty shell to any fetcher.

## The Elm Architecture

Model / view / update, wired by one of four program constructors. Magnes uses
`Browser.application` — it owns the whole page and handles URL changes:

```elm
application :
    { init : flags -> Url.Url -> Navigation.Key -> ( model, Cmd msg )
    , view : model -> Document msg
    , update : msg -> model -> ( model, Cmd msg )
    , subscriptions : model -> Sub msg
    , onUrlRequest : UrlRequest -> msg
    , onUrlChange : Url.Url -> msg
    }
    -> Program flags model msg
```

The others, for reference: `sandbox` (no effects), `element` (embeds in a JS page,
`view : model -> Html msg`), `document` (owns `<title>` and `<body>`, no URL handling).

`Browser.application` intercepts every link click instead of navigating:

```elm
type UrlRequest
    = Internal Url.Url
    | External String
```

Handle both — `Internal` with `Nav.pushUrl key (Url.toString url)`, `External` with
`Nav.load href`. Dropping either silently breaks links.

`Navigation.Key` is only obtainable from `application`'s `init`. Store it in the Model;
every `pushUrl`/`replaceUrl`/`back`/`forward` needs it.

- `pushUrl key url` — change URL, no page load, adds a history entry.
- `replaceUrl key url` — same, but no history entry. Use for filter/search churn so the
  back button doesn't have to walk every keystroke.
- `load url` / `reload` — full page load. Escape hatch only.

## Routing

Route matching lives in one `Route` custom type plus one parser. See
[references/routing.md](references/routing.md) for the full `Url.Parser` API with examples.

The essential shape:

```elm
type Route
    = Home
    | Search (Maybe String)
    | Torrent String
    | Dashboard
    | NotFound

route : Parser (Route -> a) a
route =
    oneOf
        [ map Home top
        , map Search (s "search" <?> Query.string "q")
        , map Torrent (s "torrent" </> string)
        , map Dashboard (s "dashboard")
        ]

toRoute : Url -> Route
toRoute url =
    Maybe.withDefault NotFound (parse route url)
```

**`Torrent` is an address, not a mode.** It renders the same expanded row the results list
renders — share `viewTorrentExpanded` between the two rather than writing a detail page.
Opened cold there is no list to draw from, so the route fetches that one torrent — via
`search` with `infoHashes: [hash]`, since bitmagnet has no get-by-hash query.

**Row expansion is local state and must stay out of the URL.** Scanning means expanding
several rows in passing; if each pushed a history entry the back button would fill with
them. Expansion lives on the record in the model (which is also what the virtualized list
requires — see below), and several rows may be open at once.

Rows should still link to `/torrent/<hash>` with a real `<a href>`, so middle-click and
open-in-new-tab work. `Browser.application` intercepts the plain click and routes it
through `onUrlRequest` as `Internal`; the browser handles the modified clicks natively.

Search terms, filters, and sort belong in the URL (`<?>` + `Url.Parser.Query`), not only
in the Model — that makes results linkable and back/forward meaningful. Scroll offset
does **not** go in the URL: results load on scroll, and depth is deliberately not
addressable. Use `replaceUrl` for keystroke-level churn so back doesn't unwind one entry
per character.

## elm-graphql

Full API notes in [references/elm-graphql.md](references/elm-graphql.md). The rules that
matter most:

**Build selections with `SelectionSet.mapN`, not the `succeed |> with` pipeline.** The
pipeline typechecks but produces much worse error messages when a field is added or
reordered:

```elm
rowSelection : SelectionSet Row Magnes.Api.Object.TorrentContent
rowSelection =
    SelectionSet.map4 Row
        TorrentContent.id
        TorrentContent.title
        TorrentContent.seeders
        (TorrentContent.torrent torrentSelection)
```

Search returns `TorrentContent`, which *nests* the `Torrent` — so selections nest too.
See [references/bitmagnet-api.md](references/bitmagnet-api.md) for the schema.

**Optional arguments are three-state**, not `Maybe`:

```elm
type OptionalArgument a = Present a | Absent | Null
```

Absent and null can mean different things to the server. Where a *field* has optional
arguments, the generated function takes a record-updater for them:

```elm
SomeQuery.field (\optionals -> { optionals | limit = Present 50 }) selection
```

**bitmagnet mostly doesn't work that way.** Its queries take a single non-null `input:`
object, so the optionality lives in the input object's *fields* rather than in the
argument list, and the query is nested two levels:

```elm
Query.torrentContent
    (TorrentContentQuery.search
        { input = InputObject.buildTorrentContentSearchQueryInput
            (\opts -> { opts | queryString = Present term, limit = Present 50 })
        }
        resultSelection
    )
```

The exact generated names above are **inferred, not verified** — nothing has been
generated yet. Check them against `src/Magnes/Api/` after the first codegen run.

**Sending a request:**

```elm
Graphql.Http.queryRequest endpoint query
    |> Graphql.Http.send GotResponse
```

`send : (Result (Error decodesTo) decodesTo -> msg) -> Request decodesTo -> Cmd msg`.
The error type distinguishes GraphQL errors (possibly with partial data) from HTTP
errors — don't collapse both into a bare "something went wrong" string in the Model
until the view actually needs one.

## Modeling

Custom types are the main tool. Encode states so impossible ones don't typecheck:

```elm
type Data e a
    = Loading
    | Failure e
    | Success a
```

Prefer this over `{ isLoading : Bool, error : Maybe String, items : List Torrent }`,
which admits four states that can't happen. The view then must handle each case, so a
loading spinner can't be forgotten.

Same for messages — name the interaction, not the mutation:

```elm
type Msg
    = ChangedQuery String
    | SubmittedSearch
    | GotTorrents (Result (Graphql.Http.Error TorrentPage) TorrentPage)
    | ClickedLink Browser.UrlRequest
    | ChangedUrl Url
```

## Module structure

Follow the official guidance, which cuts against instincts from React/Vue:

- **Build modules around a type**, not around a layer. `Page.Search`, `Page.Torrent`,
  `Torrent` — never `Models.elm` / `Update.elm` / `View.elm`. The shared expanded-row view
  belongs in `Torrent` (the type), used by both pages. Layer-based splits create
  boundaries where "which file does this go in?" has no answer.
- **Long files are fine.** Elm has no mutable state to lose track of; a 1000-line
  `Page.Search` is healthier than six files that must be read together.
- **Don't build components.** A sidebar is a `viewSidebar : Model -> Html Msg` function,
  not a module with its own Model/Msg/update. Nesting TEA is a real cost and is only
  worth paying for genuinely independent state.
- **Don't factor out shared code early.** Two pages that look similar usually diverge.
  The compiler makes it safe to unify later, once the shape is actually known.

## Infinite scroll and large lists

Results append as the user reaches the bottom — no page numbers — so within a query the
loaded list only ever grows. The results list is virtualized (see below), which handles
rendering cost there. For **any other** long list, two tools apply:

- `Html.Keyed.node "tbody" [] [ ( id, viewRow t ) ]` — stable identity per row, so
  appending a batch doesn't rebuild every row above it. Key on `TorrentContent.id`, never
  on list index (an index key changes meaning as the list grows) and never on `infoHash`
  (one torrent can surface as several content rows, so hashes collide).
- `Html.Lazy.lazy fn arg` — skips re-render when `arg` is unchanged *by reference*. Pass
  the smallest data the function needs: `lazy viewResults model.torrents` works,
  `lazy viewPage model` never hits.

Model the fetch state so a scroll event during an in-flight request can't fire a second
one, and so the end of results is distinguishable from a pending load:

```elm
type alias SearchState =
    { query : Query
    , loaded : List TorrentContent
    , more : More
    }

type More
    = Idle Int          -- next offset
    | Loading Int
    | Exhausted
    | Failed Int Error
```

Pagination is **offset-based** — bitmagnet's schema has no cursors. Two consequences that
are easy to get wrong, both detailed in [references/bitmagnet-api.md](references/bitmagnet-api.md):

- `hasNextPage` and `totalCount` are **opt-in request flags that fail silently**. Omit
  them and the response says `hasNextPage: false` and `totalCount: 0` — not null. The
  scroll stops dead after the first batch and looks like "no more results". Set both on
  every search.
- **Dedupe by `id` when appending.** bitmagnet is a live crawler; new rows shift offsets
  under a recency sort, so a batch routinely repeats rows already shown. This is the
  steady state, not a rare race.

Append with `loaded ++ batch`, and note that `++` on a long left operand is O(n) — for
very deep scrolls, accumulate reversed and reverse once in the view, or keep batches as a
`List (List Torrent)`.

Use **`FabienHenon/elm-infinite-list-view`** for the results list, from the start rather
than as a later optimization. Rationale and the rejected alternatives are in
[references/packages.md](references/packages.md); the short version is that it's the only
candidate that doesn't seize the scroll container, so the same handler can also detect
near-bottom and fire the next fetch. Don't add a second scroll package — Elm allows one
`on "scroll"` per node.

The package renders only visible rows and applies `lazy3` per item itself, so the manual
`Html.Keyed`/`Html.Lazy` work above is *not* additionally needed inside the results list.
It still applies to any other long list.

### Row heights must be a pure function of the item

Rows expand in place: collapsed → metadata → file list. Use `withVariableHeight`, and keep
every state's height computable without measuring:

| Row state | Height |
| --- | --- |
| Collapsed | constant — name truncated with `text-overflow: ellipsis` |
| Expanded | constant — metadata panel |
| Expanded, files loading | constant placeholder |
| Expanded, files loaded | `min cap (header + fileCount * rowHeight)` |

The file level often doesn't exist: `torrent.filesStatus` is `no_info` for roughly 4 rows
in 5 on a real index. Branch on it before offering the expander — the states are
`no_info | single | multi | over_threshold` and only `multi` has a list worth fetching.

The loading placeholder is what keeps the function total: a torrent's file list needs its
own fetch, so the count is unknown at click time. Transitioning to the computed height
only once the files arrive gives exactly one layout shift instead of a jumping scrollbar.
`cap` matters because a torrent can hold thousands of files — past it, scroll inside the
expanded panel.

**Expansion state belongs on the record in the model**, not zipped in at view time. The
package's internal `lazy3` compares `item` by reference, so building wrapper records in
the view allocates fresh ones each render and silently disables it.

Debounce search-as-you-type before it reaches the network.

## Gotchas

- Elm has no runtime exceptions in practice, but `Debug.log`/`Debug.todo` block
  `--optimize`. Strip them before a production build.
- `elm-format` is not configurable and not a matter of taste — run it, don't argue with it.
- A missing `case` branch is a compile error. That's the point; don't add a catch-all `_ ->`
  to silence it unless the behavior really is "everything else does nothing."
- Record update syntax needs an existing value: `{ model | query = q }`. There is no
  partial-record literal.
- `==` doesn't work on functions and will fail at runtime; it doesn't work on custom types
  containing them either.
