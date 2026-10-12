# Elm package notes

Findings from surveying the registry, kept so the same reading isn't repeated. Version
bounds were verified against each package's `elm.json` at the time of writing — re-check
before adopting.

## Searching the registry

```bash
curl -s --compressed https://package.elm-lang.org/search.json -o /tmp/pkgs.json
```

~2000 entries of `{name, summary, license, version}`. `--compressed` is mandatory; the
server returns a 406 scolding without it. Grep by name *and* summary — Elm package names
are often oblique (`lue-bird/elm-scroll` is a zipper, not scrolling).

Docs must be read from `raw.githubusercontent.com`; `package.elm-lang.org` is an Elm SPA
that serves an empty shell to fetchers. Useful raw paths:

- `https://raw.githubusercontent.com/<owner>/<repo>/master/elm.json` — version bounds
- `https://raw.githubusercontent.com/<owner>/<repo>/master/README.md`
- `https://raw.githubusercontent.com/<owner>/<repo>/master/src/<Module>.elm` — real docs

## Virtualized lists and load-on-scroll

Two problems that look separate — *when to fetch more* and *what to render* — and it is
tempting to pick a package for each. **They do not compose**: both want to be the
`on "scroll"` handler on the same element, and Elm allows one per node.

So it is one package plus a scroll decoder you write yourself. Pick the package that
leaves you the scroll container.

### `FabienHenon/elm-infinite-list-view` 3.3.1 — the one that composes

`0.19.0 <= v < 0.20.0`. Deps: `elm/browser`, `elm/core`, `elm/html`, `elm/json`.

Renders only visible items. Crucially it **does not own the scroll container** — you build
the container and attach the listener, which leaves room to detect near-bottom and fire the
next fetch from the same handler.

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
  by reference. Building wrapper records in the view (`List.map2 (\item open ->
  { item = item, open = open }) …`) allocates fresh records every render and silently
  defeats this. Per-row view state must live on the record stored in the model.
- `containerHeight` may be an overestimate — it just renders a few extra rows. Use the
  window height if the real height is unknown.

### `dominikmayer/elm-virtual-list` 2.1.0 — better virtualizer, no load-on-scroll

It *measures* rendered heights instead of asking you to compute them, which handles
arbitrary content. Unusable if you also need to fetch on scroll:

- It **owns the scroll container** (`overflow: auto` plus its own `onScroll`).
- It exposes **no end-of-list hook, no scroll position, no visible range**. `visibleRows`
  is computed internally and kept private.

Together those make load-on-scroll impossible without forking it. The right choice if the
list is fully loaded and only rendering cost matters; revisit for the other case only if it
later exposes the visible range.

### `FabienHenon/elm-infinite-scroll` 3.0.4 — load-more only, conflicts with the above

Does the load-more half well (`infiniteScroll` attribute, `Direction = Top | Bottom`,
`stopLoading` to re-arm). Skip it when pairing with a virtualizer: they fight over the
scroll handler, and replacing it is ~15 lines — decode `scrollTop` / `scrollHeight` /
`clientHeight` in one handler, feed the virtualizer's scroll update, and compare against a
threshold.

Two traps if used alone: the container needs an explicit height or no event fires, and
forgetting `stopLoading` makes scrolling silently die after one page. Its module doc is
0.18-era (`style [ ("height", "300px") ]`) though the package is 0.19.

## Page-number pagination

`jschomay/elm-paginate`, `prikhi/paginate`, `correl/elm-paginated` all solve page-number
pagination. They are the answer to a different question than load-on-scroll — reach for
them only if the UI actually has page numbers.

## Worth knowing about

- **Styling** — `mdgriffith/elm-ui` versus plain `elm/html` + CSS. Architectural, and
  expensive to reverse. Decide early.
- **Remote data** — `krisajenkins/remotedata` is the standard four-state wrapper. With
  elm-graphql, note that `Graphql.Http.Error` already separates GraphQL from HTTP failures,
  so a hand-rolled custom type often models the real states better than a generic wrapper.
