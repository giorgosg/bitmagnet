module OperationsTest exposing (suite)

import Expect
import Html.Attributes
import Identity
import Operations
import Route
import Test exposing (Test, describe, test)
import Test.Html.Query as Query
import Test.Html.Selector as Selector
import Time


suite : Test
suite =
    describe "Operations"
        [ test "lists the queue's jobs for an Identity holding queue::query" <|
            \_ ->
                Operations.view mount (Identity.Anonymous [ Identity.graphql "health" "query", Identity.graphql "queue" "query" ])
                    |> Query.fromHtml
                    |> Query.find [ Selector.tag "a" ]
                    |> Query.has
                        [ Selector.text "Queue jobs"
                        , Selector.attribute (Html.Attributes.href "/magnes/queue/jobs")
                        ]
        , test "lists the torrent statistics for an Identity holding torrent::query" <|
            \_ ->
                Operations.view mount (Identity.UserAuthenticated user [ Identity.graphql "health" "query", Identity.graphql "torrent" "query" ])
                    |> Query.fromHtml
                    |> Query.find [ Selector.tag "a" ]
                    |> Query.has
                        [ Selector.text "Torrent statistics"
                        , Selector.attribute (Html.Attributes.href "/magnes/stats/torrents")
                        ]
        , test "lists each page only to an Identity that may open it, torrent statistics first" <|
            \_ ->
                Operations.view mount (Identity.Anonymous [ Identity.graphql "queue" "query", Identity.graphql "torrent" "query" ])
                    |> Query.fromHtml
                    |> Query.findAll [ Selector.tag "a" ]
                    |> Expect.all
                        [ Query.count (Expect.equal 2)
                        , Query.index 0 >> Query.has [ Selector.text "Torrent statistics" ]
                        , Query.index 1 >> Query.has [ Selector.text "Queue jobs" ]
                        ]
        , test "lists nothing, heading included, for an Identity holding none of them" <|
            \_ ->
                Operations.view mount (Identity.UserAuthenticated user [ Identity.graphql "health" "query" ])
                    |> Query.fromHtml
                    |> Query.hasNot [ Selector.tag "h2" ]
        ]


mount : Route.BasePath
mount =
    Route.basePath "/magnes"


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
