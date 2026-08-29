module ApiErrorTest exposing (suite)

import ApiError
import Dict
import Expect
import Graphql.Http
import Graphql.Http.GraphqlError as GraphqlError
import Identity
import Json.Encode as Encode
import Test exposing (Test, describe, test)


{-| A GraphQL error carrying the extensions bitmagnet's error presenter emits.
-}
coded : String -> List ( String, Encode.Value ) -> GraphqlError.GraphqlError
coded code extras =
    withPath [] (codedAt code extras)


codedAt : String -> List ( String, Encode.Value ) -> GraphqlError.GraphqlError
codedAt code extras =
    { message = "some human-readable message"
    , locations = Nothing
    , details =
        Dict.fromList
            [ ( "extensions"
              , Encode.object (( "code", Encode.string code ) :: extras)
              )
            ]
    }


withPath : List String -> GraphqlError.GraphqlError -> GraphqlError.GraphqlError
withPath path error =
    { error
        | details =
            Dict.insert "path" (Encode.list Encode.string path) error.details
    }


unrefused : ApiError.Refusal
unrefused =
    { field = Nothing, objectAction = Nothing }


bare : String -> GraphqlError.GraphqlError
bare message =
    { message = message, locations = Nothing, details = Dict.empty }


from : List GraphqlError.GraphqlError -> ApiError.Failure
from errors =
    ApiError.fromError (Graphql.Http.GraphqlError (GraphqlError.UnparsedData Encode.null) errors)


graphqlAuthQuery : List ( String, Encode.Value )
graphqlAuthQuery =
    [ ( "namespace", Encode.string "graphql" )
    , ( "object", Encode.string "auth" )
    , ( "action", Encode.string "query" )
    ]


