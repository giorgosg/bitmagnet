module RouteTest exposing (suite)

import Expect
import Identity
import Magnes.Api.Enum.MetricsBucketDuration exposing (MetricsBucketDuration(..))
import Magnes.Api.Enum.QueueJobStatus exposing (QueueJobStatus(..))
import Magnes.Api.Enum.QueueJobsOrderByField exposing (QueueJobsOrderByField(..))
import QueueMetrics
import Route
import StatsControls exposing (AutoRefresh(..), Timeframe(..))
import Test exposing (Test, describe, test)
import Time
import Url


suite : Test
suite =
    describe "Identity routes"
        [ describe "round trips beneath the configured base path"
            (List.map roundTripTest routes)
        , test "an Anonymous Identity is sent to login with the protected URL" <|
            \_ ->
                Route.guard mount (Identity.Anonymous []) Route.APIKeys
                    |> Expect.equal
                        (Route.RedirectTo
                            (Route.Login { returnUrl = Just "/magnes/account/api-keys" })
                        )
        , describe "the User overview, where a password is changed"
            [ test "is a User-authenticated Identity's own" <|
                \_ ->
                    Route.guard mount userIdentity Route.UserOverview
                        |> Expect.equal Route.Allowed
            , test "sends an API-key Identity to sign in, as it does Anonymous" <|
                \_ ->
                    -- An API-key Identity reports its owning User, but bitmagnet refuses it a
                    -- password change, as it refuses it key management: the page and its form
                    -- are for the person, signed in as themselves.
                    ( Route.guard mount (Identity.APIKeyAuthenticated user "a key" [ adminAction ]) Route.UserOverview
                    , Route.guard mount (Identity.Anonymous [ adminAction ]) Route.UserOverview
                    )
                        |> Expect.equal
                            ( Route.RedirectTo (Route.Login { returnUrl = Just "/magnes/account" })
                            , Route.RedirectTo (Route.Login { returnUrl = Just "/magnes/account" })
                            )
            ]
        , describe "signing in again after a password change"
            [ test "is asked for on the login form, returning to the User overview" <|
                \_ ->
                    Route.toHref mount (Route.loginReturningTo mount Route.UserOverview)
                        |> Expect.equal "/magnes/login?returnUrl=%2Fmagnes%2Faccount"
            , test "returns to the User overview once it succeeds" <|
                \_ ->
                    Route.returnDestination mount (Route.loginReturningTo mount Route.UserOverview)
                        |> Expect.equal Route.UserOverview
            , test "is the same request the guard makes of a route that needs a User" <|
                \_ ->
                    Route.guard mount (Identity.Anonymous []) Route.UserOverview
                        |> Expect.equal (Route.RedirectTo (Route.loginReturningTo mount Route.UserOverview))
            ]
        , test "a User-authenticated Identity is sent away from login" <|
            \_ ->
                Route.guard mount userIdentity (Route.Login { returnUrl = Nothing })
                    |> Expect.equal (Route.RedirectTo Route.UserOverview)
        , test "administration refuses a User without auth query permission" <|
            \_ ->
                Route.guard mount userIdentity Route.AdminUsers
                    |> Expect.equal (Route.Refused "Your Identity does not permit administration.")
        , test "administration allows the admin wildcard" <|
            \_ ->
                Route.guard mount adminIdentity Route.AdminRoles
                    |> Expect.equal Route.Allowed
        , describe "status"
            [ test "is open to an Anonymous Identity holding health::query" <|
                \_ ->
                    Route.guard mount (Identity.Anonymous [ Identity.graphql "health" "query" ]) Route.Status
                        |> Expect.equal Route.Allowed
            , test "is open to a User holding health::query" <|
                \_ ->
                    Route.guard mount (Identity.UserAuthenticated user [ Identity.graphql "health" "query" ]) Route.Status
                        |> Expect.equal Route.Allowed
            , test "is refused, not redirected, to an Identity without health::query" <|
                \_ ->
                    ( Route.guard mount (Identity.Anonymous []) Route.Status
                    , Route.guard mount userIdentity Route.Status
                    )
                        |> Expect.equal
                            ( Route.Refused "Your Identity does not permit reading bitmagnet's health."
                            , Route.Refused "Your Identity does not permit reading bitmagnet's health."
                            )
            ]
        , describe "queue jobs"
            [ test "an unfiltered first page is a bare path" <|
                \_ ->
                    Route.toHref mount (Route.QueueJobs Route.emptyJobs)
                        |> Expect.equal "/magnes/queue/jobs"
            , test "carries its filters, ordering and page in the query string" <|
                \_ ->
                    Route.toHref mount (Route.QueueJobs filteredJobs)
                        |> Expect.equal "/magnes/queue/jobs?queue=process_torrent&status=failed&status=retry&order=priority&direction=desc&page=3"
            , test "leaves out a direction that is the ordering's own" <|
                \_ ->
                    Route.toHref mount (Route.QueueJobs { emptyJobs | order = { field = Ran_at, descending = True } })
                        |> Expect.equal "/magnes/queue/jobs?order=ran_at"
            , test "unknown, legacy and repeated torrent events use both events" <|
                \_ ->
                    [ "event=created", "event=all", "event=unknown", "event=new&event=updated" ]
                        |> List.map (\query -> Url.fromString ("https://example.test/magnes/stats/torrents?" ++ query) |> Maybe.map (Route.fromUrl mount))
                        |> Expect.equal (List.repeat 4 (Just (Route.TorrentStats emptyStats)))
            , test "drops what it does not recognise rather than failing the page" <|
                \_ ->
                    "https://example.test/magnes/queue/jobs?status=bogus&status=failed&queue=&order=size&direction=sideways&page=0"
                        |> Url.fromString
                        |> Maybe.map (Route.fromUrl mount)
                        |> Expect.equal (Just (Route.QueueJobs { emptyJobs | statuses = [ Failed ] }))
            , test "is refused, not redirected, to an Identity without queue::query" <|
                \_ ->
                    ( Route.guard mount (Identity.Anonymous [ Identity.graphql "health" "query" ]) (Route.QueueJobs emptyJobs)
                    , Route.guard mount userIdentity (Route.QueueJobs emptyJobs)
                    )
                        |> Expect.equal
                            ( Route.Refused "Your Identity does not permit reading bitmagnet's queue."
                            , Route.Refused "Your Identity does not permit reading bitmagnet's queue."
                            )
            , test "is open to an Anonymous Identity or a User holding queue::query" <|
                \_ ->
                    ( Route.guard mount (Identity.Anonymous [ Identity.graphql "queue" "query" ]) (Route.QueueJobs emptyJobs)
                    , Route.guard mount adminIdentity (Route.QueueJobs filteredJobs)
                    )
                        |> Expect.equal ( Route.Allowed, Route.Allowed )
            ]
        , describe "queue administration"
            [ test "is a bare path" <|
                \_ ->
                    Route.toHref mount Route.AdminQueue
                        |> Expect.equal "/magnes/admin/queue"
            , test "is refused, not redirected, to an Identity without queue::mutate" <|
                \_ ->
                    -- Reading the queue is not enough to change it, and signing in would not
                    -- help an Anonymous Identity whose instance grants it: the core `user` Role
                    -- does not hold queue::mutate either.
                    ( Route.guard mount (Identity.Anonymous [ Identity.graphql "queue" "query" ]) Route.AdminQueue
                    , Route.guard mount (Identity.UserAuthenticated user [ Identity.graphql "queue" "query" ]) Route.AdminQueue
                    )
                        |> Expect.equal
                            ( Route.Refused "Your Identity does not permit changing bitmagnet's queue."
                            , Route.Refused "Your Identity does not permit changing bitmagnet's queue."
                            )
            , test "is refused to an Identity that may change the queue but not read it" <|
                \_ ->
                    -- A purge's confirmation says how many jobs it would delete, which only
                    -- reading the queue can count.
                    Route.guard mount (Identity.UserAuthenticated user [ Identity.graphql "queue" "mutate" ]) Route.AdminQueue
                        |> Expect.equal (Route.Refused "Your Identity does not permit reading bitmagnet's queue, which changing it here needs.")
            , test "is open to any Identity holding queue::query and queue::mutate" <|
                \_ ->
                    ( Route.guard mount (Identity.Anonymous [ Identity.graphql "queue" "query", Identity.graphql "queue" "mutate" ]) Route.AdminQueue
                    , Route.guard mount adminIdentity Route.AdminQueue
                    )
                        |> Expect.equal ( Route.Allowed, Route.Allowed )
            ]
        , describe "torrent statistics"
            [ test "round-trips a chosen torrent event and leaves both events out of the URL" <|
                \_ ->
                    [ Route.AllTorrentEvents, Route.NewTorrents, Route.UpdatedTorrents ]
                        |> List.map
                            (\event ->
                                let
                                    chosen =
                                        Route.TorrentStats { emptyStats | event = event }

                                    href =
                                        Route.toHref mount chosen
                                in
                                ( href, Url.fromString ("https://example.test" ++ href) |> Maybe.map (Route.fromUrl mount) )
                            )
                        |> Expect.equal
                            [ ( "/magnes/stats/torrents", Just (Route.TorrentStats emptyStats) )
                            , ( "/magnes/stats/torrents?event=new", Just (Route.TorrentStats { emptyStats | event = Route.NewTorrents }) )
                            , ( "/magnes/stats/torrents?event=updated", Just (Route.TorrentStats { emptyStats | event = Route.UpdatedTorrents }) )
                            ]
            , test "the defaults are a bare path" <|
                \_ ->
                    Route.toHref mount (Route.TorrentStats Route.emptyTorrentStats)
                        |> Expect.equal "/magnes/stats/torrents"
            , test "carries its controls and sources in the query string" <|
                \_ ->
                    Route.toHref mount (Route.TorrentStats chosenStats)
                        |> Expect.equal "/magnes/stats/torrents?timeframe=6h&resolution=hour&every=2&refresh=30s&source=dht&source=rarbg"
            , test "reads the query string back into the same choices" <|
                \_ ->
                    "https://example.test/magnes/stats/torrents?refresh=30s&source=dht&every=2&source=rarbg&resolution=hour&timeframe=6h"
                        |> Url.fromString
                        |> Maybe.map (Route.fromUrl mount)
                        |> Expect.equal (Just (Route.TorrentStats chosenStats))
            , test "drops what it does not recognise rather than failing the page" <|
                \_ ->
                    "https://example.test/magnes/stats/torrents?timeframe=fortnight&resolution=week&every=0&refresh=2s&source=&source=%20"
                        |> Url.fromString
                        |> Maybe.map (Route.fromUrl mount)
                        |> Expect.equal (Just (Route.TorrentStats Route.emptyTorrentStats))
            , test "reads a source named twice as named once" <|
                \_ ->
                    "https://example.test/magnes/stats/torrents?source=dht&source=rarbg&source=dht"
                        |> Url.fromString
                        |> Maybe.map (Route.fromUrl mount)
                        |> Expect.equal (Just (Route.TorrentStats { emptyStats | sources = [ "dht", "rarbg" ] }))
            , test "has no timeframe of everything, which only the queue's statistics offer" <|
                \_ ->
                    "https://example.test/magnes/stats/torrents?timeframe=all"
                        |> Url.fromString
                        |> Maybe.map (Route.fromUrl mount)
                        |> Expect.equal (Just (Route.TorrentStats Route.emptyTorrentStats))
            , test "is refused, not redirected, to an Identity without torrent::query" <|
                \_ ->
                    ( Route.guard mount (Identity.Anonymous [ Identity.graphql "health" "query", Identity.graphql "queue" "query" ]) (Route.TorrentStats Route.emptyTorrentStats)
                    , Route.guard mount (Identity.UserAuthenticated user [ Identity.graphql "health" "query" ]) (Route.TorrentStats chosenStats)
                    )
                        |> Expect.equal
                            ( Route.Refused "Your Identity does not permit reading bitmagnet's torrents."
                            , Route.Refused "Your Identity does not permit reading bitmagnet's torrents."
                            )
            , test "is open to an Anonymous Identity or a User holding torrent::query" <|
                \_ ->
                    ( Route.guard mount (Identity.Anonymous [ Identity.graphql "torrent" "query" ]) (Route.TorrentStats Route.emptyTorrentStats)
                    , Route.guard mount (Identity.UserAuthenticated user [ Identity.graphql "torrent" "query" ]) (Route.TorrentStats chosenStats)
                    )
                        |> Expect.equal ( Route.Allowed, Route.Allowed )
            ]
        , describe "queue statistics"
            [ test "the defaults, everything by the hour, are a bare path" <|
                \_ ->
                    Route.toHref mount (Route.QueueStats Route.emptyQueueStats)
                        |> Expect.equal "/magnes/stats/queue"
            , test "carries its controls, queues and events in the query string" <|
                \_ ->
                    Route.toHref mount (Route.QueueStats chosenQueueStats)
                        |> Expect.equal "/magnes/stats/queue?timeframe=1d&resolution=minute&every=5&refresh=10s&queue=process_torrent_batch&event=failed&event=created"
            , test "reads the query string back into the same choices" <|
                \_ ->
                    "https://example.test/magnes/stats/queue?event=failed&queue=process_torrent_batch&every=5&event=created&refresh=10s&resolution=minute&timeframe=1d"
                        |> Url.fromString
                        |> Maybe.map (Route.fromUrl mount)
                        |> Expect.equal (Just (Route.QueueStats chosenQueueStats))
            , test "writes a timeframe other than everything, and the torrent page's defaults, which are not its own" <|
                \_ ->
                    Route.toHref mount (Route.QueueStats { emptyQueueStats | controls = StatsControls.default })
                        |> Expect.equal "/magnes/stats/queue?timeframe=1h&resolution=minute"
            , test "drops what it does not recognise rather than failing the page" <|
                \_ ->
                    "https://example.test/magnes/stats/queue?timeframe=fortnight&resolution=week&every=0&refresh=2s&queue=&queue=%20&event=retry&event=updated"
                        |> Url.fromString
                        |> Maybe.map (Route.fromUrl mount)
                        |> Expect.equal (Just (Route.QueueStats Route.emptyQueueStats))
            , test "reads a queue or an event named twice as named once, in the order first named" <|
                \_ ->
                    "https://example.test/magnes/stats/queue?queue=b&queue=a&queue=b&event=failed&event=created&event=failed"
                        |> Url.fromString
                        |> Maybe.map (Route.fromUrl mount)
                        |> Expect.equal (Just (Route.QueueStats { emptyQueueStats | queues = [ "b", "a" ], events = [ QueueMetrics.Failed, QueueMetrics.Created ] }))
            , test "is refused, not redirected, to an Identity without queue::query" <|
                \_ ->
                    ( Route.guard mount (Identity.Anonymous [ Identity.graphql "health" "query", Identity.graphql "torrent" "query" ]) (Route.QueueStats Route.emptyQueueStats)
                    , Route.guard mount userIdentity (Route.QueueStats chosenQueueStats)
                    )
                        |> Expect.equal
                            ( Route.Refused "Your Identity does not permit reading bitmagnet's queue."
                            , Route.Refused "Your Identity does not permit reading bitmagnet's queue."
                            )
            , test "is open to an Anonymous Identity or a User holding queue::query" <|
                \_ ->
                    ( Route.guard mount (Identity.Anonymous [ Identity.graphql "queue" "query" ]) (Route.QueueStats Route.emptyQueueStats)
                    , Route.guard mount (Identity.UserAuthenticated user [ Identity.graphql "queue" "query" ]) (Route.QueueStats chosenQueueStats)
                    )
                        |> Expect.equal ( Route.Allowed, Route.Allowed )
            ]
        , describe "looking again by itself"
            [ test "a page that asks for it says how often, and every other page says it never does" <|
                \_ ->
                    List.map Route.refreshInterval
                        (Route.TorrentStats chosenStats
                            :: Route.TorrentStats Route.emptyTorrentStats
                            :: Route.QueueStats chosenQueueStats
                            :: Route.QueueStats Route.emptyQueueStats
                            :: List.filter (not << isStats) routes
                        )
                        |> Expect.equal (Just 30000 :: Nothing :: Just 10000 :: Nothing :: List.map (always Nothing) (List.filter (not << isStats) routes))
            ]
        , describe "sameQuestion"
            [ test "two looks at the torrents that differ only in how often to look again ask the same" <|
                \_ ->
                    let
                        slower =
                            { chosenStats | controls = withRefresh Every5Minutes chosenStats.controls }
                    in
                    ( Route.sameQuestion (Route.TorrentStats chosenStats) (Route.TorrentStats slower)
                    , Route.sameQuestion (Route.TorrentStats chosenStats) (Route.TorrentStats { chosenStats | sources = [] })
                    )
                        |> Expect.equal ( True, False )
            , test "torrent events are drawn from the same answer without another request" <|
                \_ ->
                    [ Route.NewTorrents, Route.UpdatedTorrents ]
                        |> List.map (\event -> Route.sameQuestion (Route.TorrentStats chosenStats) (Route.TorrentStats { chosenStats | event = event }))
                        |> Expect.equal [ True, True ]
            , test "two looks at the queue ask the same whatever they look again at, and whichever queues and events they pick out of the answer" <|
                \_ ->
                    ( Route.sameQuestion (Route.QueueStats chosenQueueStats)
                        (Route.QueueStats { chosenQueueStats | controls = withRefresh Off chosenQueueStats.controls, queues = [], events = [] })
                    , Route.sameQuestion (Route.QueueStats chosenQueueStats)
                        (Route.QueueStats { chosenQueueStats | controls = { timeframe = Hours1, resolution = chosenQueueStats.controls.resolution, refresh = Every10Seconds } })
                    )
                        |> Expect.equal ( True, False )
            , test "for every other page, is the same route" <|
                \_ ->
                    let
                        others =
                            List.filter (not << isStats) routes
                    in
                    List.concatMap (\one -> List.map (\other -> Route.sameQuestion one other == (one == other)) others) others
                        |> List.all identity
                        |> Expect.equal True
            , test "is never asked across two pages" <|
                \_ ->
                    Route.sameQuestion (Route.QueueStats Route.emptyQueueStats) (Route.TorrentStats Route.emptyTorrentStats)
                        |> Expect.equal False
            ]
        , test "Unknown waits and bootstrap failure remains a refusal" <|
            \_ ->
                ( Route.guard mount Identity.Unknown Route.UserOverview
                , Route.guard mount (Identity.Failed "offline") Route.UserOverview
                )
                    |> Expect.equal
                        ( Route.PendingIdentity, Route.Refused "offline" )
        , describe "returnDestination"
            [ test "a protected route is resolved back from its stored URL" <|
                \_ ->
                    Route.returnDestination mount (Route.Login { returnUrl = Just "/magnes/account/api-keys" })
                        |> Expect.equal Route.APIKeys
            , test "a search keeps its query" <|
                \_ ->
                    Route.returnDestination mount (Route.Login { returnUrl = Just "/magnes/search?q=dune" })
                        |> Expect.equal
                            (searchFor "dune")
            , test "a protocol-relative URL cannot send a User off-site" <|
                \_ ->
                    Route.returnDestination mount (Route.Login { returnUrl = Just "//evil.test/phish" })
                        |> Expect.equal home
            , test "an absolute URL cannot send a User off-site" <|
                \_ ->
                    Route.returnDestination mount (Route.Login { returnUrl = Just "https://evil.test/phish" })
                        |> Expect.equal home
            , test "a backslash cannot stand in for the second slash" <|
                \_ ->
                    Route.returnDestination mount (Route.Login { returnUrl = Just "/\\evil.test/phish" })
                        |> Expect.equal home
            , test "a path outside the mount is not a destination" <|
                \_ ->
                    Route.returnDestination mount (Route.Login { returnUrl = Just "/elsewhere/account" })
                        |> Expect.equal home
            , test "login does not return to itself" <|
                \_ ->
                    Route.returnDestination mount (Route.Login { returnUrl = Just "/magnes/login" })
                        |> Expect.equal home
            , test "registration is not a destination either" <|
                \_ ->
                    Route.returnDestination mount (Route.Login { returnUrl = Just "/magnes/register?code=x" })
                        |> Expect.equal home
            , test "no stored URL means the default destination" <|
                \_ ->
                    Route.returnDestination mount (Route.Login { returnUrl = Nothing })
                        |> Expect.equal home
            ]
        ]


