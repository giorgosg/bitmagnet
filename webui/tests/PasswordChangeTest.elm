module PasswordChangeTest exposing (suite)

import ApiError
import Dict
import Expect
import Graphql.Document
import Graphql.Http
import Graphql.Http.GraphqlError as GraphqlError
import Html.Attributes
import Json.Decode as Decode
import Json.Encode as Encode
import PasswordChange
import PasswordEntropy
import Test exposing (Test, describe, test)
import Test.Html.Query as Query
import Test.Html.Selector as Selector


typed : String -> String -> PasswordChange.Form
typed current new =
    PasswordChange.empty
        |> PasswordChange.withCurrent current
        |> PasswordChange.withNew new


messages : PasswordChange.Messages ()
messages =
    { currentChanged = always (), newChanged = always (), submitted = () }


rendered : PasswordChange.Form -> Query.Single ()
rendered form =
    PasswordChange.view messages form |> Query.fromHtml


measured : Float -> Float -> PasswordEntropy.Entropy
measured entropy minEntropy =
    PasswordEntropy.Measured { entropy = entropy, minEntropy = minEntropy }


{-| The form as it is while the change numbered 1 is on its way, and that change. A form
that would not send stands in as an empty one numbered 0, which no test here expects.
-}
waiting : ( PasswordChange.Form, PasswordChange.Sent )
waiting =
    PasswordChange.sending 1 "ada" (typed "old horse" "new horse")
        |> Maybe.withDefault ( PasswordChange.empty, { attempt = 0, credential = { id = "", password = "" } } )


{-| bitmagnet answering with the code its error presenter emits.
-}
refused : String -> Graphql.Http.Error ()
refused code =
    Graphql.Http.GraphqlError (GraphqlError.UnparsedData Encode.null)
        [ { message = "some human-readable message"
          , locations = Nothing
          , details = Dict.fromList [ ( "extensions", Encode.object [ ( "code", Encode.string code ) ] ) ]
          }
        ]


{-| The answer to the change `waiting` sent.
-}
answered : Result (Graphql.Http.Error ()) () -> PasswordChange.Outcome
answered result =
    PasswordChange.answer (Tuple.second waiting) result (Tuple.first waiting)


