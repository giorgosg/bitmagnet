module Bitmagnet exposing (File, FileList, Page, Row, SearchArgs, byInfoHash, errorToString, fileLimit, files, search)

{-| Every call Magnes makes to bitmagnet. Read-only by construction: the generated
`Magnes.Api.Mutation` is never imported here or anywhere else.
-}

import Graphql.Http
import Graphql.Operation exposing (RootQuery)
import Graphql.OptionalArgument as Opt exposing (OptionalArgument(..))
import Graphql.SelectionSet as SelectionSet exposing (SelectionSet)
import Magnes.Api.Enum.ContentType exposing (ContentType)
import Magnes.Api.Enum.FilesStatus exposing (FilesStatus)
import Magnes.Api.InputObject as InputObject
import Magnes.Api.Object
import Magnes.Api.Object.Torrent as Torrent
import Magnes.Api.Object.TorrentContent as TorrentContent
import Magnes.Api.Object.TorrentContentQuery as TorrentContentQuery
import Magnes.Api.Object.TorrentContentSearchResult as SearchResult
import Magnes.Api.Object.TorrentFile as TorrentFile
import Magnes.Api.Object.TorrentFilesQueryResult as FilesResult
import Magnes.Api.Object.TorrentQuery as TorrentQuery
import Magnes.Api.Query as Query
import Magnes.Api.Scalar as Scalar
import Time


{-| One result line.

Keyed on `id`, not `infoHash`: `id` is a composite of the info hash and the content match,
so one torrent matched to two content entries is two rows sharing a hash. `id` is the row
identity, and offset paging over a live crawler re-serves rows, so appends dedupe on it.

-}
type alias Row =
    { id : String
    , infoHash : String
    , title : String
    , name : String
    , size : Int
    , magnetUri : String
    , filesStatus : FilesStatus
    , filesCount : Maybe Int
    , contentType : Maybe ContentType
    , seeders : Maybe Int
    , leechers : Maybe Int
    , publishedAt : Time.Posix
    }


type alias File =
    { index : Int
    , path : String
    , size : Int
    }


{-| A fetched file list after padding is dropped, plus what the dropping and the `limit`
cost — both are things the list has to be able to admit to.
-}
type alias FileList =
    { files : List File
    , hidden : Int
    , capped : Bool
    }


type alias Page =
    { totalCount : Int
    , totalCountIsEstimate : Bool
    , hasNextPage : Bool
    , items : List Row
    }


type alias SearchArgs =
    { queryString : Maybe String
    , infoHashes : List String
    , limit : Int
    , offset : Int
    }


{-| There is no get-torrent-by-hash query in the schema, so the `/torrent/<hash>` route is
a search filtered to one hash.
-}
byInfoHash : String -> SearchArgs
byInfoHash infoHash =
    { queryString = Nothing, infoHashes = [ infoHash ], limit = 1, offset = 0 }


search : SearchArgs -> SelectionSet Page RootQuery
search args =
    Query.torrentContent
        (TorrentContentQuery.search { input = searchInput args } pageSelection)


{-| `totalCount` and `hasNextPage` are opt-in request flags, and omitting them fails
silently: the response comes back with `0` and `false` rather than null, so the list
reports "no more results" and stops dead after the first batch. Always send both.
-}
searchInput : SearchArgs -> InputObject.TorrentContentSearchQueryInput
searchInput args =
    InputObject.buildTorrentContentSearchQueryInput
        (\opts ->
            { opts
                | queryString = Opt.fromMaybe args.queryString
                , infoHashes =
                    case args.infoHashes of
                        [] ->
                            Absent

                        hashes ->
                            Present (List.map Scalar.Hash20 hashes)
                , limit = Present args.limit
                , offset = Present args.offset
                , totalCount = Present True
                , hasNextPage = Present True
            }
        )


pageSelection : SelectionSet Page Magnes.Api.Object.TorrentContentSearchResult
pageSelection =
    SelectionSet.map4 Page
        SearchResult.totalCount
        SearchResult.totalCountIsEstimate
        (SearchResult.hasNextPage |> SelectionSet.map (Maybe.withDefault False))
        (SearchResult.items rowSelection)


