---
name: elm
description: Elm 0.19 conventions - the Elm Architecture, Browser.application routing, elm-graphql clients, modeling with custom types, and rendering long lists. Use when writing or reviewing any .elm file, wiring routes, querying a GraphQL API from Elm, choosing an Elm package, or reading Elm compiler errors.
---

# Elm

Elm 0.19. The compiler is the design tool: most of what follows is about giving it enough
information to reject the broken version.

## Toolchain

Assume `elm` is not installed globally; run through `npx`:

```bash
npx elm make src/Main.elm --output=dist/main.js
npx elm make src/Main.elm --optimize --output=dist/main.js
npx elm repl
npx elm-format src/ --yes
npx elm-test
```

Check the project's `package.json` scripts first — most projects wrap these, and the
wrapper knows the real paths.

`elm-format` has no options and no style debate. Run it; don't argue with it.

## Make impossible states impossible

The central Elm move, and the one that most changes what code gets written. Encode states
as a custom type so the broken combinations cannot be constructed:

```elm
type Data e a
    = Loading
    | Failure e
    | Success a
```

Prefer this to `{ isLoading : Bool, error : Maybe String, items : List a }`, which admits
four states that cannot happen and lets the view forget one. With the custom type the
`case` is exhaustive, so a missing branch is a compile error.

Name messages for the interaction, not the mutation — `ChangedQuery String`,
`SubmittedSearch`, `GotResults (Result Error Page)`. A message named after the state
change it performs (`SetQuery`) puts update logic in the constructor's name and makes two
callers with different intent share one message.

## The Elm Architecture

Model / view / update, wired by one of four program constructors:

| Constructor | Owns | Use when |
| --- | --- | --- |
| `sandbox` | nothing | no effects at all |
| `element` | one DOM node | embedding in a JS page |
| `document` | `<title>` and `<body>` | whole page, no URL handling |
| `application` | the page and its URL | single-page app with routes |

`Browser.application` is the one with routing:

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

It intercepts every link click rather than navigating:

```elm
type UrlRequest
    = Internal Url.Url
    | External String
```

Handle both — `Internal` with `Nav.pushUrl key (Url.toString url)`, `External` with
`Nav.load href`. Dropping either silently breaks links.

`Navigation.Key` is obtainable only from `application`'s `init`. Store it in the Model;
every `pushUrl` / `replaceUrl` / `back` / `forward` needs it.

- `pushUrl key url` — change URL, no page load, adds a history entry.
- `replaceUrl key url` — same, no history entry. Use for keystroke-level churn (search
  terms, filter toggles) so the back button doesn't have to walk every character.
- `load url` / `reload` — full page load. Escape hatch only.

## Routing

Route matching is one `Route` custom type plus one parser plus its inverse. Full
`Url.Parser` API with worked examples in [references/routing.md](references/routing.md).

```elm
type Route
    = Home
    | Search (Maybe String)
    | Item String
    | NotFound

route : Parser (Route -> a) a
route =
    oneOf
        [ map Home top
        , map Search (s "search" <?> Query.string "q")
        , map Item (s "item" </> string)
        ]

toRoute : Url -> Route
toRoute url =
    Maybe.withDefault NotFound (parse route url)
```

**Write the inverse (`toHref : Route -> String`) alongside the parser and build every link
through it.** A `case` there means adding a route variant is a compile error at the link
builder rather than a dead link discovered later.

**Put shareable state in the URL; keep ephemeral state in the Model.** A query, a filter
and a sort order make a link worth sending, so they belong in query parameters. Scroll
depth and which rows a user expanded in passing do not — putting them in the URL fills the
back button with entries nobody meant to create.

Real paths (rather than `#/` fragments) require whatever serves the app to fall back to
`index.html` on unmatched paths, or a deep link 404s on refresh. Confirm the deployment
does that before choosing them, and keep top-level routes clear of paths the host owns.

## elm-graphql

`dillonkearns/elm-graphql` generates a type-safe client by introspecting a live schema.
Full API notes in [references/elm-graphql.md](references/elm-graphql.md).

```bash
npx @dillonkearns/elm-graphql <endpoint> --base Api --output src
```

**Generated code is build output.** Never hand-edit it — regeneration overwrites. If a
generated type is awkward, wrap it in your own module. Commit the output so a checkout
builds without reaching a server, and exclude it from review:

```elm
Review.Rule.ignoreErrorsForDirectories [ "src/Api" ]
```

The one exception is `Api/ScalarCodecs.elm`, generated *to be* edited and preserved across
regeneration. That is where custom scalars become real Elm types instead of opaque
`String` wrappers.

**Build selections with `SelectionSet.mapN`, not the `succeed |> with` pipeline.** Both
typecheck; the pipeline produces far worse errors when a field is added or reordered.
Reach for the pipeline only past 8 fields.

