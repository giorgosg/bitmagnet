module IdentityTest exposing (suite)

import Expect
import Identity
import Test exposing (Test, describe, test)


suite : Test
suite =
    describe "Identity.can"
        [ test "the admin wildcard authorizes every GraphQL Object action" <|
            \_ ->
                Identity.Anonymous
                    [ { namespace = "**", object = "**", action = "**" } ]
                    |> Identity.can (Identity.graphql "auth" "mutate")
                    |> Expect.equal True
        , test "literal components authorize their exact Object action" <|
            \_ ->
                Identity.Anonymous
                    [ Identity.graphql "torrentContent" "query" ]
                    |> Identity.can (Identity.graphql "torrentContent" "query")
                    |> Expect.equal True
        , test "a literal Permission does not authorize a different object" <|
            \_ ->
                Identity.Anonymous
                    [ Identity.graphql "torrentContent" "query" ]
                    |> Identity.can (Identity.graphql "auth" "query")
                    |> Expect.equal False
        , test "wildcards are matched independently per component" <|
            \_ ->
                Identity.Anonymous
                    [ { namespace = "graphql", object = "**", action = "query" } ]
                    |> Identity.can (Identity.graphql "torrent" "query")
                    |> Expect.equal True
        , test "Unknown and failed Identity states have no presented authority" <|
            \_ ->
                [ Identity.Unknown, Identity.Failed "offline" ]
                    |> List.any (Identity.can (Identity.graphql "torrentContent" "query"))
                    |> Expect.equal False
        ]