suite : Test
suite =
    describe "PasswordChange"
        [ describe "canSubmit"
            [ test "the current password and a new one are enough" <|
                \_ ->
                    PasswordChange.canSubmit (typed "old horse" "new horse")
                        |> Expect.equal True
            , test "an empty form is not ready" <|
                \_ ->
                    PasswordChange.canSubmit PasswordChange.empty
                        |> Expect.equal False
            , test "a new password without the current one is not ready" <|
                \_ ->
                    PasswordChange.canSubmit (typed "" "new horse")
                        |> Expect.equal False
            , test "the current password without a new one is not ready" <|
                \_ ->
                    PasswordChange.canSubmit (typed "old horse" "")
                        |> Expect.equal False
            , test "a password of spaces is a password, since a space is a character the User chose" <|
                \_ ->
                    PasswordChange.canSubmit (typed "   " "   ")
                        |> Expect.equal True
            , test "a change on its way is not sent twice" <|
                \_ ->
                    typed "old horse" "new horse"
                        |> PasswordChange.sending 1 "ada"
                        |> Maybe.map (Tuple.first >> PasswordChange.canSubmit)
                        |> Expect.equal (Just False)
            , test "typing does not forget a change on its way" <|
                \_ ->
                    -- Its answer is still coming, and the button must not offer a second one
                    -- before it does.
                    typed "old horse" "new horse"
                        |> PasswordChange.sending 1 "ada"
                        |> Maybe.map (Tuple.first >> PasswordChange.withNew "newer horse" >> PasswordChange.canSubmit)
                        |> Expect.equal (Just False)
            ]
        , describe "before anything is sent"
            [ test "says that every device is signed out, this one included" <|
                \_ ->
                    rendered PasswordChange.empty
                        |> Query.has
                            [ Selector.text "Changing your password signs you out everywhere: every device and browser signed in as you, this one included. You then sign in again with the new password." ]
            , test "says that API keys keep working" <|
                \_ ->
                    -- bitmagnet's revocation is of what a password opened. A key is a credential
                    -- of its own, and someone rotating a leaked password needs to know that
                    -- a leaked key is not dealt with by this.
                    rendered PasswordChange.empty
                        |> Query.has
                            [ Selector.text "Your API keys are not affected and keep working. Revoke any you no longer trust on the API keys page." ]
            , test "a change on its way says so, and offers no second one" <|
                \_ ->
                    rendered (Tuple.first waiting)
                        |> Query.find [ Selector.tag "button" ]
                        |> Query.has [ Selector.text "Changing…", Selector.disabled True ]
            ]
        , describe "the entropy meter"
            [ test "a new password abandons the measurement of the old one" <|
                \_ ->
                    typed "old horse" "new horse"
                        |> PasswordChange.measured (measured 80 70)
                        |> PasswordChange.withNew "newer horse"
                        |> .entropy
                        |> Expect.equal PasswordEntropy.Measuring
            , test "the current password is never measured" <|
                \_ ->
                    -- It is not being chosen, and bitmagnet already holds it to the minimum.
                    PasswordChange.empty
                        |> PasswordChange.withCurrent "old horse"
                        |> .entropy
                        |> Expect.equal PasswordEntropy.Unmeasured
            , test "a measurement lands only on a form waiting for one" <|
                \_ ->
                    -- A form emptied while a measurement was on its way, by an Identity
                    -- change or a change accepted, must not grow a meter under a blank field.
                    PasswordChange.empty
                        |> PasswordChange.measured (measured 80 70)
                        |> .entropy
                        |> Expect.equal PasswordEntropy.Unmeasured
            , test "measures the new password against the minimum the server named" <|
                \_ ->
                    rendered (PasswordChange.measured (measured 42 70) (typed "old horse" "new horse"))
                        |> Query.find [ Selector.class "entropy" ]
                        |> Query.has [ Selector.tag "progress", Selector.text "42 of 70" ]
            , test "says it is measuring rather than reading as weak" <|
                \_ ->
                    rendered (typed "old horse" "new horse")
                        |> Query.has [ Selector.text "Measuring…" ]
            , test "a meter too weak still leaves the change to bitmagnet" <|
                \_ ->
                    -- As at registration: the meter is advice from a query that may be stale
                    -- or may have failed, so refusing to send on it would make a broken
                    -- query look like a refused password.
                    typed "old horse" "new horse"
                        |> PasswordChange.measured (measured 12 70)
                        |> PasswordChange.canSubmit
                        |> Expect.equal True
            ]
        , describe "the answer"
            [ test "an accepted change offers the credential it leaves the User with" <|
                \_ ->
                    answered (Ok ())
                        |> Expect.equal (PasswordChange.Changed { id = "ada", password = "new horse" })
            , test "offers what was sent, not what the form holds now" <|
                \_ ->
                    -- The person may have typed on while the change was on its way; the password
                    -- bitmagnet accepted is the one that was sent.
                    PasswordChange.answer (Tuple.second waiting)
                        (Ok ())
                        (PasswordChange.withNew "typed afterwards" (Tuple.first waiting))
                        |> Expect.equal (PasswordChange.Changed { id = "ada", password = "new horse" })
            , test "no other answer offers a credential" <|
                \_ ->
                    [ refused "PASSWORD_INCORRECT"
                    , refused "PASSWORD_INSUFFICIENT_ENTROPY"
                    , refused "USER_SESSION_REQUIRED"
                    , refused "INTERNAL_SERVER_ERROR"
                    , Graphql.Http.HttpError Graphql.Http.NetworkError
                    , Graphql.Http.HttpError Graphql.Http.Timeout
                    ]
                        |> List.map (Err >> answered)
                        |> List.filter offersCredential
                        |> Expect.equal []
            ]
        , describe "a refusal"
            [ test "a wrong current password is marked on the current password" <|
                \_ ->
                    answered (Err (refused "PASSWORD_INCORRECT"))
                        |> renderedAnswer
                        |> Expect.all
                            [ Query.find [ Selector.id "password-current" ]
                                >> Query.has
                                    [ Selector.attribute (Html.Attributes.attribute "aria-invalid" "true")
                                    , Selector.attribute (Html.Attributes.attribute "aria-describedby" "password-current-error")
                                    ]
                            , Query.find [ Selector.id "password-current-error" ]
                                >> Query.has
                                    [ Selector.attribute (Html.Attributes.attribute "role" "alert")
                                    , Selector.text (ApiError.toMessage ApiError.PasswordIncorrect)
                                    ]
                            , Query.find [ Selector.id "password-new" ]
                                >> Query.has [ Selector.attribute (Html.Attributes.attribute "aria-invalid" "false") ]
                            ]
            , test "a weak new password is marked on the new password" <|
                \_ ->
                    answered (Err (refused "PASSWORD_INSUFFICIENT_ENTROPY"))
                        |> renderedAnswer
                        |> Expect.all
                            [ Query.find [ Selector.id "password-new" ]
                                >> Query.has
                                    [ Selector.attribute (Html.Attributes.attribute "aria-invalid" "true")
                                    , Selector.attribute (Html.Attributes.attribute "aria-describedby" "password-new-error")
                                    ]
                            , Query.find [ Selector.id "password-new-error" ]
                                >> Query.has [ Selector.text (ApiError.toMessage ApiError.PasswordInsufficientEntropy) ]
                            , Query.find [ Selector.id "password-current" ]
                                >> Query.has [ Selector.attribute (Html.Attributes.attribute "aria-invalid" "false") ]
                            ]
            , test "a refusal about neither field marks neither, and says nothing changed" <|
                \_ ->
                    answered (Err (refused "INTERNAL_SERVER_ERROR"))
                        |> renderedAnswer
                        |> Expect.all
                            [ Query.findAll [ Selector.attribute (Html.Attributes.attribute "aria-invalid" "true") ]
                                >> Query.count (Expect.equal 0)
                            , Query.find [ Selector.attribute (Html.Attributes.attribute "role" "alert") ]
                                >> Query.has
                                    [ Selector.text
                                        ("Your password was not changed. " ++ ApiError.toMessage ApiError.ServerFault)
                                    ]
                            ]
            , test "can be answered by trying again" <|
                \_ ->
                    answered (Err (refused "PASSWORD_INCORRECT"))
                        |> shownForm
                        |> Maybe.map PasswordChange.canSubmit
                        |> Expect.equal (Just True)
            , test "typing dismisses the refusal it no longer describes" <|
                \_ ->
                    answered (Err (refused "PASSWORD_INCORRECT"))
                        |> shownForm
                        |> Maybe.map (PasswordChange.withCurrent "old horse, again" >> .state)
                        |> Expect.equal (Just PasswordChange.Ready)
            ]
        , describe "no usable answer"
            [ test "leaves the change unknown rather than refused" <|
                \_ ->
                    -- The request may have reached bitmagnet and committed before the answer
                    -- was lost. Saying it failed would send the person back to a password that
                    -- may no longer work.
                    answered (Err (Graphql.Http.HttpError Graphql.Http.NetworkError))
                        |> shownForm
                        |> Maybe.map .state
                        |> Expect.equal (Just (PasswordChange.Unknown ApiError.Unreachable))
            , test "an answer that could not be read is no answer" <|
                \_ ->
                    answered (Err (Graphql.Http.HttpError (Graphql.Http.BadPayload (Decode.Failure "not bitmagnet's" Encode.null))))
                        |> shownForm
                        |> Maybe.map .state
                        |> Expect.equal (Just (PasswordChange.Unknown ApiError.SchemaMismatch))
            , test "says the password may have changed, and which one signs in if it did" <|
                \_ ->
                    answered (Err (Graphql.Http.HttpError Graphql.Http.NetworkError))
                        |> renderedAnswer
                        |> Query.find [ Selector.attribute (Html.Attributes.attribute "role" "alert") ]
                        |> Query.has
                            [ Selector.text "No usable answer to the change came back."
                            , Selector.text (ApiError.toMessage ApiError.Unreachable)
                            , Selector.text "Whether your password was changed is not known."
                            , Selector.text "If it was, you are signed out everywhere and only the new password signs in."
                            ]
            , test "does not claim the password was not changed" <|
                \_ ->
                    answered (Err (Graphql.Http.HttpError Graphql.Http.NetworkError))
                        |> renderedAnswer
                        |> Query.hasNot [ Selector.text "was not changed" ]
            , test "blames neither field" <|
                \_ ->
                    answered (Err (Graphql.Http.HttpError Graphql.Http.Timeout))
                        |> renderedAnswer
                        |> Query.findAll [ Selector.attribute (Html.Attributes.attribute "aria-invalid" "true") ]
                        |> Query.count (Expect.equal 0)
            ]
        , describe "an answer the form is not waiting for"
            [ test "is not shown on a form emptied since it was sent" <|
                \_ ->
                    PasswordChange.answer (Tuple.second waiting) (Err (refused "PASSWORD_INCORRECT")) PasswordChange.empty
                        |> Expect.equal (PasswordChange.Answered PasswordChange.empty)
            , test "is not shown on a form waiting for a later change" <|
                \_ ->
                    let
                        later =
                            PasswordChange.empty
                                |> PasswordChange.withCurrent "old horse"
                                |> PasswordChange.withNew "newer horse"
                                |> PasswordChange.sending 2 "ada"
                                |> Maybe.map Tuple.first
                                |> Maybe.withDefault PasswordChange.empty
                    in
                    PasswordChange.answer (Tuple.second waiting) (Err (Graphql.Http.HttpError Graphql.Http.NetworkError)) later
                        |> Expect.equal (PasswordChange.Answered later)
            , test "still offers the credential when it accepted the change" <|
                \_ ->
                    -- The password changed whether or not anyone is still looking at the form,
                    -- and this is the one moment the new one exists in the client.
                    PasswordChange.answer (Tuple.second waiting) (Ok ()) PasswordChange.empty
                        |> Expect.equal (PasswordChange.Changed { id = "ada", password = "new horse" })
            ]
        , describe "a refusal of the Identity"
            [ test "says the Identity is stale rather than that the password was refused" <|
                \_ ->
                    -- Signed out elsewhere before the change arrived: bitmagnet resolved the
                    -- request to Anonymous, and changed nothing.
                    answered (Err (refused "USER_SESSION_REQUIRED"))
                        |> Expect.equal PasswordChange.IdentityStale
            ]
        , describe "on the wire"
            [ test "asks updatePassword for exactly the two passwords typed" <|
                \_ ->
                    Graphql.Document.serializeMutation (PasswordChange.mutation (typed "old horse" "new horse"))
                        |> String.contains "self {\n    updatePassword(input: {currentPassword: \"old horse\", newPassword: \"new horse\"})\n  }"
                        |> Expect.equal True
            , test "sends spaces around a password as typed" <|
                \_ ->
                    Graphql.Document.serializeMutation (PasswordChange.mutation (typed " old " " new "))
                        |> String.contains "updatePassword(input: {currentPassword: \" old \", newPassword: \" new \"})"
                        |> Expect.equal True
            ]
        , describe "sending"
            [ test "numbers the change, and keeps the credential it would leave the User with" <|
                \_ ->
                    -- The username is the signed-in User's, not anything typed: there is no
                    -- username field, and the credential belongs to whoever asked.
                    typed "old horse" "new horse"
                        |> PasswordChange.sending 7 "ada"
                        |> Maybe.map Tuple.second
                        |> Expect.equal
                            (Just { attempt = 7, credential = { id = "ada", password = "new horse" } })
            , test "sends nothing from a form that is not ready" <|
                \_ ->
                    typed "old horse" ""
                        |> PasswordChange.sending 1 "ada"
                        |> Expect.equal Nothing
            ]
        ]


offersCredential : PasswordChange.Outcome -> Bool
offersCredential outcome =
    case outcome of
        PasswordChange.Changed _ ->
            True

        _ ->
            False


shownForm : PasswordChange.Outcome -> Maybe PasswordChange.Form
shownForm outcome =
    case outcome of
        PasswordChange.Answered form ->
            Just form

        _ ->
            Nothing


renderedAnswer : PasswordChange.Outcome -> Query.Single ()
renderedAnswer =
    shownForm >> Maybe.withDefault PasswordChange.empty >> rendered