suite : Test
suite =
    describe "ApiError"
        [ describe "fromError"
            [ test "invalid credentials are their own outcome" <|
                \_ ->
                    from [ coded "INVALID_CREDENTIALS" [] ]
                        |> Expect.equal ApiError.InvalidCredentials
            , test "throttling is not reported as an invalid password" <|
                \_ ->
                    from [ coded "LOGIN_THROTTLED" [] ]
                        |> Expect.equal ApiError.LoginThrottled
            , test "a disabled User is distinguishable from a wrong password" <|
                \_ ->
                    from [ coded "USER_DISABLED" [] ]
                        |> Expect.equal ApiError.UserDisabled
            , test "each Invitation failure keeps its own outcome" <|
                \_ ->
                    List.map (\code -> from [ coded code [] ])
                        [ "INVITATION_REQUIRED"
                        , "INVITATION_INVALID"
                        , "INVITATION_EXPIRED"
                        , "INVITATION_CLAIMED"
                        ]
                        |> Expect.equal
                            [ ApiError.InvitationRequired
                            , ApiError.InvitationInvalid
                            , ApiError.InvitationExpired
                            , ApiError.InvitationClaimed
                            ]
            , test "duplicate Users and weak passwords are distinct" <|
                \_ ->
                    List.map (\code -> from [ coded code [] ])
                        [ "USER_ALREADY_EXISTS", "PASSWORD_INSUFFICIENT_ENTROPY", "USERNAME_INVALID" ]
                        |> Expect.equal
                            [ ApiError.UserAlreadyExists
                            , ApiError.PasswordInsufficientEntropy
                            , ApiError.UsernameInvalid
                            ]
            , test "an instance that requires an email says so rather than refusing barely" <|
                \_ ->
                    from [ coded "EMAIL_REQUIRED" [] ]
                        |> Expect.equal ApiError.EmailRequired
            , test "an authorization refusal carries the refused Object action" <|
                \_ ->
                    from
                        [ coded "UNAUTHORIZED"
                            [ ( "namespace", Encode.string "graphql" )
                            , ( "object", Encode.string "auth" )
                            , ( "action", Encode.string "query" )
                            ]
                        ]
                        |> Expect.equal
                            (ApiError.Unauthorized
                                { field = Nothing
                                , objectAction =
                                    Just { namespace = "graphql", object = "auth", action = "query" }
                                }
                            )
            , test "an authorization refusal without an Object action is still unauthorized" <|
                \_ ->
                    from [ coded "UNAUTHORIZED" [] ]
                        |> Expect.equal (ApiError.Unauthorized unrefused)
            , test "a refusal records the top-level field from the error path" <|
                \_ ->
                    from [ withPath [ "self", "login" ] (codedAt "UNAUTHORIZED" []) ]
                        |> Expect.equal
                            (ApiError.Unauthorized { unrefused | field = Just "self" })
            , test "the refused Object action feeds Identity.can unchanged" <|
                \_ ->
                    case from [ coded "UNAUTHORIZED" graphqlAuthQuery ] of
                        ApiError.Unauthorized { objectAction } ->
                            objectAction
                                |> Maybe.map
                                    (\refused ->
                                        Identity.can refused
                                            (Identity.Anonymous [ Identity.graphql "auth" "query" ])
                                    )
                                |> Expect.equal (Just True)

                        _ ->
                            Expect.fail "expected an authorization refusal"
            , test "the authentication service being down is not an internal fault" <|
                \_ ->
                    from [ coded "AUTHENTICATION_INFRASTRUCTURE_FAILURE" [] ]
                        |> Expect.equal ApiError.ServiceUnavailable
            , test "an internal server error is a server fault" <|
                \_ ->
                    from [ coded "INTERNAL_SERVER_ERROR" [] ]
                        |> Expect.equal ApiError.ServerFault
            , test "the first coded error wins over an earlier uncoded one" <|
                \_ ->
                    from [ bare "something vague", coded "USER_DISABLED" [] ]
                        |> Expect.equal ApiError.UserDisabled
            , test "an unrecognized code falls back to the server's message" <|
                \_ ->
                    from [ coded "SOME_FUTURE_CODE" [] ]
                        |> Expect.equal (ApiError.ServerRejected "some human-readable message")
            , test "an uncoded error falls back to the server's message" <|
                \_ ->
                    from [ bare "user: login failed: something odd" ]
                        |> Expect.equal (ApiError.ServerRejected "user: login failed: something odd")
            , test "an empty error list still produces a failure" <|
                \_ ->
                    from []
                        |> Expect.equal (ApiError.ServerRejected "The server rejected the query.")
            , test "transport failures are not confused with server outcomes" <|
                \_ ->
                    ApiError.fromError (Graphql.Http.HttpError Graphql.Http.NetworkError)
                        |> Expect.equal ApiError.Unreachable
            , test "a bad status keeps its status code" <|
                \_ ->
                    ApiError.fromError
                        (Graphql.Http.HttpError
                            (Graphql.Http.BadStatus
                                { url = "/graphql"
                                , statusCode = 502
                                , statusText = "Bad Gateway"
                                , headers = Dict.empty
                                }
                                ""
                            )
                        )
                        |> Expect.equal (ApiError.BadStatus 502)
            ]
        , describe "isUnauthorized"
            [ test "an UNAUTHORIZED refusal triggers an Identity refresh" <|
                \_ ->
                    ApiError.isUnauthorized (ApiError.Unauthorized unrefused)
                        |> Expect.equal True
            , test "an operation needing a User-authenticated Identity means the Identity is stale" <|
                \_ ->
                    ApiError.isUnauthorized ApiError.UserAuthenticationRequired
                        |> Expect.equal True
            , test "an invalid password says nothing about the current Identity" <|
                \_ ->
                    ApiError.isUnauthorized ApiError.InvalidCredentials
                        |> Expect.equal False
            , test "a message the mapping does not recognize is not an authorization refusal" <|
                \_ ->
                    ApiError.isUnauthorized (ApiError.ServerRejected "unauthorized-looking text")
                        |> Expect.equal False
            ]
        , describe "toMessage"
            [ test "every outcome has a non-empty message" <|
                \_ ->
                    [ ApiError.InvalidCredentials
                    , ApiError.UserDisabled
                    , ApiError.LoginThrottled
                    , ApiError.UserAlreadyExists
                    , ApiError.UsernameInvalid
                    , ApiError.InvitationRequired
                    , ApiError.InvitationInvalid
                    , ApiError.InvitationExpired
                    , ApiError.InvitationClaimed
                    , ApiError.PasswordInsufficientEntropy
                    , ApiError.Unauthorized unrefused
                    , ApiError.UserAuthenticationRequired
                    , ApiError.ApiKeyManagementForbidden
                    , ApiError.ServiceUnavailable
                    , ApiError.ServerFault
                    , ApiError.ServerRejected "x"
                    , ApiError.Unreachable
                    , ApiError.Timeout
                    , ApiError.BadStatus 502
                    , ApiError.BadUrl "nope"
                    , ApiError.SchemaMismatch
                    ]
                        |> List.filter (String.isEmpty << ApiError.toMessage)
                        |> Expect.equal []
            , test "throttling reads as a wait, not a credential problem" <|
                \_ ->
                    ApiError.toMessage ApiError.LoginThrottled
                        |> String.toLower
                        |> String.contains "password"
                        |> Expect.equal False
            , test "an unrecognized error keeps the server's own words" <|
                \_ ->
                    ApiError.toMessage (ApiError.ServerRejected "user: register failed: nope")
                        |> Expect.equal "user: register failed: nope"
            ]
        ]
