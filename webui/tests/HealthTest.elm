module HealthTest exposing (suite)

import ApiError
import Expect
import Graphql.Document
import Health
import Html.Attributes
import Identity
import Json.Decode as Decode
import Magnes.Api.Enum.HealthStatus exposing (HealthStatus(..))
import Route
import Test exposing (Test, describe, test)
import Test.Html.Query as Query
import Test.Html.Selector as Selector
import Time


user : Identity.User
user =
    { id = 1
    , username = "grace"
    , role = "user"
    , email = Nothing
    , lastLoginAt = Nothing
    , createdAt = Time.millisToPosix 0
    , updatedAt = Time.millisToPosix 0
    }


{-| The core `user` Role: health, but not workers.
-}
ordinary : Identity.Identity
ordinary =
    Identity.UserAuthenticated user [ Identity.graphql "health" "query" ]


{-| A seeded `anon` Role while anonymous access is on: health and workers both.
-}
anonymous : Identity.Identity
anonymous =
    Identity.Anonymous [ Identity.graphql "health" "query", Identity.graphql "workers" "query" ]


{-| Shaped as bitmagnet's resolver answers: checks sorted by key, `error` null unless the
check failed, RFC 3339 with microseconds, and Go's zero time for a check that has never
run.
-}
answer : String
answer =
    """
    {"data": {
      "health": {
        "status": "down",
        "checks": [
          {"key": "dht", "status": "down", "timestamp": "2026-10-06T14:02:51.204117Z", "error": "no peers responded"},
          {"key": "postgres", "status": "up", "timestamp": "2026-10-06T14:03:07.862002Z", "error": null},
          {"key": "tmdb", "status": "inactive", "timestamp": "0001-01-01T00:00:00Z", "error": null}
        ]
      },
      "workers": {"listAll": {"workers": [
        {"key": "dht_crawler", "started": true},
        {"key": "queue_server", "started": false}
      ]}}
    }}
    """


{-| 2026-10-06T14:03:07Z.
-}
lastChecked : Time.Posix
lastChecked =
    Time.millisToPosix 1791295387000


report : HealthStatus -> Health.Report
report status =
    { status = status
    , checks =
        [ { key = "postgres", status = Up, checkedAt = Just lastChecked, error = Nothing } ]
    , workers = Nothing
    }


{-| One check in each status, and one whose key Magnes has no name for.
-}
everyKind : Health.Report
everyKind =
    { status = Down
    , checks =
        [ { key = "dht", status = Down, checkedAt = Just lastChecked, error = Just "no peers responded" }
        , { key = "postgres", status = Up, checkedAt = Just lastChecked, error = Nothing }
        , { key = "search_index", status = Unknown, checkedAt = Nothing, error = Nothing }
        , { key = "tmdb", status = Inactive, checkedAt = Nothing, error = Nothing }
        ]
    , workers = Nothing
    }


checkRows : Health.Report -> Query.Multiple msg
checkRows r =
    page (Health.Reported r)
        |> Query.find [ Selector.class "health-checks" ]
        |> Query.find [ Selector.tag "tbody" ]
        |> Query.findAll [ Selector.tag "tr" ]


indicator : Health.State -> Query.Single msg
indicator state =
    Health.indicator (Route.basePath "") state |> Query.fromHtml


{-| The link, by its accessible name.
-}
named : String -> List Selector.Selector
named name =
    [ Selector.tag "a", Selector.attribute (Html.Attributes.attribute "aria-label" name) ]


page : Health.State -> Query.Single msg
page state =
    Health.view Time.utc state |> Query.fromHtml


