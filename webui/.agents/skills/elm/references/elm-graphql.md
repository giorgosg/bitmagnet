# elm-graphql reference

`dillonkearns/elm-graphql`. Everything here is from the package docs and FAQ.

## Code generation

```bash
npx @dillonkearns/elm-graphql http://<host>:3333/graphql --base Magnes.Api --output src
```

With auth, if bitmagnet is behind one:

```bash
npx @dillonkearns/elm-graphql <url> --base Magnes.Api --output src --header 'headerKey: header value'
```

Generated module layout (with `--base Magnes.Api`):

| Module | Contents |
| --- | --- |
| `Magnes.Api.Query` | one function per root query field |
| `Magnes.Api.Mutation` | one function per root mutation field |
| `Magnes.Api.Object` | phantom types, one per GraphQL object — the `scope` in `SelectionSet a scope` |
| `Magnes.Api.Object.Torrent` (etc.) | field selectors for that object |
| `Magnes.Api.InputObject` | record types + constructors for GraphQL input objects |
| `Magnes.Api.Enum.*` | Elm custom types for GraphQL enums |
| `Magnes.Api.Scalar` | opaque wrappers for custom scalars |
| `Magnes.Api.ScalarCodecs` | the one generated file intended to be edited (see below) |

## SelectionSet

`SelectionSet decodesTo scope` — a set of fields to fetch from `scope`, plus the decoder
for the result. The `scope` phantom type is what stops you selecting a `Torrent` field
inside a `TorrentContent` selection.

```elm
import Graphql.Operation exposing (RootQuery)
import Graphql.SelectionSet as SelectionSet exposing (SelectionSet)
import StarWars.Object
import StarWars.Object.Human as Human
import StarWars.Query as Query
import StarWars.Scalar exposing (Id(..))

query : SelectionSet (Maybe HumanData) RootQuery
query =
    Query.human { id = Id "1001" } humanSelection

type alias HumanData =
    { name : String
    , homePlanet : Maybe String
    }

humanSelection : SelectionSet HumanData StarWars.Object.Human
humanSelection =
    SelectionSet.map2 HumanData
        Human.name
        Human.homePlanet
```

### Combining

```elm
map  : (a -> b) -> SelectionSet a scope -> SelectionSet b scope
map2 : (a -> b -> c) -> SelectionSet a scope -> SelectionSet b scope -> SelectionSet c scope
-- ... through map8
```

`mapN` is **preferred over the pipeline form** — the docs say so explicitly, for clearer
error messages. Reach for the pipeline only past 8 fields.

### Pipeline form

```elm
succeed   : a -> SelectionSet a scope
with      : SelectionSet a scope -> SelectionSet (a -> b) scope -> SelectionSet b scope
hardcoded : a -> SelectionSet (a -> b) scope -> SelectionSet b scope
```

```elm
SelectionSet.succeed Hero
    |> with Character.name
    |> hardcoded "Star Wars"
```

### Narrowing nullability

The schema is often more permissive than reality. These turn a decode into a failure
rather than pushing `Maybe`s through the whole app:

```elm
nonNullOrFail         : SelectionSet (Maybe a) scope -> SelectionSet a scope
nonNullElementsOrFail : SelectionSet (List (Maybe a)) scope -> SelectionSet (List a) scope
withDefault           : a -> SelectionSet (Maybe a) scope -> SelectionSet a scope
mapOrFail             : (a -> Result String b) -> SelectionSet a scope -> SelectionSet b scope
```

Use `nonNullOrFail` only where a null genuinely indicates a broken server — it converts a
`Maybe` into a request-level error. `withDefault` is usually the kinder choice for
display fields.

`mapOrFail` is the hook for parsing stringly-typed scalars (dates, sizes) into real types
at the edge.

## Optional arguments

```elm
type OptionalArgument a
    = Present a
    | Absent
    | Null
```

> "An optional argument can be either present, absent, or null, so using a Maybe does not
> fully capture the GraphQL concept of an optional argument."

The distinction is load-bearing: a mutation may delete a field on `Null` but ignore it
when `Absent`.

Generated functions with optional args take a record-updating function:

```elm
Query.hero
    (\optionals ->
        { optionals
            | episode = Present Episode.EMPIRE
        }
    )
    hero
```

Convert from `Maybe` with `Graphql.OptionalArgument.fromMaybe` (`Nothing -> Absent`).

**FAQ gotcha:** a field can be optional per the schema yet still fail at runtime if the
server requires at least one of a set of arguments. The schema can't express that
constraint, so it surfaces as a GraphQL error, not a compile error.

## Sending requests

```elm
queryRequest    : String -> SelectionSet decodesTo RootQuery -> Request decodesTo
mutationRequest : String -> SelectionSet decodesTo RootMutation -> Request decodesTo
send            : (Result (Error decodesTo) decodesTo -> msg) -> Request decodesTo -> Cmd msg

withHeader  : String -> String -> Request decodesTo -> Request decodesTo
withTimeout : Float -> Request decodesTo -> Request decodesTo
```

```elm
searchTorrents : String -> String -> Cmd Msg
searchTorrents endpoint term =
    Query.torrentContent
        (\opts -> { opts | queryString = Present term })
        resultSelection
        |> Graphql.Http.queryRequest endpoint
        |> Graphql.Http.send GotTorrents
```

## Errors

```elm
type alias Error parsedData =
    RawError parsedData HttpError

type RawError parsedData httpError
    = GraphqlError (GraphqlError.PossiblyParsedData parsedData) (List GraphqlError.GraphqlError)
    | HttpError httpError

type HttpError
    = BadUrl String
    | Timeout
    | NetworkError
    | BadStatus Http.Metadata String
    | BadPayload Json.Decode.Error
```

Two distinct failure modes. `GraphqlError` can carry *partial* data — a response with
some fields resolved and others errored. Decide deliberately whether to show the partial
result or treat it as total failure; don't let the distinction vanish at the boundary.

`BadPayload` means the response didn't match the generated decoder — almost always a
sign the committed generated code is stale relative to the server. Regenerate.

## Custom scalars

Custom scalars default to `String`-backed opaque wrappers in `Magnes.Api.Scalar` because
GraphQL gives no type-safe way to describe them. Two ways to get real types:

1. Edit `Magnes.Api.ScalarCodecs` — the generated file designed to be customized. It maps
   each scalar to an Elm type plus encoder/decoder, and regeneration preserves it. This is
   the right place for bitmagnet's date and hash scalars.
2. Unwrap at the call site with `SelectionSet.map` / `mapOrFail`. Fine for one-offs,
   repetitive if the scalar is common.

## Static analysis

Generated code trips lint rules. Exclude it:

```elm
Review.Rule.ignoreErrorsForDirectories [ "src/Magnes/Api" ]
```
