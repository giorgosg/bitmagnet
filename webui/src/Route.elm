module Route exposing (BasePath, Route(..), SearchParams, basePath, emptySearch, fromUrl, toHref)

{-| Routes are real paths, not fragments — see the README.

`toHref` is the inverse of the parser and is the only way links are built. Adding a
variant breaks it at compile time rather than producing a dead link.

-}

import Facet exposing (Filters)
import Sort exposing (Sort)
import Url exposing (Url)
import Url.Builder as Builder
import Url.Parser as Parser exposing ((</>), (<?>), Parser, oneOf, s, top)
import Url.Parser.Query as Query


type Route
    = Search SearchParams
    | Torrent String
    | NotFound


{-| The URL prefix where Magnes is mounted. An empty value means the origin root.
Normalising it once keeps parsing and link building exact inverses.
-}
type BasePath
    = BasePath String


basePath : String -> BasePath
basePath raw =
    raw
        |> String.split "/"
        |> List.filter (not << String.isEmpty)
        |> String.join "/"
        |> (\path ->
                if String.isEmpty path then
                    BasePath ""

                else
                    BasePath ("/" ++ path)
           )


{-| Everything the URL says about a search. The model derives from this, never the
reverse, so a search is always a link — the ordering included.
-}
type alias SearchParams =
    { q : Maybe String
    , sort : Sort
    , filters : Filters
    }


emptySearch : SearchParams
emptySearch =
    { q = Nothing, sort = Sort.default, filters = Facet.empty }


parser : Parser (Route -> a) a
parser =
    oneOf
        [ Parser.map (searchWith Nothing Nothing [] []) top
        , Parser.map searchWith
            (s "search"
                <?> Query.string "q"
                <?> Query.string "sort"
                <?> Query.custom Facet.contentParam identity
                <?> Query.custom Facet.fileParam identity
            )
        , Parser.map Torrent (s "torrent" </> infoHash)
        ]


{-| Only a real info hash matches, so `/torrent/abc` falls through to `NotFound` instead
of being sent to bitmagnet, which answers a malformed hash with a raw decoding error.
Matching lowercases it too, so a link and a lookup always agree.
-}
infoHash : Parser (String -> a) a
infoHash =
    Parser.custom "INFO_HASH" <|
        \segment ->
            let
                lowered =
                    String.toLower segment
            in
            if String.length lowered == 40 && String.all Char.isHexDigit lowered then
                Just lowered

            else
                Nothing


searchWith : Maybe String -> Maybe String -> List String -> List String -> Route
searchWith q sort contentValues fileValues =
    Search
        { q = q |> Maybe.andThen nonBlank
        , sort = sort |> Maybe.map Sort.fromParam |> Maybe.withDefault Sort.default
        , filters = Facet.fromQuery contentValues fileValues
        }


nonBlank : String -> Maybe String
nonBlank raw =
    case String.trim raw of
        "" ->
            Nothing

        trimmed ->
            Just trimmed


fromUrl : BasePath -> Url -> Route
fromUrl (BasePath prefix) url =
    pathWithin prefix url.path
        |> Maybe.andThen
            (\path ->
                Parser.parse parser
                    { url
                        | path =
                            if String.isEmpty path then
                                "/"

                            else
                                path
                    }
            )
        |> Maybe.withDefault NotFound


pathWithin : String -> String -> Maybe String
pathWithin prefix path =
    if String.isEmpty prefix then
        Just path

    else if path == prefix then
        Just "/"

    else if String.startsWith (prefix ++ "/") path then
        Just (String.dropLeft (String.length prefix) path)

    else
        Nothing


toHref : BasePath -> Route -> String
toHref (BasePath prefix) route =
    prefix
        ++ (case route of
                Search params ->
                    -- The default sort is left out, so an ordinary search is still a bare ?q=.
                    Builder.absolute [ "search" ]
                        (List.filterMap identity
                            [ Maybe.map (Builder.string "q") params.q
                            , if params.sort == Sort.default then
                                Nothing

                              else
                                Just (Builder.string "sort" (Sort.toParam params.sort))
                            ]
                            ++ Facet.toQueryParams params.filters
                        )

                Torrent hash ->
                    Builder.absolute [ "torrent", hash ] []

                NotFound ->
                    Builder.absolute [] []
           )