suite : Test
suite =
    describe "Health"
        [ describe "query"
            [ test "asks for workers when workers::query is held" <|
                \_ ->
                    Graphql.Document.serializeQuery (Health.query anonymous)
                        |> String.contains "workers"
                        |> Expect.equal True
            , test "does not ask for workers otherwise, so a refusal cannot fail the page" <|
                \_ ->
                    Graphql.Document.serializeQuery (Health.query ordinary)
                        |> String.contains "workers"
                        |> Expect.equal False
            , test "reads bitmagnet's answer, and a check that has never run as never checked" <|
                \_ ->
                    Decode.decodeString (Graphql.Document.decoder (Health.query anonymous)) answer
                        |> Expect.equal
                            (Ok
                                { status = Down
                                , checks =
                                    [ { key = "dht"
                                      , status = Down

                                      -- 2026-10-06T14:02:51.204Z
                                      , checkedAt = Just (Time.millisToPosix 1791295371204)
                                      , error = Just "no peers responded"
                                      }
                                    , { key = "postgres"
                                      , status = Up
                                      , checkedAt = Just (Time.millisToPosix 1791295387862)
                                      , error = Nothing
                                      }
                                    , { key = "tmdb", status = Inactive, checkedAt = Nothing, error = Nothing }
                                    ]
                                , workers =
                                    Just
                                        [ { key = "dht_crawler", started = True }
                                        , { key = "queue_server", started = False }
                                        ]
                                }
                            )
            ]
        , describe "the status page"
            [ describe "says how bitmagnet is"
                [ test "up" <|
                    \_ ->
                        page (Health.Reported (report Up))
                            |> Query.has [ Selector.text "bitmagnet is up." ]
                , test "down, as degraded: bitmagnet answered, so it is not down itself" <|
                    \_ ->
                        page (Health.Reported (report Down))
                            |> Query.has [ Selector.text "bitmagnet is degraded: a check is down." ]
                , test "unknown, as not yet known rather than as a fault" <|
                    \_ ->
                        page (Health.Reported (report Unknown))
                            |> Query.has [ Selector.text "Not every check has run yet." ]
                , test "inactive" <|
                    \_ ->
                        page (Health.Reported (report Inactive))
                            |> Query.has [ Selector.text "bitmagnet is inactive." ]
                , test "nothing yet, while the first answer is awaited" <|
                    \_ ->
                        page Health.Unasked
                            |> Query.has [ Selector.text "Checking…" ]
                , test "no answer at all, out loud" <|
                    \_ ->
                        page (Health.Unavailable ApiError.Unreachable)
                            |> Query.find [ Selector.attribute (Html.Attributes.attribute "role" "alert") ]
                            |> Query.has [ Selector.text (ApiError.toMessage ApiError.Unreachable) ]
                ]
            , describe "lists each check"
                [ test "by name, with its status and when it was last checked" <|
                    \_ ->
                        checkRows everyKind
                            |> Query.index 1
                            |> Expect.all
                                [ Query.has [ Selector.text "Postgres" ]
                                , Query.has [ Selector.text "Up" ]
                                , Query.has [ Selector.text "2026-10-06 14:03" ]
                                ]
                , test "and keeps a key Magnes has no name for" <|
                    \_ ->
                        checkRows everyKind
                            |> Query.index 2
                            |> Query.has [ Selector.text "search_index" ]
                , test "a down check shows its error" <|
                    \_ ->
                        checkRows everyKind
                            |> Query.index 0
                            |> Expect.all
                                [ Query.has [ Selector.text "DHT" ]
                                , Query.has [ Selector.text "Down" ]
                                , Query.has [ Selector.text "no peers responded" ]
                                ]
                , test "a check that has not run reads as pending, and as never checked" <|
                    \_ ->
                        checkRows everyKind
                            |> Query.index 2
                            |> Expect.all
                                [ Query.has [ Selector.text "Pending" ]
                                , Query.has [ Selector.text "Never" ]
                                ]
                , test "an inactive check reads as inactive" <|
                    \_ ->
                        checkRows everyKind
                            |> Query.index 3
                            |> Expect.all
                                [ Query.has [ Selector.text "TMDB" ]
                                , Query.has [ Selector.text "Inactive" ]
                                ]
                , test "only a down check is marked as failing" <|
                    \_ ->
                        checkRows everyKind
                            |> Expect.all
                                [ Query.index 0 >> Query.has [ Selector.class "health-failing" ]
                                , Query.index 1 >> Query.hasNot [ Selector.class "health-failing" ]
                                , Query.index 2 >> Query.hasNot [ Selector.class "health-failing" ]
                                , Query.index 3 >> Query.hasNot [ Selector.class "health-failing" ]
                                ]
                ]
            , describe "lists the workers"
                [ test "each with whether it has started, when they were asked for" <|
                    \_ ->
                        page
                            (Health.Reported
                                { everyKind
                                    | workers =
                                        Just
                                            [ { key = "dht_crawler", started = True }
                                            , { key = "queue_server", started = False }
                                            , { key = "importer", started = True }
                                            ]
                                }
                            )
                            |> Query.find [ Selector.class "health-workers" ]
                            |> Query.find [ Selector.tag "tbody" ]
                            |> Query.findAll [ Selector.tag "tr" ]
                            |> Expect.all
                                [ Query.count (Expect.equal 3)
                                , Query.index 0 >> Query.has [ Selector.text "DHT crawler" ]
                                , Query.index 0 >> Query.has [ Selector.text "Started" ]
                                , Query.index 1 >> Query.has [ Selector.text "Queue server" ]
                                , Query.index 1 >> Query.has [ Selector.text "Not started" ]
                                , Query.index 2 >> Query.has [ Selector.text "importer" ]
                                ]
                , test "and says so when there are none" <|
                    \_ ->
                        page (Health.Reported { everyKind | workers = Just [] })
                            |> Expect.all
                                [ Query.has [ Selector.text "No workers are registered." ]
                                , Query.findAll [ Selector.class "health-workers" ] >> Query.count (Expect.equal 0)
                                ]
                , test "but not for an Identity that may not read them, which is told why" <|
                    \_ ->
                        page (Health.Reported { everyKind | workers = Nothing })
                            |> Expect.all
                                [ Query.findAll [ Selector.class "health-workers" ] >> Query.count (Expect.equal 0)
                                , Query.has [ Selector.text "Your Identity may not see bitmagnet's workers." ]
                                ]
                ]
            ]
        , describe "the header indicator"
            [ test "draws nothing until bitmagnet has answered" <|
                \_ ->
                    indicator Health.Unasked
                        |> Query.findAll [ Selector.tag "a" ]
                        |> Query.count (Expect.equal 0)
            , test "links to the status page, named for how bitmagnet is" <|
                \_ ->
                    indicator (Health.Reported (report Up))
                        |> Query.has (Selector.attribute (Html.Attributes.href "/status") :: named "bitmagnet is up.")
            , test "is quiet while bitmagnet is up" <|
                \_ ->
                    indicator (Health.Reported (report Up))
                        |> Expect.all
                            [ Query.hasNot [ Selector.class "health-alarm" ]
                            , Query.findAll [ Selector.class "health-word" ] >> Query.count (Expect.equal 0)
                            ]
            , test "says degraded, and raises the alarm, when a check is down" <|
                \_ ->
                    indicator (Health.Reported (report Down))
                        |> Expect.all
                            [ Query.has (Selector.class "health-alarm" :: named "bitmagnet is degraded: a check is down.")
                            , Query.find [ Selector.class "health-word" ] >> Query.has [ Selector.text "degraded" ]
                            ]
            , test "treats pending as quiet, not as an alarm" <|
                \_ ->
                    indicator (Health.Reported (report Unknown))
                        |> Expect.all
                            [ Query.has (named "Not every check has run yet.")
                            , Query.hasNot [ Selector.class "health-alarm" ]
                            ]
            , test "treats inactive as quiet, not as an alarm" <|
                \_ ->
                    indicator (Health.Reported (report Inactive))
                        |> Expect.all
                            [ Query.has (named "bitmagnet is inactive.")
                            , Query.hasNot [ Selector.class "health-alarm" ]
                            ]
            , test "says unavailable, and why, when bitmagnet does not answer" <|
                \_ ->
                    indicator (Health.Unavailable ApiError.Unreachable)
                        |> Expect.all
                            [ Query.has
                                (Selector.class "health-alarm"
                                    :: named "bitmagnet's health is unavailable: Could not reach bitmagnet."
                                )
                            , Query.find [ Selector.class "health-word" ] >> Query.has [ Selector.text "unavailable" ]
                            ]
            ]
        ]
