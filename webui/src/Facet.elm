module Facet exposing
    ( ContentFilter(..)
    , Filters
    , contentLabel
    , contentParam
    , count
    , empty
    , fileLabel
    , fileParam
    , fileTypes
    , fromQuery
    , isEmpty
    , toQueryParams
    , toggleContent
    , toggleFile
    )

{-| The filters a search can carry, as one value shared by the URL, the query and the
chips — the same arrangement as [`Sort`](Sort).

Two facets are drawn, out of the nine bitmagnet offers. Content type is the obvious axis,
and file type is the one that still works for the ~88% of rows a DHT index never manages
to classify. The rest — genre, tag, language, release year, video resolution and source —
are real filters that simply have almost no data behind them here: on a 4.7M-row instance
a "ubuntu" search aggregated 4 language buckets totalling 4 rows. They are worth adding to
this module when an index exists that can fill them.

-}

import Magnes.Api.Enum.ContentType as ContentType exposing (ContentType)
import Magnes.Api.Enum.FileType as FileType exposing (FileType)
import Url.Builder as Builder


{-| `Unclassified` is a real, filterable value rather than the absence of one: bitmagnet
accepts a null in the content-type filter and answers with exactly the rows it never
classified — 4,210 of 4,703 on a live "ubuntu" search.
-}
type ContentFilter
    = Known ContentType
    | Unclassified


type alias Filters =
    { content : List ContentFilter
    , files : List FileType
    }


empty : Filters
empty =
    { content = [], files = [] }


isEmpty : Filters -> Bool
isEmpty filters =
    List.isEmpty filters.content && List.isEmpty filters.files


count : Filters -> Int
count filters =
    List.length filters.content + List.length filters.files


{-| Every file type, always. Unlike content type these are not read off an aggregation:
the set is fixed and small, and this instance's `torrentFileType` counts are wrong anyway
(every bucket came back ~1918 on a query whose total was 673). Drawing the enum costs the
server nothing and promises nothing false.
-}
fileTypes : List FileType
fileTypes =
    FileType.list



-- URL


contentParam : String
contentParam =
    "contentType"


fileParam : String
fileParam =
    "fileType"


{-| Repeated keys rather than a delimited list — `?fileType=video&fileType=audio` — so
nothing has to be escaped and `Url.Parser.Query.custom` reads it directly. Unrecognised
values are dropped, so a link written against a later schema still opens a search.
-}
fromQuery : List String -> List String -> Filters
fromQuery contentValues fileValues =
    { content = List.filterMap contentFromKey contentValues
    , files = List.filterMap FileType.fromString fileValues
    }


toQueryParams : Filters -> List Builder.QueryParameter
toQueryParams filters =
    List.map (Builder.string contentParam << contentKey) filters.content
        ++ List.map (Builder.string fileParam << FileType.toString) filters.files


contentKey : ContentFilter -> String
contentKey filter =
    case filter of
        Known contentType ->
            ContentType.toString contentType

        Unclassified ->
            "unknown"


contentFromKey : String -> Maybe ContentFilter
contentFromKey raw =
    if raw == "unknown" then
        Just Unclassified

    else
        ContentType.fromString raw |> Maybe.map Known



-- LABELS


contentLabel : ContentFilter -> String
contentLabel filter =
    case filter of
        Known contentType ->
            String.replace "_" " " (ContentType.toString contentType)

        Unclassified ->
            "unclassified"


fileLabel : FileType -> String
fileLabel =
    FileType.toString >> String.replace "_" " "



-- TOGGLING


toggleContent : ContentFilter -> Filters -> Filters
toggleContent value filters =
    { filters | content = toggle value filters.content }


toggleFile : FileType -> Filters -> Filters
toggleFile value filters =
    { filters | files = toggle value filters.files }


toggle : a -> List a -> List a
toggle value values =
    if List.member value values then
        List.filter ((/=) value) values

    else
        values ++ [ value ]
