module ApiErrorLiveShapeTest exposing (suite)

{-| The exact bytes the homeserver returned on 2026-08-24, decoded through the real
elm-graphql error decoder — not a hand-built Dict.
-}

import ApiError
import Expect
import Graphql.Http
import Graphql.Http.GraphqlError as GraphqlError
import Json.Decode as Decode
import Json.Encode as Encode
import Test exposing (Test, describe, test)


live : String -> ApiError.Failure
live body =
    case Decode.decodeString GraphqlError.decoder body of
        Ok errors ->
            ApiError.fromError
                (Graphql.Http.GraphqlError (GraphqlError.UnparsedData Encode.null) errors)

        Err err ->
            ApiError.ServerRejected ("decoder failed: " ++ Decode.errorToString err)


suite : Test
suite =
    describe "live homeserver payloads"
        [ test "an unauthorized refusal" <|
            \_ ->
                live """{"errors":[{"message":"unauthorized","path":["auth"],"locations":[{"line":1,"column":2}],"extensions":{"action":"query","code":"UNAUTHORIZED","namespace":"graphql","object":"auth"}}],"data":null}"""
                    |> Expect.equal
                        (ApiError.Unauthorized
                            { field = Just "auth"
                            , objectAction =
                                Just { namespace = "graphql", object = "auth", action = "query" }
                            }
                        )
        , test "a failed login" <|
            \_ ->
                live """{"errors":[{"message":"invalid username or password","path":["self","login"],"locations":[{"line":1,"column":15}],"extensions":{"code":"INVALID_CREDENTIALS"}}],"data":null}"""
                    |> Expect.equal ApiError.InvalidCredentials
        ]
