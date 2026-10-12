module ApiErrorLiveShapeTest exposing (suite)

{-| The exact bytes a real bitmagnet returned, decoded through the real elm-graphql error
decoder — not a hand-built Dict. Each test says which server and when.
-}

import ApiError
import Dict
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


{-| A refusal that arrived with a status other than 200, as elm-graphql hands it over: the
body undecoded, beside the status.
-}
refusedWith : Int -> String -> ApiError.Failure
refusedWith status body =
    ApiError.fromError
        (Graphql.Http.HttpError
            (Graphql.Http.BadStatus
                { url = "/graphql", statusCode = status, statusText = "", headers = Dict.empty }
                body
            )
        )


suite : Test
suite =
    describe "live homeserver payloads"
        [ describe "refused before anything ran, from the fixture server"
            -- `dev fixture serve` at bitmagnet trunk e76818643, 2026-10-11, through the dev
            -- proxy. gqlgen answers a request it cannot parse or validate with 422, and
            -- bitmagnet answers a cookie-authenticated mutation from another origin, or none,
            -- with 403. elm-graphql reports any status but 2xx as BadStatus, body attached.
            [ test "a field the schema does not have" <|
                \_ ->
                    refusedWith 422 """{"errors":[{"message":"Cannot query field \\"updatePasswordd\\" on type \\"SelfMutation\\". Did you mean \\"updatePassword\\"?","locations":[{"line":1,"column":19}],"extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}],"data":null}"""
                        |> Expect.equal (ApiError.RequestInvalid "Cannot query field \"updatePasswordd\" on type \"SelfMutation\". Did you mean \"updatePassword\"?")
            , test "a document that does not parse" <|
                \_ ->
                    refusedWith 422 """{"errors":[{"message":"Expected Name, found \\u003cEOF\\u003e","locations":[{"line":1,"column":42}],"extensions":{"code":"GRAPHQL_PARSE_FAILED"}}],"data":null}"""
                        |> Expect.equal (ApiError.RequestInvalid "Expected Name, found <EOF>")
            , test "a mutation from another origin" <|
                \_ ->
                    refusedWith 403 """{"errors":[{"message":"browser session request must be same-origin"}],"data":null}"""
                        |> Expect.equal (ApiError.Forbidden "browser session request must be same-origin")
            , test "a mutation with no origin at all" <|
                \_ ->
                    refusedWith 403 """{"errors":[{"message":"browser session request requires an Origin"}],"data":null}"""
                        |> Expect.equal (ApiError.Forbidden "browser session request requires an Origin")
            ]
        , test "a wrong current password, from the fixture server" <|
            \_ ->
                -- The same server and day: updatePassword offered a current password that is
                -- not the User's. It arrives beside partial data, as a refused login does.
                live """{"errors":[{"message":"password is incorrect","path":["self","updatePassword"],"locations":[{"line":1,"column":19}],"extensions":{"code":"PASSWORD_INCORRECT"}}],"data":{"self":{"updatePassword":null}}}"""
                    |> Expect.equal ApiError.PasswordIncorrect
        , test "a refusal to Anonymous, from the fixture server" <|
            \_ ->
                -- `dev fixture serve` at bitmagnet trunk e76818643, 2026-10-11: an Anonymous
                -- purgeJobs, which the anon Role does not hold. Through the dev proxy, as the
                -- browser sees it.
                live """{"errors":[{"message":"authentication required","path":["queue"],"locations":[{"line":2,"column":3}],"extensions":{"action":"mutate","code":"AUTHENTICATION_REQUIRED","namespace":"graphql","object":"queue"}}],"data":null}"""
                    |> Expect.equal
                        (ApiError.AuthenticationRequired
                            { field = Just "queue"
                            , objectAction =
                                Just { namespace = "graphql", object = "queue", action = "mutate" }
                            }
                        )
        , test "an unauthorized refusal, from the homeserver on 2026-08-24" <|
            \_ ->
                live """{"errors":[{"message":"unauthorized","path":["auth"],"locations":[{"line":1,"column":2}],"extensions":{"action":"query","code":"UNAUTHORIZED","namespace":"graphql","object":"auth"}}],"data":null}"""
                    |> Expect.equal
                        (ApiError.Unauthorized
                            { field = Just "auth"
                            , objectAction =
                                Just { namespace = "graphql", object = "auth", action = "query" }
                            }
                        )
        , test "a failed browser login, which arrives beside partial data, from the homeserver on 2026-08-24" <|
            \_ ->
                -- bitmagnet answers a rejected loginBrowser with data present
                -- ("loginBrowser": null) alongside the error. elm-graphql's `send` still
                -- yields Err, because only `parseableErrorAsSuccess` would prefer the
                -- data, and nothing here uses it. A rejected login cannot read as success.
                live """{"errors":[{"message":"invalid username or password","path":["self","loginBrowser"],"locations":[{"line":1,"column":15}],"extensions":{"code":"INVALID_CREDENTIALS"}}],"data":{"self":{"loginBrowser":null}}}"""
                    |> Expect.equal ApiError.InvalidCredentials
        , test "a failed login, from the homeserver on 2026-08-24" <|
            \_ ->
                live """{"errors":[{"message":"invalid username or password","path":["self","login"],"locations":[{"line":1,"column":15}],"extensions":{"code":"INVALID_CREDENTIALS"}}],"data":null}"""
                    |> Expect.equal ApiError.InvalidCredentials
        ]