home : Route.Route
home =
    Route.Search Route.emptySearch


searchFor : String -> Route.Route
searchFor term =
    let
        params =
            Route.emptySearch
    in
    Route.Search { params | q = Just term }


mount : Route.BasePath
mount =
    Route.basePath "/magnes"


routes : List Route.Route
routes =
    [ Route.Login { returnUrl = Just "/magnes/account/api-keys" }
    , Route.Register { code = Just "invitation code" }
    , Route.UserOverview
    , Route.APIKeys
    , Route.AdminUsers
    , Route.AdminRoles
    , Route.AdminInvitations
    , Route.AdminQueue
    , Route.Status
    , Route.QueueJobs Route.emptyJobs
    , Route.QueueJobs filteredJobs
    , Route.QueueJobs { emptyJobs | order = { field = Created_at, descending = False } }
    , Route.TorrentStats Route.emptyTorrentStats
    , Route.TorrentStats chosenStats
    , Route.TorrentStats { emptyStats | controls = { timeframe = Hours1, resolution = { unit = Minute, every = Just 1 }, refresh = Off } }
    , Route.QueueStats Route.emptyQueueStats
    , Route.QueueStats chosenQueueStats
    , Route.QueueStats { emptyQueueStats | controls = StatsControls.default, events = [ QueueMetrics.Processed ] }
    ]


