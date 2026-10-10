module Route exposing (Access(..), BasePath, JobsParams, LoginParams, RegisterParams, Route(..), SearchParams, TorrentStatsParams, basePath, emptyJobs, emptySearch, emptyTorrentStats, fromUrl, guard, returnDestination, toHref)

{-| Routes are real paths, not fragments — see the README.

`toHref` is the inverse of the parser and is the only way links are built. Adding a
variant breaks it at compile time rather than producing a dead link.

-}

import Facet exposing (Filters)
import Identity
import JobOrder exposing (JobOrder)
import Magnes.Api.Enum.QueueJobStatus as QueueJobStatus exposing (QueueJobStatus)
import Sort exposing (Sort)
import StatsControls exposing (Controls, Timeframe(..))
import Url exposing (Url)
import Url.Builder as Builder
import Url.Parser as Parser exposing ((</>), (<?>), Parser, oneOf, s, top)
import Url.Parser.Query as Query


type Route
    = Search SearchParams
    | Torrent String
    | Login LoginParams
    | Register RegisterParams
    | UserOverview
    | APIKeys
    | AdminUsers
    | AdminRoles
    | AdminInvitations
    | Status
    | QueueJobs JobsParams
    | TorrentStats TorrentStatsParams
    | NotFound


type alias LoginParams =
    { returnUrl : Maybe String }


type alias RegisterParams =
    { code : Maybe String }


type Access
    = PendingIdentity
    | Allowed
    | RedirectTo Route
    | Refused String


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


{-| Everything the URL says about the queue's jobs, so a filtered page of them is a link
(ADR 0002). `page` counts from 1, as bitmagnet's does.
-}
type alias JobsParams =
    { queues : List String
    , statuses : List QueueJobStatus
    , order : JobOrder
    , page : Int
    }


emptyJobs : JobsParams
emptyJobs =
    { queues = [], statuses = [], order = JobOrder.default, page = 1 }


{-| Everything the URL says about the torrent timeline: how far back, how coarsely, how
often to look again, and which sources, so a view of the index's growth is a link (ADR
0002). No source is all of them.
-}
type alias TorrentStatsParams =
    { controls : Controls
    , sources : List String
    }


emptyTorrentStats : TorrentStatsParams
emptyTorrentStats =
    { controls = StatsControls.default, sources = [] }


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
        , Parser.map (Login << LoginParams) (s "login" <?> Query.string "returnUrl")
        , Parser.map (Register << RegisterParams) (s "register" <?> Query.string "code")
        , Parser.map UserOverview (s "account")
        , Parser.map APIKeys (s "account" </> s "api-keys")
        , Parser.map AdminUsers (s "admin" </> s "users")
        , Parser.map AdminRoles (s "admin" </> s "roles")
        , Parser.map AdminInvitations (s "admin" </> s "invitations")
        , Parser.map Status (s "status")
        , Parser.map jobsWith
            (s "queue"
                </> s "jobs"
                <?> Query.custom "queue" identity
                <?> Query.custom "status" identity
                <?> Query.string "order"
                <?> Query.string "direction"
                <?> Query.int "page"
            )
        , Parser.map torrentStatsWith
            (s "stats"
                </> s "torrents"
                <?> Query.string "timeframe"
                <?> Query.string "resolution"
                <?> Query.int "every"
                <?> Query.string "refresh"
                <?> Query.custom "source" identity
            )
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


{-| As a search's filters do, unrecognised values are dropped rather than failing the page,
so a link written against a later schema still opens a list.
-}
jobsWith : List String -> List String -> Maybe String -> Maybe String -> Maybe Int -> Route
jobsWith queues statuses order direction page =
    QueueJobs
        { queues = List.filterMap nonBlank queues
        , statuses = List.filterMap QueueJobStatus.fromString statuses
        , order = JobOrder.fromParams order direction
        , page = page |> Maybe.withDefault 1 |> max 1
        }


{-| As the jobs' filters do, drops what it does not recognise. The timeframe of everything is
the queue's alone: the torrent query is not bounded by what the index holds, and a link
to it is read as the default.
-}
torrentStatsWith : Maybe String -> Maybe String -> Maybe Int -> Maybe String -> List String -> Route
torrentStatsWith timeframe resolution every refresh sources =
    TorrentStats
        { controls =
            StatsControls.fromParams
                { defaults = StatsControls.default
                , timeframes = List.filter ((/=) AllTime) StatsControls.allTimeframes
                , timeframe = timeframe
                , resolution = resolution
                , every = every
                , refresh = refresh
                }
        , sources = List.filterMap nonBlank sources
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

                Login params ->
                    Builder.absolute [ "login" ]
                        (Maybe.map (Builder.string "returnUrl") params.returnUrl
                            |> Maybe.map List.singleton
                            |> Maybe.withDefault []
                        )

                Register params ->
                    Builder.absolute [ "register" ]
                        (Maybe.map (Builder.string "code") params.code
                            |> Maybe.map List.singleton
                            |> Maybe.withDefault []
                        )

                UserOverview ->
                    Builder.absolute [ "account" ] []

                APIKeys ->
                    Builder.absolute [ "account", "api-keys" ] []

                AdminUsers ->
                    Builder.absolute [ "admin", "users" ] []

                AdminRoles ->
                    Builder.absolute [ "admin", "roles" ] []

                AdminInvitations ->
                    Builder.absolute [ "admin", "invitations" ] []

                Status ->
                    Builder.absolute [ "status" ] []

                QueueJobs params ->
                    Builder.absolute [ "queue", "jobs" ]
                        (List.map (Builder.string "queue") params.queues
                            ++ List.map (Builder.string "status" << QueueJobStatus.toString) params.statuses
                            ++ JobOrder.toParams params.order
                            ++ (if params.page == 1 then
                                    []

                                else
                                    [ Builder.int "page" params.page ]
                               )
                        )

                TorrentStats params ->
                    Builder.absolute [ "stats", "torrents" ]
                        (StatsControls.toParams StatsControls.default params.controls
                            ++ List.map (Builder.string "source") params.sources
                        )

                NotFound ->
                    Builder.absolute [] []
           )