```elm
itemSelection : SelectionSet Item Api.Object.Item
itemSelection =
    SelectionSet.map3 Item
        Item.id
        Item.title
        (Item.owner ownerSelection)
```

**Optional arguments are three-state, not `Maybe`:**

```elm
type OptionalArgument a = Present a | Absent | Null
```

Absent and null can mean different things to a server. Fields with optional arguments take
a record-updater: `Query.field (\opts -> { opts | limit = Present 50 }) selection`.

**Keep the two failure modes apart.** `Graphql.Http.Error` distinguishes a GraphQL error
(which may carry *partial* data) from an HTTP error. Decide deliberately which to show;
collapsing both into one "something went wrong" string at the boundary throws away the
distinction before the view can use it.

A `BadPayload` almost always means the committed generated code is stale. Regenerate
before debugging anything else.

## Modules

Follow the official guidance, which cuts against React and Vue instincts:

- **Build modules around a type, not a layer.** `Page.Search`, `Item` — never
  `Models.elm` / `Update.elm` / `View.elm`. Layer splits create boundaries where "which
  file does this go in?" has no answer.
- **Long files are fine.** There is no mutable state to lose track of; a 1000-line page
  module is healthier than six files that must be read together.
- **Write functions, not components.** A sidebar is `viewSidebar : Model -> Html Msg`, not
  a module with its own Model/Msg/update. Nesting TEA costs real complexity and pays only
  for genuinely independent state.
- **Let duplication sit.** Two similar pages usually diverge. The compiler makes unifying
  them safe later, once the shape is known.

## Long lists

Three tools, in increasing order of cost:

`Html.Keyed.node "tbody" [] [ ( id, viewRow item ) ]` gives each row a stable identity, so
appending a batch doesn't rebuild the rows above it. Key on a stable domain id — never the
list index, whose meaning shifts as the list grows.

`Html.Lazy.lazy fn arg` skips re-render when `arg` is unchanged **by reference**. Pass the
smallest data the function needs: `lazy viewResults model.results` works, `lazy viewPage
model` never hits.

Virtualization — rendering only visible rows — is worth adopting **from the start** rather
than retrofitting, because the code deciding *what to render* and the code deciding *when
to fetch more* both want to own the scroll handler, and Elm allows one `on "scroll"` per
node. See [references/packages.md](references/packages.md) for the survey and the choice.

If rows vary in height, keep height a **pure function of the item**, computable without
measuring. A row whose height is only known after a fetch needs a fixed-size placeholder
for the pending state — that costs exactly one layout shift when the data arrives, instead
of a scrollbar that jumps on every response.

Model fetch state so a scroll event during an in-flight request cannot fire a second one,
and so "no more results" is distinguishable from "still loading":

```elm
type More
    = Idle Int          -- next offset
    | Loading Int
    | Exhausted
    | Failed Int Error
```

Note that `++` on a long left operand is O(n). For very deep lists, accumulate reversed
and reverse once in the view, or keep batches as a `List (List a)`.

## Check the registry before building it

Do this **before** writing a feature. Elm's registry is small enough to search
exhaustively in seconds, and its packages are unusually safe to adopt: enforced semantic
versioning means a patch bump cannot change an API, and no package performs side effects
its types don't declare.

The whole registry is one ~275KB JSON file — grep it locally rather than guessing names:

```bash
curl -s --compressed https://package.elm-lang.org/search.json -o /tmp/pkgs.json
```

`--compressed` is mandatory; the server returns a 406 without it. Search **name and
summary both** — Elm package names are often oblique.

Then vet the candidate:

1. **Check `elm.json` for the version bound** — `"0.19.0 <= v < 0.20.0"`. Elm 0.19 was a
   breaking change and many registry entries are 0.18-only. This is the most common reason
   a promising package turns out to be unusable.
2. **Read the source, not the README.** Elm packages are small, and reading one is usually
   faster than integrating it blind. Doc comments go stale — plenty still show 0.18 syntax
   like `style [ ("height", "300px") ]` where 0.19 wants `style "height" "300px"`.
3. Prefer `elm/*` and `elm-community/*` where they cover the need.

Read docs from `raw.githubusercontent.com`. `package.elm-lang.org` is itself an Elm SPA and
serves an empty shell to any fetcher.

## Gotchas

- `Debug.log` and `Debug.todo` block `--optimize`. Strip them before a production build.
- A missing `case` branch is a compile error, which is the point. Add `_ ->` only when the
  behaviour genuinely is "everything else does nothing".
- Record update needs an existing value: `{ model | query = q }`. There is no partial
  record literal.
- `==` on functions fails at **runtime**, not compile time — including custom types that
  contain them.
- `parse` needs a real `Url`; a bare path string doesn't parse into one. Only a trap in
  tests, where there is no `init` to take one from.
