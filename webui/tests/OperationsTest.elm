module OperationsTest exposing (suite)

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
