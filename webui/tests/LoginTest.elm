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
                        |> .id
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
                            { username = "ada", password = "changed", state = Login.Ready, reason = Nothing, filled = False }
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
        , describe "after a password change"
            [ test "opens on the User's own name, with no password" <|
                \_ ->
                    Login.afterPasswordChange "ada"
                        |> Expect.all
                            [ .username >> Expect.equal "ada"
                            , .password >> Expect.equal ""
                            ]
            , test "says why the person is here, so the change does not look as though it failed" <|
                \_ ->
                    Login.afterPasswordChange "ada"
                        |> Login.view messages
                        |> Query.fromHtml
                        |> Query.find [ Selector.attribute (Html.Attributes.attribute "role" "status") ]
                        |> Query.has
                            [ Selector.text "Your password was changed, and that signed you out everywhere, this browser included. Sign in with the new password." ]
            , test "keeps saying so while the person types" <|
                \_ ->
                    Login.afterPasswordChange "ada"
                        |> Login.withPassword "new horse"
                        |> .reason
                        |> Expect.equal (Just Login.PasswordChanged)
            , test "keeps saying so beside a refused attempt" <|
                \_ ->
                    -- The old password being refused is exactly when the reason is worth reading.
                    Login.afterPasswordChange "ada"
                        |> Login.withState (Login.Rejected ApiError.InvalidCredentials)
                        |> .reason
                        |> Expect.equal (Just Login.PasswordChanged)
            , test "is forgotten once the form is left" <|
                \_ ->
                    Login.afterPasswordChange "ada"
                        |> Login.withoutReason
                        |> .reason
                        |> Expect.equal Nothing
            , test "puts the cursor in the password field, the one left to fill" <|
                \_ ->
                    Login.focusFieldId (Login.afterPasswordChange "ada")
                        |> Expect.equal "login-password"
            , test "has the password field read the reason out when it is focused" <|
                \_ ->
                    -- A status present from the start is often not announced; the description
                    -- of the field the cursor lands in is.
                    Login.afterPasswordChange "ada"
                        |> Login.view messages
                        |> Query.fromHtml
                        |> Expect.all
                            [ Query.find [ Selector.id "login-password" ]
                                >> Query.has [ Selector.attribute (Html.Attributes.attribute "aria-describedby" "login-reason") ]
                            , Query.find [ Selector.id "login-reason" ]
                                >> Query.has [ Selector.text "Your password was changed" ]
                            ]
            , test "points the password field at the reason and at a refusal, both" <|
                \_ ->
                    Login.afterPasswordChange "ada"
                        |> Login.withPassword "old horse"
                        |> Login.withState (Login.Rejected ApiError.InvalidCredentials)
                        |> Login.view messages
                        |> Query.fromHtml
                        |> Query.find [ Selector.id "login-password" ]
                        |> Query.has [ Selector.attribute (Html.Attributes.attribute "aria-describedby" "login-reason login-rejection") ]
            ]
        , describe "after a password change whose answer was lost"
            [ test "opens on the User's name and the new password, to be tried first" <|
                \_ ->
                    Login.afterLostChange { id = "ada", password = "new horse" }
                        |> Expect.all
                            [ .username >> Expect.equal "ada"
                            , .password >> Expect.equal "new horse"
                            , .reason >> Expect.equal (Just Login.PasswordMayHaveChanged)
                            ]
            , test "says the password may have changed, and what a refusal of the new one means" <|
                \_ ->
                    Login.afterLostChange { id = "ada", password = "new horse" }
                        |> Login.view messages
                        |> Query.fromHtml
                        |> Query.find [ Selector.id "login-reason" ]
                        |> Query.has
                            [ Selector.text "Your password may have changed: the answer to the change was lost, and you are signed out, as a change would sign you out. Try the new password first; it is filled in. If it is refused, the change was not made, and your old password still works." ]
            , test "puts the cursor in the filled password field, so Enter tries it" <|
                \_ ->
                    Login.focusFieldId (Login.afterLostChange { id = "ada", password = "new horse" })
                        |> Expect.equal "login-password"
            , test "can be sent as it is" <|
                \_ ->
                    Login.canSubmit (Login.afterLostChange { id = "ada", password = "new horse" })
                        |> Expect.equal True
            , test "what is sent, and offered once accepted, is the password filled in" <|
                \_ ->
                    -- The credential travels with the request and comes back with its answer,
                    -- so what the credential store is offered is what bitmagnet accepted.
                    Login.credentials (Login.afterLostChange { id = "ada", password = "new horse" })
                        |> Expect.equal { id = "ada", password = "new horse" }
            , test "keeps a password the person typed over the one it filled in, when the form is left" <|
                \_ ->
                    Login.afterLostChange { id = "ada", password = "new horse" }
                        |> Login.withPassword "old horse"
                        |> Login.withoutReason
                        |> .password
                        |> Expect.equal "old horse"
            , test "a change confirmed while the form is open keeps what is in it, and says so" <|
                \_ ->
                    -- The confirmation is the moment the filled-in password is known to work;
                    -- wiping it then would lose Magnes's only copy where the browser has no
                    -- credential store.
                    Login.afterLostChange { id = "ada", password = "new horse" }
                        |> Login.withState Login.Submitting
                        |> Login.changeConfirmed
                        |> Expect.all
                            [ .username >> Expect.equal "ada"
                            , .password >> Expect.equal "new horse"
                            , .reason >> Expect.equal (Just Login.PasswordChanged)
                            , .state >> Expect.equal Login.Submitting
                            ]
            , test "a confirmed password Magnes filled in still goes when the form is left" <|
                \_ ->
                    Login.afterLostChange { id = "ada", password = "new horse" }
                        |> Login.changeConfirmed
                        |> Login.withoutReason
                        |> .password
                        |> Expect.equal ""
            , test "forgets the password it filled in once the form is left" <|
                \_ ->
                    -- Magnes put it there for that reason; without the reason it is a password
                    -- nobody typed.
                    Login.afterLostChange { id = "ada", password = "new horse" }
                        |> Login.withoutReason
                        |> Expect.all
                            [ .reason >> Expect.equal Nothing
                            , .password >> Expect.equal ""
                            , .username >> Expect.equal "ada"
                            ]
            ]
        , describe "with no reason to give"
            [ test "puts the cursor in the username field" <|
                \_ ->
                    Login.focusFieldId Login.empty
                        |> Expect.equal Login.usernameFieldId
            , test "keeps a password typed when the form is left" <|
                \_ ->
                    -- As it always has: the form outlives a visit, and a reason is what is
                    -- dropped on leaving, not what the person typed.
                    typed "ada" "correct horse"
                        |> Login.withoutReason
                        |> .password
                        |> Expect.equal "correct horse"
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
