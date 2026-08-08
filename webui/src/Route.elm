module Route exposing (Route(..), SearchParams, emptySearch, fromUrl, toHref)

{-| Routes are real paths, not fragments — see the README.

`toHref` is the inverse of the parser and is the only way links are built. Adding a
variant breaks it at compile time rather than producing a dead link.

-}

import Sort exposing (Sort)
import Url exposing (Url)
import Url.Builder as Builder
import Url.Parser as Parser exposing ((</>), (<?>), Parser, oneOf, s, top)
import Url.Parser.Query as Query


type Route
    = Search SearchParams
    | Torrent String
    | NotFound


{-| Everything the URL says about a search. The model derives from this, never the
reverse, so a search is always a link — the ordering included.
-}
type alias SearchParams =
    { q : Maybe String
    , sort : Sort
    }


emptySearch : SearchParams
emptySearch =
    { q = Nothing, sort = Sort.default }


parser : Parser (Route -> a) a
parser =
    oneOf
        [ Parser.map (searchWith Nothing Nothing) top
        , Parser.map searchWith (s "search" <?> Query.string "q" <?> Query.string "sort")
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


searchWith : Maybe String -> Maybe String -> Route
searchWith q sort =
    Search
        { q = q |> Maybe.andThen nonBlank
        , sort = sort |> Maybe.map Sort.fromParam |> Maybe.withDefault Sort.default
        }


nonBlank : String -> Maybe String
nonBlank raw =
    case String.trim raw of
        "" ->
            Nothing

        trimmed ->
            Just trimmed


fromUrl : Url -> Route
fromUrl url =
    Parser.parse parser url |> Maybe.withDefault NotFound


toHref : Route -> String
toHref route =
    case route of
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
                )

        Torrent hash ->
            Builder.absolute [ "torrent", hash ] []

        NotFound ->
            Builder.absolute [] []
