module RouteTest exposing (suite)

import Expect
import Identity
import Magnes.Api.Enum.MetricsBucketDuration exposing (MetricsBucketDuration(..))
import Magnes.Api.Enum.QueueJobStatus exposing (QueueJobStatus(..))
import Magnes.Api.Enum.QueueJobsOrderByField exposing (QueueJobsOrderByField(..))
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
        , describe "torrent statistics"
            [ test "the defaults are a bare path" <|
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
        , describe "looking again by itself"
            [ test "a page that asks for it says how often, and every other page says it never does" <|
                \_ ->
                    List.map Route.refreshInterval
                        (Route.TorrentStats chosenStats :: Route.TorrentStats Route.emptyTorrentStats :: List.filter (not << isTorrentStats) routes)
                        |> Expect.equal (Just 30000 :: Nothing :: List.map (always Nothing) (List.filter (not << isTorrentStats) routes))
            , test "two looks that differ only in how often to look again are the same look" <|
                \_ ->
                    let
                        slower =
                            { chosenStats | controls = withRefresh Every5Minutes chosenStats.controls }
                    in
                    ( Route.withoutRefresh (Route.TorrentStats chosenStats) == Route.withoutRefresh (Route.TorrentStats slower)
                    , Route.withoutRefresh (Route.TorrentStats chosenStats) == Route.withoutRefresh (Route.TorrentStats { chosenStats | sources = [] })
                    )
                        |> Expect.equal ( True, False )
            , test "leaves a page that does not look again by itself as it is" <|
                \_ ->
                    List.map Route.withoutRefresh (List.filter (not << isTorrentStats) routes)
                        |> Expect.equal (List.filter (not << isTorrentStats) routes)
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
    , Route.Status
    , Route.QueueJobs Route.emptyJobs
    , Route.QueueJobs filteredJobs
    , Route.QueueJobs { emptyJobs | order = { field = Created_at, descending = False } }
    , Route.TorrentStats Route.emptyTorrentStats
    , Route.TorrentStats chosenStats
    , Route.TorrentStats { emptyStats | controls = { timeframe = Hours1, resolution = { unit = Minute, every = Just 1 }, refresh = Off } }
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


isTorrentStats : Route.Route -> Bool
isTorrentStats route =
    case route of
        Route.TorrentStats _ ->
            True

        _ ->
            False


withRefresh : AutoRefresh -> StatsControls.Controls -> StatsControls.Controls
withRefresh refresh controls =
    { controls | refresh = refresh }