rowSelection : SelectionSet Row Magnes.Api.Object.TorrentContent
rowSelection =
    SelectionSet.map7
        (\id infoHash title contentType publishedAt content torrent ->
            { id = id
            , infoHash = infoHash
            , title = title
            , name = torrent.name
            , size = torrent.size
            , magnetUri = torrent.magnetUri
            , filesStatus = torrent.filesStatus
            , filesCount = torrent.filesCount
            , contentType = contentType
            , seeders = content.seeders
            , leechers = content.leechers
            , publishedAt = publishedAt
            }
        )
        (TorrentContent.id |> SelectionSet.map (\(Scalar.Id raw) -> raw))
        (TorrentContent.infoHash |> SelectionSet.map (\(Scalar.Hash20 raw) -> raw))
        TorrentContent.title
        TorrentContent.contentType
        TorrentContent.publishedAt
        (SelectionSet.map2 (\seeders leechers -> { seeders = seeders, leechers = leechers })
            TorrentContent.seeders
            TorrentContent.leechers
        )
        (TorrentContent.torrent torrentSelection)


type alias TorrentFields =
    { name : String
    , size : Int
    , magnetUri : String
    , filesStatus : FilesStatus
    , filesCount : Maybe Int
    }


{-| The torrent is selected once and destructured, rather than selecting `torrent` once
per field — one field in the query, one place to add to.
-}
torrentSelection : SelectionSet TorrentFields Magnes.Api.Object.Torrent
torrentSelection =
    SelectionSet.map5 TorrentFields
        Torrent.name
        Torrent.size
        Torrent.magnetUri
        Torrent.filesStatus
        Torrent.filesCount



-- FILES


{-| A torrent can contain thousands of files, and the query is paginated, so the cap on an
expanded list is a `limit` rather than something trimmed client-side.
-}
fileLimit : Int
fileLimit =
    100


{-| Only worth calling when `filesStatus` is `multi`. `no_info` and `over_threshold` both
return an empty list — the second one despite reporting a `filesCount`.
-}
files : String -> SelectionSet FileList RootQuery
files infoHash =
    Query.torrent
        (TorrentQuery.files
            { input =
                InputObject.buildTorrentFilesQueryInput
                    (\opts ->
                        { opts
                            | infoHashes = Present [ Scalar.Hash20 infoHash ]
                            , limit = Present fileLimit
                        }
                    )
            }
            (FilesResult.items fileSelection |> SelectionSet.map withoutPadding)
        )


withoutPadding : List File -> FileList
withoutPadding fetched =
    let
        kept =
            List.filter (not << isPadding) fetched
    in
    { files = kept
    , hidden = List.length fetched - List.length kept
    , capped = List.length fetched >= fileLimit
    }


{-| Padding files are alignment filler, not content — a torrent's real file list is what
someone is looking at, and interleaving a dozen `.pad/1870630` entries hides it.

Two conventions exist. BEP-47 (libtorrent, qBittorrent) puts them in a `.pad` directory
named for their own byte length, which is the form this index actually contains — a sweep
of 120 multi-file torrents turned up 24 distinct paths, all of that shape. The older
µTorrent convention is a `_____padding_file…` name at any depth, so both are matched.

-}
isPadding : File -> Bool
isPadding file =
    let
        segments =
            String.split "/" file.path

        basename =
            List.reverse segments |> List.head |> Maybe.withDefault ""

        directories =
            List.take (List.length segments - 1) segments
    in
    List.member ".pad" directories
        || (String.startsWith "_" basename && String.contains "padding_file" basename)


fileSelection : SelectionSet File Magnes.Api.Object.TorrentFile
fileSelection =
    SelectionSet.map3 File
        TorrentFile.index
        TorrentFile.path
        TorrentFile.size


errorToString : Graphql.Http.Error a -> String
errorToString error =
    case error of
        Graphql.Http.GraphqlError _ errors ->
            case errors of
                first :: _ ->
                    first.message

                [] ->
                    "The server rejected the query."

        Graphql.Http.HttpError httpError ->
            case httpError of
                Graphql.Http.NetworkError ->
                    "Could not reach bitmagnet."

                Graphql.Http.Timeout ->
                    "bitmagnet took too long to answer."

                Graphql.Http.BadStatus metadata _ ->
                    "bitmagnet answered with status " ++ String.fromInt metadata.statusCode ++ "."

                Graphql.Http.BadUrl url ->
                    "Not a usable bitmagnet URL: " ++ url

                Graphql.Http.BadPayload _ ->
                    "bitmagnet's answer did not match the schema Magnes was built against."