emptyStats : Route.TorrentStatsParams
emptyStats =
    Route.emptyTorrentStats


chosenStats : Route.TorrentStatsParams
chosenStats =
    { controls =
        { timeframe = Hours6
        , resolution = { unit = Hour, every = Just 2 }
        , refresh = Every30Seconds
        }
    , sources = [ "dht", "rarbg" ]
    , event = Route.AllTorrentEvents
    }


emptyQueueStats : Route.QueueStatsParams
emptyQueueStats =
    Route.emptyQueueStats


chosenQueueStats : Route.QueueStatsParams
chosenQueueStats =
    { controls =
        { timeframe = Days1
        , resolution = { unit = Minute, every = Just 5 }
        , refresh = Every10Seconds
        }
    , queues = [ "process_torrent_batch" ]
    , events = [ QueueMetrics.Failed, QueueMetrics.Created ]
    }


emptyJobs : Route.JobsParams
emptyJobs =
    Route.emptyJobs


filteredJobs : Route.JobsParams
filteredJobs =
    { queues = [ "process_torrent" ]
    , statuses = [ Failed, Retry ]
    , order = { field = Priority, descending = True }
    , page = 3
    }


roundTripTest : Route.Route -> Test
roundTripTest route =
    test (Route.toHref mount route) <|
        \_ ->
            Route.toHref mount route
                |> (\href -> Url.fromString ("https://example.test" ++ href))
                |> Maybe.map (Route.fromUrl mount)
                |> Expect.equal (Just route)


userIdentity : Identity.Identity
userIdentity =
    Identity.UserAuthenticated user []


adminIdentity : Identity.Identity
adminIdentity =
    Identity.UserAuthenticated user
        [ { namespace = "**", object = "**", action = "**" } ]


adminAction : Identity.ObjectAction
adminAction =
    { namespace = "**", object = "**", action = "**" }


user : Identity.User
user =
    { id = 1
    , username = "user"
    , role = "user"
    , email = Nothing
    , lastLoginAt = Nothing
    , createdAt = Time.millisToPosix 0
    , updatedAt = Time.millisToPosix 0
    }


isStats : Route.Route -> Bool
isStats route =
    case route of
        Route.TorrentStats _ ->
            True

        Route.QueueStats _ ->
            True

        _ ->
            False


withRefresh : AutoRefresh -> StatsControls.Controls -> StatsControls.Controls
withRefresh refresh controls =
    { controls | refresh = refresh }
