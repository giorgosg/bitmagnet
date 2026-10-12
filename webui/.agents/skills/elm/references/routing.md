# Routing reference

`elm/url` — `Url.Parser` and `Url.Parser.Query`. Examples below are from the package docs.

## Parser type

`Parser a b` is a continuation-passing type; route parsers are written
`Parser (Route -> a) a` and consumed by `parse`.

## oneOf — the route table

```elm
type Route
  = Topic String
  | Blog Int
  | User String
  | Comment String Int

route : Parser (Route -> a) a
route =
  oneOf
    [ map Topic   (s "topic" </> string)
    , map Blog    (s "blog" </> int)
    , map User    (s "user" </> string)
    , map Comment (s "user" </> string </> s "comment" </> int)
    ]
```

Order matters — first match wins.

## Segments

`s "literal"` matches a fixed segment; `string` and `int` capture one; `</>` joins them.

```elm
blog : Parser (Int -> a) a
blog =
  s "blog" </> int

-- /blog/42  ==>  Just 42
-- /tree/42  ==>  Nothing
-- /blog/    ==>  Nothing
```

`int` fails on non-numeric segments, so `/blog/wolf` falls through to the next alternative
rather than erroring.

`top` matches zero segments — that's the root route, and it's also how you give a nested
group an index page:

```elm
type Route = Overview | Post Int

blog : Parser (Route -> a) a
blog =
  s "blog" </>
    oneOf
      [ map Overview top
      , map Post (s "post" </> int)
      ]

-- /blog/         ==>  Just Overview
-- /blog/post/42  ==>  Just (Post 42)
```

## Query parameters

`<?>` attaches a `Url.Parser.Query` parser. This is where search terms, filters, and
paging belong.

```elm
import Url.Parser.Query as Query

type Route
  = Overview (Maybe String)
  | Post Int

blog : Parser (Route -> a) a
blog =
  oneOf
    [ map Overview (s "blog" <?> Query.string "q")
    , map Post (s "blog" </> int)
    ]

-- /blog/           ==>  Just (Overview Nothing)
-- /blog/?q=wolf    ==>  Just (Overview (Just "wolf"))
-- /blog/wolf       ==>  Nothing
-- /blog/42         ==>  Just (Post 42)
-- /blog/42?q=wolf  ==>  Just (Post 42)
```

Query parsers: `Query.string`, `Query.int`, `Query.enum`, `Query.map`..`Query.map8`,
`Query.custom` (for repeated keys like `?tag=a&tag=b`). All return `Maybe`, so a bad
value degrades to `Nothing` rather than failing the whole route.

Note from the last two lines: unrecognized query params never break a match. A route can
gain a filter without invalidating existing links.

## map — building the value

```elm
type alias Comment = { user : String, id : Int }

userAndId : Parser (String -> Int -> a) a
userAndId =
  s "user" </> string </> s "comment" </> int

comment : Parser (Comment -> a) a
comment =
  map Comment userAndId

-- /user/bob/comment/42  ==>  Just { user = "bob", id = 42 }
```

## fragment

```elm
type alias Docs =
  (String, Maybe String)

docs : Parser (Docs -> a) a
docs =
  map Tuple.pair (string </> fragment identity)

-- /List/map   ==>  Nothing
-- /List/#map  ==>  Just ("List", Just "map")
-- /List#      ==>  Just ("List", Just "")
-- /List       ==>  Just ("List", Nothing)
```

## parse — running it

```elm
import Url
import Url.Parser exposing (Parser, parse, int, map, oneOf, s, top)

type Route = Home | Blog Int | NotFound

route : Parser (Route -> a) a
route =
  oneOf
    [ map Home top
    , map Blog (s "blog" </> int)
    ]

toRoute : String -> Route
toRoute string =
  case Url.fromString string of
    Nothing ->
      NotFound

    Just url ->
      Maybe.withDefault NotFound (parse route url)

-- toRoute "/blog/42"                            ==  NotFound
-- toRoute "https://example.com/"                ==  Home
-- toRoute "https://example.com/blog/42"         ==  Blog 42
-- toRoute "https://example.com/blog/42/"        ==  Blog 42
-- toRoute "https://example.com/blog/42#wolf"    ==  Blog 42
-- toRoute "https://example.com/blog/42?q=wolf"  ==  Blog 42
```

Note the first line: `parse` needs a full `Url`, and a bare path string doesn't parse into
one. In an app you always have the real `Url` from `init`/`onUrlChange`, so this is only a
trap in tests.

## Round-tripping

Write the inverse alongside the parser and use it for every link — never hand-build path
strings in views:

```elm
toHref : Route -> String
toHref r =
    case r of
        Home -> "/"
        Search Nothing -> "/search"
        Search (Just q) -> "/search?q=" ++ Url.percentEncode q
        Item id -> "/item/" ++ id
        NotFound -> "/"
```

A `case` here means adding a route variant produces a compile error at the link builder,
not a dead link.

## What real paths demand of the server

`Browser.application` with real paths (rather than `#/` fragments) needs whatever serves
the app to fall back to `index.html` on any unmatched path, or a deep link 404s on refresh.

Check that both ends do it — the dev server *and* the deployment. They are usually
different programs, and a fragment-free URL scheme that works locally and 404s in
production is the standard way this bites.

The corollary: a new top-level route must not collide with a path the host owns. An API
endpoint and any bundled UI are the usual claimants. Serving the app beneath a path prefix
makes collisions unlikely, since everything the app routes is namespaced under it.
