# Package survey

Candidates already researched for Magnes, with version bounds verified against each
package's `elm.json`. Re-check bounds before adopting — this file is a snapshot.

## Searching the registry

```bash
curl -s --compressed https://package.elm-lang.org/search.json -o /tmp/pkgs.json
```

~2000 entries of `{name, summary, license, version}`. `--compressed` is mandatory; the
server returns a 406 scolding without it. Grep locally by name *and* summary — Elm package
names are often oblique (`lue-bird/elm-scroll` is a zipper, not scrolling).

Docs must be read from `raw.githubusercontent.com`; `package.elm-lang.org` is an Elm SPA
that serves an empty shell to fetchers. Useful raw paths:

- `https://raw.githubusercontent.com/<owner>/<repo>/master/elm.json` — version bounds
- `https://raw.githubusercontent.com/<owner>/<repo>/master/README.md`
- `https://raw.githubusercontent.com/<owner>/<repo>/master/src/<Module>.elm` — real docs

## Scrolling — decided: `FabienHenon/elm-infinite-list-view`

There are two problems here — *when to fetch more* and *what to render* — and it is
tempting to pick a package for each. **They do not compose**, because both need to be the
`on "scroll"` handler on the same element, and Elm allows only one per node.

So it is one package plus a scroll decoder you write yourself.

### Chosen: `FabienHenon/elm-infinite-list-view` 3.3.1

`0.19.0 <= v < 0.20.0`. Deps: `elm/browser`, `elm/core`, `elm/html`, `elm/json`.

Renders only visible items. Crucially it **does not own the scroll container** — you build
the container and attach the listener, which leaves room to also detect near-bottom and
fire the next fetch from the same handler.

```elm
init   : Model
config : { itemView : Int -> Int -> item -> Html msg
         , itemHeight : ItemHeight item
         , containerHeight : Int
         } -> Config item msg
view     : Config item msg -> Model -> List item -> Html msg
onScroll : (Model -> msg) -> Html.Attribute msg

type ItemHeight item = Constant Int | Variable (Int -> item -> Int)
withConstantHeight : Int -> ItemHeight item
withVariableHeight : (Int -> item -> Int) -> ItemHeight item
withKeepFirst      : Int -> Config item msg -> Config item msg   -- pins first N rows
scrollToNthItem    : { postScrollMessage, listHtmlId, itemIndex, configValue, items } -> Cmd msg
```

Facts verified by reading `src/InfiniteList.elm`, not the README:

- **Heights are recomputed on every view; nothing is cached.** `computeElementsAndSizes`
  runs fresh each render, so an item that changes height at runtime just works. There is
  no invalidation call to forget.
- **`Constant` is O(1); `Variable` is O(loaded items) per scroll event.**
  `computeElementsAndSizesForSimpleHeight` divides to find the skip count;
  `computeElementsAndSizesForMultipleHeights` folds from index 0 to accumulate offsets.
  Fine at ~10k rows, degrades at extreme scroll depth.
- **It applies `lazy3 itemView idx listIdx item` internally.** `item` is therefore compared
  by reference. Building wrapper records in the view (`List.map2 (\t e -> { torrent = t,
  expanded = e }) …`) allocates fresh records every render and silently defeats this.
  Expansion state must live on the record stored in the model.
- `containerHeight` may be an overestimate — it just renders a few extra rows. Use the
  window height if the real height is unknown.

### Rejected: `dominikmayer/elm-virtual-list` 2.1.0

The better virtualizer — it *measures* rendered heights instead of asking you to compute
them, which handles arbitrary content. Unusable here regardless:

- It **owns the scroll container** (`overflow: auto` plus its own `onScroll`).
- It exposes **no end-of-list hook, no scroll position, no visible range**. `visibleRows`
  is computed internally and kept private.

Together those make load-on-scroll impossible without forking it. Worth revisiting only if
it later exposes the visible range.

### Rejected: `FabienHenon/elm-infinite-scroll` 3.0.4

Does the load-more half well (`infiniteScroll` attribute, `Direction = Top | Bottom`,
`stopLoading` to re-arm). Dropped because it conflicts with the list view over the scroll
handler, and replacing it is ~15 lines: decode `scrollTop`/`scrollHeight`/`clientHeight`
in one handler, feed `InfiniteList.updateScroll`, and compare against a threshold.

If it is ever reconsidered, two traps: the container needs an explicit height or no event
fires, and forgetting `stopLoading` makes scrolling silently die after one page. Its module
doc is also 0.18-era (`style [ ("height", "300px") ]`) though the package is 0.19.

## Pagination — not applicable

`jschomay/elm-paginate`, `prikhi/paginate`, `correl/elm-paginated` all solve page-number
pagination. Magnes decided against page numbers (see README), so these are listed only so
nobody re-discovers them and assumes they're the answer.

## Still to survey

Nothing chosen yet for these; check the registry before hand-rolling.

- **Date/size formatting** — bitmagnet returns timestamps and byte counts; `elm/time` +
  something for human-readable durations.
- **Styling** — `mdgriffith/elm-ui` vs plain `elm/html` + CSS. Architectural; decide
  early, it is expensive to reverse.
- **Remote data** — `krisajenkins/remotedata` is the standard, but note elm-graphql's
  `Graphql.Http.Error` already distinguishes GraphQL from HTTP failures, and a hand-rolled
  4-case custom type may model the loading states better than a generic wrapper.
