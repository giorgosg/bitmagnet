module LoginTest exposing (suite)

import ApiError
import Expect
import Html.Attributes
import Login
import Test exposing (Test, describe, test)
import Test.Html.Query as Query
import Test.Html.Selector as Selector


typed : String -> String -> Login.Form
typed username password =
    Login.empty
        |> Login.withUsername username
        |> Login.withPassword password


messages : Login.Messages ()
messages =
    { usernameChanged = always (), passwordChanged = always (), submitted = () }


rendered : Login.Submission -> Query.Single ()
rendered state =
    typed "ada" "correct horse"
        |> Login.withState state
        |> Login.view messages
        |> Query.fromHtml


suite : Test
suite =
    describe "Login"
        [ describe "canSubmit"
            [ test "a complete form is ready" <|
                \_ ->
                    Login.canSubmit (typed "ada" "correct horse")
                        |> Expect.equal True
            , test "an empty form is not" <|
                \_ ->
                    Login.canSubmit Login.empty
                        |> Expect.equal False
            , test "a username of only spaces is not a username" <|
                \_ ->
                    Login.canSubmit (typed "   " "correct horse")
                        |> Expect.equal False
            , test "a missing password is not ready" <|
                \_ ->
                    Login.canSubmit (typed "ada" "")
                        |> Expect.equal False
            , test "a request in flight is not sent twice" <|
                \_ ->
                    typed "ada" "correct horse"
                        |> Login.withState Login.Submitting
                        |> Login.canSubmit
                        |> Expect.equal False
            , test "a rejected form can be submitted again" <|
                \_ ->
                    typed "ada" "correct horse"
                        |> Login.withState (Login.Rejected ApiError.InvalidCredentials)
                        |> Login.canSubmit
                        |> Expect.equal True
            ]
        , describe "credentials"
            [ test "a trailing space in the username is a typo, not a username" <|
                \_ ->
                    Login.credentials (typed "  ada " "correct horse")
                        |> .username
                        |> Expect.equal "ada"
            , test "a space in the password is a character the User chose" <|
                \_ ->
                    Login.credentials (typed "ada" " correct horse ")
                        |> .password
                        |> Expect.equal " correct horse "
            ]
        , describe "editing"
            [ test "typing dismisses the rejection it no longer describes" <|
                \_ ->
                    typed "ada" "wrong"
                        |> Login.withState (Login.Rejected ApiError.InvalidCredentials)
                        |> Login.withPassword "wrong2"
                        |> .state
                        |> Expect.equal Login.Ready
            , test "typing does not discard the other field" <|
                \_ ->
                    typed "ada" "correct horse"
                        |> Login.withPassword "changed"
                        |> Expect.equal
                            { username = "ada", password = "changed", state = Login.Ready }
            ]
        , describe "view"
            [ test "invalid credentials are announced beside the fields" <|
                \_ ->
                    rendered (Login.Rejected ApiError.InvalidCredentials)
                        |> Query.find [ Selector.attribute (Html.Attributes.attribute "role" "alert") ]
                        |> Query.has
                            [ Selector.text (ApiError.toMessage ApiError.InvalidCredentials) ]
            , test "a rejection marks the fields invalid and points at its own message" <|
                \_ ->
                    rendered (Login.Rejected ApiError.InvalidCredentials)
                        |> Query.findAll [ Selector.attribute (Html.Attributes.attribute "aria-invalid" "true") ]
                        |> Query.count (Expect.equal 2)
            , test "throttling is a status to wait on, not an error on the fields" <|
                \_ ->
                    rendered (Login.Rejected ApiError.LoginThrottled)
                        |> Query.find [ Selector.attribute (Html.Attributes.attribute "role" "status") ]
                        |> Query.has
                            [ Selector.text (ApiError.toMessage ApiError.LoginThrottled) ]
            , test "throttling does not mark what was typed as wrong" <|
                \_ ->
                    rendered (Login.Rejected ApiError.LoginThrottled)
                        |> Query.findAll [ Selector.attribute (Html.Attributes.attribute "aria-invalid" "true") ]
                        |> Query.count (Expect.equal 0)
            , test "an untouched form offers no notice at all" <|
                \_ ->
                    rendered Login.Ready
                        |> Query.findAll [ Selector.class "notice" ]
                        |> Query.count (Expect.equal 0)
            , test "the submit button is disabled until both fields are filled" <|
                \_ ->
                    Login.view messages Login.empty
                        |> Query.fromHtml
                        |> Query.find [ Selector.tag "button" ]
                        |> Query.has [ Selector.disabled True ]
            ]
        , describe "the outcomes the form must tell apart"
            [ test "invalid credentials, a disabled User, and throttling stay distinct" <|
                \_ ->
                    [ ApiError.InvalidCredentials, ApiError.UserDisabled, ApiError.LoginThrottled ]
                        |> List.map (\failure -> Login.withState (Login.Rejected failure) Login.empty)
                        |> List.map .state
                        |> Expect.equal
                            [ Login.Rejected ApiError.InvalidCredentials
                            , Login.Rejected ApiError.UserDisabled
                            , Login.Rejected ApiError.LoginThrottled
                            ]
            ]
        ]