{-| Where login should land after it succeeds.

The stored `returnUrl` came off the address bar, so it is attacker-supplied: a crafted
`/login?returnUrl=https://evil.test` would otherwise turn Magnes' own login into an
off-site redirect, which is exactly the shape a credential-phishing page wants. So this
never navigates to the string. It re-parses it through the same parser the address bar
goes through and returns a `Route`, which cannot name another origin at all.

Anything that does not resolve to a real destination within the mount falls back to the
default one. Login and registration are excluded because returning to them would bounce a
User who has just signed in straight back to the form.

-}
returnDestination : BasePath -> Route -> Route
returnDestination mount from =
    storedReturnUrl from
        |> Maybe.andThen internalPath
        |> Maybe.map (fromUrl mount)
        |> Maybe.andThen destination
        |> Maybe.withDefault (Search emptySearch)


storedReturnUrl : Route -> Maybe String
storedReturnUrl route =
    case route of
        Login params ->
            params.returnUrl

        _ ->
            Nothing


{-| A single leading slash, and nothing that could start an authority. `//evil.test` is a
protocol-relative URL, and browsers have historically treated a backslash as a slash in
that position, so both are refused rather than normalized.
-}
internalPath : String -> Maybe Url
internalPath raw =
    if String.startsWith "/" raw && not (List.any (\prefix -> String.startsWith prefix raw) [ "//", "/\\" ]) then
        let
            ( path, query ) =
                case String.split "?" raw of
                    before :: rest ->
                        ( before, String.join "?" rest |> nonBlank )

                    [] ->
                        ( raw, Nothing )
        in
        Just
            { protocol = Url.Https
            , host = ""
            , port_ = Nothing
            , path = path
            , query = query
            , fragment = Nothing
            }

    else
        Nothing


destination : Route -> Maybe Route
destination route =
    case route of
        Login _ ->
            Nothing

        Register _ ->
            Nothing

        NotFound ->
            Nothing

        _ ->
            Just route


guard : BasePath -> Identity.Identity -> Route -> Access
guard mount identity route =
    case identity of
        Identity.Unknown ->
            PendingIdentity

        Identity.Failed message ->
            Refused message

        Identity.Anonymous _ ->
            anonymousAccess mount identity route

        Identity.APIKeyAuthenticated _ _ _ ->
            anonymousAccess mount identity route

        Identity.UserAuthenticated _ _ ->
            userAccess identity route


anonymousAccess : BasePath -> Identity.Identity -> Route -> Access
anonymousAccess mount identity route =
    case route of
        Status ->
            requireHealth identity

        QueueJobs _ ->
            requireQueue identity

        TorrentStats _ ->
            requireTorrent identity

        UserOverview ->
            loginRedirect mount route

        APIKeys ->
            loginRedirect mount route

        AdminUsers ->
            loginRedirect mount route

        AdminRoles ->
            loginRedirect mount route

        AdminInvitations ->
            loginRedirect mount route

        _ ->
            Allowed


loginRedirect : BasePath -> Route -> Access
loginRedirect mount route =
    RedirectTo (Login { returnUrl = Just (toHref mount route) })


userAccess : Identity.Identity -> Route -> Access
userAccess identity route =
    case route of
        Login _ ->
            RedirectTo UserOverview

        Register _ ->
            RedirectTo UserOverview

        AdminUsers ->
            requireAdministration identity

        AdminRoles ->
            requireAdministration identity

        AdminInvitations ->
            requireAdministration identity

        Status ->
            requireHealth identity

        QueueJobs _ ->
            requireQueue identity

        TorrentStats _ ->
            requireTorrent identity

        _ ->
            Allowed


{-| Refused rather than sent to sign in, whoever is asking. `health::query` is in the
baseline of the `anon` and `user` Roles, so Anonymous always holds it and signing in would
not change the answer. An Identity without it is one whose Role was given less: `editor`
holds nothing until an administrator grants it.
-}
requireHealth : Identity.Identity -> Access
requireHealth =
    require "health" "Your Identity does not permit reading bitmagnet's health."


{-| Refused rather than sent to sign in, as `requireHealth` is. Signing in can grant it, but
only to a User whose Role holds it, and the core `user` Role does not; a sign-in offered
here would mostly lead to the same refusal.
-}
requireQueue : Identity.Identity -> Access
requireQueue =
    require "queue" "Your Identity does not permit reading bitmagnet's queue."


{-| Refused rather than sent to sign in, as the other guards here refuse, and the refusal
says what is missing. `torrent::query` is in the core `user` Role, so a User can open the page
unless their Role was given less; Anonymous can only if the `anon` Role has been granted it,
which it starts without (bitmagnet #87).
-}
requireTorrent : Identity.Identity -> Access
requireTorrent =
    require "torrent" "Your Identity does not permit reading bitmagnet's torrents."


requireAdministration : Identity.Identity -> Access
requireAdministration =
    require "auth" "Your Identity does not permit administration."


{-| Allowed with the object's `query` action, and otherwise refused with `refusal`.
-}
require : String -> String -> Identity.Identity -> Access
require object refusal identity =
    if Identity.can (Identity.graphql object "query") identity then
        Allowed

    else
        Refused refusal
