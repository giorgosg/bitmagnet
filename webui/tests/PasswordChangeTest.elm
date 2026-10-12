module PasswordChangeTest exposing (suite)

import ApiError
import Dict
import Expect
import Graphql.Document
import Graphql.Http
import Graphql.Http.GraphqlError as GraphqlError
import Html.Attributes
import Identity
import Json.Decode as Decode
import Json.Encode as Encode
import Login
import PasswordChange
import PasswordEntropy
import Route
import Test exposing (Test, describe, test)
import Test.Html.Query as Query
import Test.Html.Selector as Selector
import Time


typed : String -> String -> PasswordChange.State
typed current new =
    PasswordChange.initial
        |> PasswordChange.withCurrent current
        |> PasswordChange.withNew new


messages : PasswordChange.Messages ()
messages =
    { currentChanged = always (), newChanged = always (), submitted = () }


rendered : PasswordChange.State -> Query.Single ()
rendered state =
    PasswordChange.view messages state |> Query.fromHtml


measured : Float -> Float -> PasswordEntropy.Entropy
measured entropy minEntropy =
    PasswordEntropy.Measured { entropy = entropy, minEntropy = minEntropy }


{-| The state while the first change is on its way, and that change. A form that would not
send stands in as nothing sent, numbered 0, which no test here expects.
-}
waiting : ( PasswordChange.State, PasswordChange.Sent )
waiting =
    PasswordChange.sending (signedInAs "ada") (typed "old horse" "new horse")
        |> Maybe.withDefault ( PasswordChange.initial, { attempt = 0, credential = { id = "", password = "" } } )


sent : PasswordChange.Sent
sent =
    Tuple.second waiting


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


{-| A status other than 2xx, with the body bitmagnet sent beside it.
-}
badStatus : Int -> String -> Graphql.Http.Error ()
badStatus status body =
    Graphql.Http.HttpError
        (Graphql.Http.BadStatus
            { url = "/graphql", statusCode = status, statusText = "", headers = Dict.empty }
            body
        )


lost : Graphql.Http.Error ()
lost =
    Graphql.Http.HttpError Graphql.Http.NetworkError


{-| The answer to the change `waiting` sent, landing on the state it left.
-}
answered : Result (Graphql.Http.Error ()) () -> ( PasswordChange.State, PasswordChange.Settlement )
answered result =
    PasswordChange.answer sent result (Tuple.first waiting)


{-| The state once the first change's answer was lost.
-}
unknown : PasswordChange.State
unknown =
    Tuple.first (answered (Err lost))


signedInAs : String -> Identity.Identity
signedInAs username =
    Identity.UserAuthenticated { ada | username = username } []


mount : Route.BasePath
mount =
    Route.basePath "/magnes"


{-| Where an unsettled change found signed out sends the browser: the login form, returning to
the User overview the change was made on.
-}
backToAccount : Route.Route
backToAccount =
    Route.Login { returnUrl = Just "/magnes/account" }


ada : Identity.User
ada =
    { id = 1
    , username = "ada"
    , role = "user"
    , email = Nothing
    , lastLoginAt = Nothing
    , createdAt = Time.millisToPosix 0
    , updatedAt = Time.millisToPosix 0
    }


anonymous : Identity.Identity
anonymous =
    Identity.Anonymous []


{-| The state once `unknown` found the browser signed out.
-}
signedOut : PasswordChange.State
signedOut =
    Tuple.first (PasswordChange.identified 1 anonymous unknown)


notMade : PasswordChange.Settlement
notMade =
    PasswordChange.NotMade { identityStale = False, offerWithdrawn = False }


alert : Query.Single () -> Query.Single ()
alert =
    Query.find [ Selector.attribute (Html.Attributes.attribute "role" "alert") ]


submitButton : Query.Single () -> Query.Single ()
submitButton =
    Query.find [ Selector.tag "button" ]


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
                    PasswordChange.canSubmit PasswordChange.initial
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
            ]
        , describe "sending"
            [ test "numbers the change, and keeps the credential it would leave the User with" <|
                \_ ->
                    -- The username is the signed-in User's, not anything typed: there is no
                    -- username field, and the credential belongs to whoever asked.
                    sent
                        |> Expect.equal { attempt = 1, credential = { id = "ada", password = "new horse" } }
            , test "numbers the next change after it" <|
                \_ ->
                    unknown
                        |> PasswordChange.identified 1 (Identity.UserAuthenticated { ada | username = "grace" } [])
                        |> Tuple.first
                        |> PasswordChange.withCurrent "old horse"
                        |> PasswordChange.withNew "newer horse"
                        |> PasswordChange.sending (signedInAs "grace")
                        |> Maybe.map (Tuple.second >> .attempt)
                        |> Expect.equal (Just 2)
            , test "sends nothing from a form that is not ready" <|
                \_ ->
                    typed "old horse" ""
                        |> PasswordChange.sending (signedInAs "ada")
                        |> Expect.equal Nothing
            , test "sends nothing for an Identity that is not a signed-in User" <|
                \_ ->
                    -- The credential a change leaves behind is the signed-in User's, and
                    -- bitmagnet refuses a change to every other Identity anyway.
                    [ anonymous
                    , Identity.APIKeyAuthenticated ada "a key" []
                    , Identity.Unknown
                    , Identity.Failed "offline"
                    ]
                        |> List.map (\identity -> PasswordChange.sending identity (typed "old horse" "new horse"))
                        |> Expect.equal [ Nothing, Nothing, Nothing, Nothing ]
            , test "sends no second change while the first is on its way" <|
                \_ ->
                    Tuple.first waiting
                        |> PasswordChange.withNew "newer horse"
                        |> PasswordChange.sending (signedInAs "ada")
                        |> Expect.equal Nothing
            , test "sends no second change while the first is unsettled" <|
                \_ ->
                    -- Two changes whose outcomes are unknown would leave no telling which new
                    -- password bitmagnet holds.
                    unknown
                        |> PasswordChange.withCurrent "old horse"
                        |> PasswordChange.withNew "newer horse"
                        |> PasswordChange.sending (signedInAs "ada")
                        |> Expect.equal Nothing
            , test "a change on its way says so, and offers no second one" <|
                \_ ->
                    rendered (Tuple.first waiting)
                        |> submitButton
                        |> Query.has [ Selector.text "Changing…", Selector.disabled True ]
            , test "a change on its way says how it will be settled, coming back to the page included" <|
                \_ ->
                    rendered (PasswordChange.left (Tuple.first waiting))
                        |> Query.has
                            [ Selector.text "Waiting for bitmagnet's answer to the change. Another cannot be sent until it comes; if none has come within five minutes, Magnes stops waiting and says what it knows." ]
            , test "an unsettled change says why no other can be sent, and how it is settled" <|
                \_ ->
                    rendered (PasswordChange.withNew "newer horse" (PasswordChange.withCurrent "x" unknown))
                        |> Expect.all
                            [ submitButton >> Query.has [ Selector.disabled True ]
                            , Query.has [ Selector.text "Another change cannot be sent until this one is settled, which signing in again does: if you are still signed in, sign out first." ]
                            ]
            ]
        , describe "before anything is sent"
            [ test "says that every device is signed out, this one included" <|
                \_ ->
                    rendered PasswordChange.initial
                        |> Query.has
                            [ Selector.text "Changing your password signs you out everywhere: every device and browser signed in as you, this one included. You then sign in again with the new password." ]
            , test "says that API keys keep working" <|
                \_ ->
                    -- bitmagnet's revocation is of what a password opened. A key is a credential
                    -- of its own, and someone rotating a leaked password needs to know that
                    -- a leaked key is not dealt with by this.
                    rendered PasswordChange.initial
                        |> Query.has
                            [ Selector.text "Your API keys are not affected and keep working. Revoke any you no longer trust on the API keys page." ]
            ]
        , describe "the entropy meter"
            [ test "a new password abandons the measurement of the old one" <|
                \_ ->
                    typed "old horse" "new horse"
                        |> PasswordChange.measured (measured 80 70)
                        |> PasswordChange.withNew "newer horse"
                        |> .form
                        |> .entropy
                        |> Expect.equal PasswordEntropy.Measuring
            , test "the current password is never measured" <|
                \_ ->
                    PasswordChange.initial
                        |> PasswordChange.withCurrent "old horse"
                        |> .form
                        |> .entropy
                        |> Expect.equal PasswordEntropy.Unmeasured
            , test "a measurement lands only on a form waiting for one" <|
                \_ ->
                    -- A form dropped while a measurement was on its way, by leaving the page or
                    -- an Identity change, must not grow a meter under a blank field.
                    typed "old horse" "new horse"
                        |> PasswordChange.left
                        |> PasswordChange.measured (measured 80 70)
                        |> .form
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
            , test "the new password typed is the one measured" <|
                \_ ->
                    PasswordChange.newPassword (typed "old horse" "new horse")
                        |> Expect.equal "new horse"
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
        , describe "an accepted change"
            [ test "offers the credential it leaves the User with" <|
                \_ ->
                    answered (Ok ())
                        |> Tuple.second
                        |> Expect.equal (PasswordChange.Made { id = "ada", password = "new horse" })
            , test "is settled, and takes the form with it" <|
                \_ ->
                    answered (Ok ())
                        |> Tuple.first
                        |> Expect.all
                            [ .unsettled >> Expect.equal Nothing
                            , .form >> Expect.equal PasswordChange.initial.form
                            ]
            , test "offers what was sent, not what the form holds now" <|
                \_ ->
                    -- The person may have typed on while the change was on its way; the password
                    -- bitmagnet accepted is the one that was sent.
                    PasswordChange.answer sent (Ok ()) (PasswordChange.withNew "typed afterwards" (Tuple.first waiting))
                        |> Tuple.second
                        |> Expect.equal (PasswordChange.Made { id = "ada", password = "new horse" })
            , test "is acted on even once nothing is waiting for it" <|
                \_ ->
                    -- The password changed whether or not anyone is still looking, and this is
                    -- the one moment the new one is known to be accepted.
                    PasswordChange.answer sent (Ok ()) PasswordChange.initial
                        |> Tuple.second
                        |> Expect.equal (PasswordChange.Made { id = "ada", password = "new horse" })
            , test "is the only answer that offers a credential" <|
                \_ ->
                    [ refused "PASSWORD_INCORRECT"
                    , refused "PASSWORD_INSUFFICIENT_ENTROPY"
                    , refused "USER_SESSION_REQUIRED"
                    , refused "INTERNAL_SERVER_ERROR"
                    , badStatus 403 """{"errors":[{"message":"browser session request must be same-origin"}],"data":null}"""
                    , badStatus 422 """{"errors":[{"message":"Cannot query field","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}],"data":null}"""
                    , badStatus 502 ""
                    , lost
                    , Graphql.Http.HttpError Graphql.Http.Timeout
                    ]
                        |> List.map (Err >> answered >> Tuple.second)
                        |> List.filter offersCredential
                        |> Expect.equal []
            ]
        , describe "a refusal"
            [ test "settles the change as not made" <|
                \_ ->
                    answered (Err (refused "PASSWORD_INCORRECT"))
                        |> Expect.all
                            [ Tuple.second >> Expect.equal notMade
                            , Tuple.first >> .unsettled >> Expect.equal Nothing
                            ]
            , test "a wrong current password is marked on the current password" <|
                \_ ->
                    answered (Err (refused "PASSWORD_INCORRECT"))
                        |> Tuple.first
                        |> rendered
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
                        |> Tuple.first
                        |> rendered
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
                    answered (Err (badStatus 403 """{"errors":[{"message":"browser session request must be same-origin"}],"data":null}"""))
                        |> Tuple.first
                        |> rendered
                        |> Expect.all
                            [ Query.findAll [ Selector.attribute (Html.Attributes.attribute "aria-invalid" "true") ]
                                >> Query.count (Expect.equal 0)
                            , alert
                                >> Query.has
                                    [ Selector.text
                                        ("Your password was not changed. "
                                            ++ ApiError.toMessage (ApiError.Forbidden "browser session request must be same-origin")
                                        )
                                    ]
                            ]
            , test "a request turned away before anything ran is a refusal, not an unknown" <|
                \_ ->
                    -- gqlgen's 422 for a document it cannot validate, and bitmagnet's 403 for a
                    -- mutation from another origin, both arrive as a bad status.
                    [ badStatus 422 """{"errors":[{"message":"Cannot query field","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}],"data":null}"""
                    , badStatus 422 """{"errors":[{"message":"Expected Name","extensions":{"code":"GRAPHQL_PARSE_FAILED"}}],"data":null}"""
                    , badStatus 403 """{"errors":[{"message":"browser session request requires an Origin"}],"data":null}"""
                    ]
                        |> List.map (Err >> answered >> Tuple.second)
                        |> Expect.equal [ notMade, notMade, notMade ]
            , test "can be answered by trying again" <|
                \_ ->
                    answered (Err (refused "PASSWORD_INCORRECT"))
                        |> Tuple.first
                        |> PasswordChange.canSubmit
                        |> Expect.equal True
            , test "typing dismisses the refusal it no longer describes" <|
                \_ ->
                    answered (Err (refused "PASSWORD_INCORRECT"))
                        |> Tuple.first
                        |> PasswordChange.withCurrent "old horse, again"
                        |> .form
                        |> .refusal
                        |> Expect.equal Nothing
            , test "a refusal of the Identity says it is stale, and shows nothing on the form" <|
                \_ ->
                    -- Signed out elsewhere before the change arrived: bitmagnet resolved the
                    -- request to Anonymous, and changed nothing.
                    answered (Err (refused "USER_SESSION_REQUIRED"))
                        |> Expect.all
                            [ Tuple.second >> Expect.equal (PasswordChange.NotMade { identityStale = True, offerWithdrawn = False })
                            , Tuple.first >> .unsettled >> Expect.equal Nothing
                            , Tuple.first >> .form >> .refusal >> Expect.equal Nothing
                            ]
            ]
        , describe "no usable answer"
            [ test "leaves the change unsettled rather than refused" <|
                \_ ->
                    -- The request may have reached bitmagnet and committed before the answer
                    -- was lost. Saying it failed would send the person back to a password that
                    -- may no longer work.
                    answered (Err lost)
                        |> Expect.all
                            [ Tuple.second >> Expect.equal PasswordChange.NotSettled
                            , Tuple.first >> .unsettled >> Expect.equal (Just (PasswordChange.Lost sent ApiError.Unreachable))
                            ]
            , test "a fault inside bitmagnet is no answer either" <|
                \_ ->
                    -- A connection lost during COMMIT gives INTERNAL_SERVER_ERROR, though
                    -- Postgres may have committed.
                    answered (Err (refused "INTERNAL_SERVER_ERROR"))
                        |> Tuple.first
                        |> .unsettled
                        |> Expect.equal (Just (PasswordChange.Lost sent ApiError.ServerFault))
            , test "nor is a bad gateway, an unreadable answer, or none in time" <|
                \_ ->
                    [ badStatus 502 ""
                    , Graphql.Http.HttpError (Graphql.Http.BadPayload (Decode.Failure "not bitmagnet's" Encode.null))
                    , Graphql.Http.HttpError Graphql.Http.Timeout
                    ]
                        |> List.map (Err >> answered >> Tuple.second)
                        |> Expect.equal [ PasswordChange.NotSettled, PasswordChange.NotSettled, PasswordChange.NotSettled ]
            , test "says the password may have changed, which one signs in if it did, and that Magnes keeps it" <|
                \_ ->
                    rendered unknown
                        |> alert
                        |> Query.has
                            [ Selector.text "No usable answer to the change came back."
                            , Selector.text (ApiError.toMessage ApiError.Unreachable)
                            , Selector.text "Whether your password was changed is not known."
                            , Selector.text "If it was, you are signed out everywhere and only the new password signs in. Magnes keeps it until this is settled, and the login form will offer it first."
                            ]
            , test "does not claim the password was not changed" <|
                \_ ->
                    rendered unknown
                        |> Query.hasNot [ Selector.text "was not changed" ]
            , test "blames neither field" <|
                \_ ->
                    rendered unknown
                        |> Query.findAll [ Selector.attribute (Html.Attributes.attribute "aria-invalid" "true") ]
                        |> Query.count (Expect.equal 0)
            ]
        , describe "leaving the page"
            [ test "drops what is typed" <|
                \_ ->
                    PasswordChange.left (typed "old horse" "new horse")
                        |> Expect.equal PasswordChange.initial
            , test "keeps a change on its way, and its new password" <|
                \_ ->
                    PasswordChange.left (Tuple.first waiting)
                        |> .unsettled
                        |> Expect.equal (Just (PasswordChange.Awaiting sent))
            , test "keeps an unsettled change, and still says so on coming back" <|
                \_ ->
                    rendered (PasswordChange.left unknown)
                        |> alert
                        |> Query.has [ Selector.text "Whether your password was changed is not known." ]
            , test "does not stop an answer settling the change it answers" <|
                \_ ->
                    PasswordChange.answer sent (Err lost) (PasswordChange.left (Tuple.first waiting))
                        |> Tuple.first
                        |> .unsettled
                        |> Expect.equal (Just (PasswordChange.Lost sent ApiError.Unreachable))
            ]
        , describe "the Identity found signed out"
            [ test "while a change is unsettled, offers its new password at sign-in" <|
                \_ ->
                    PasswordChange.identified 1 anonymous unknown
                        |> Expect.all
                            [ Tuple.second >> Expect.equal (Just { id = "ada", password = "new horse" })
                            , Tuple.first >> .unsettled >> Expect.equal (Just (PasswordChange.SignedOut sent))
                            ]
            , test "while a change is still on its way, offers it too" <|
                \_ ->
                    PasswordChange.identified 1 anonymous (Tuple.first waiting)
                        |> Tuple.second
                        |> Expect.equal (Just { id = "ada", password = "new horse" })
            , test "an API-key Identity is not the User who sent it either" <|
                \_ ->
                    PasswordChange.identified 1 (Identity.APIKeyAuthenticated ada "a key" []) unknown
                        |> Tuple.second
                        |> Expect.equal (Just { id = "ada", password = "new horse" })
            , test "offers it once, not at every look that follows" <|
                \_ ->
                    PasswordChange.identified 2 anonymous signedOut
                        |> Tuple.second
                        |> Expect.equal Nothing
            , test "keeps offering it to the login form until it is settled" <|
                \_ ->
                    PasswordChange.offered signedOut
                        |> Expect.equal (Just { id = "ada", password = "new horse" })
            , test "offers nothing to the login form while the User is still signed in" <|
                \_ ->
                    PasswordChange.offered unknown
                        |> Expect.equal Nothing
            , test "with nothing unsettled, offers nothing" <|
                \_ ->
                    PasswordChange.identified 1 anonymous (typed "old horse" "new horse")
                        |> Tuple.second
                        |> Expect.equal Nothing
            , test "an Identity still being asked for says nothing" <|
                \_ ->
                    [ Identity.Unknown, Identity.Failed "offline" ]
                        |> List.map (\identity -> PasswordChange.identified 1 identity unknown |> Tuple.mapFirst .unsettled)
                        |> Expect.equal [ ( unknown.unsettled, Nothing ), ( unknown.unsettled, Nothing ) ]
            ]
        , describe "the Identity found signed in"
            [ test "as the User who sent it, keeps the change unsettled" <|
                \_ ->
                    -- Still signed in says only that it had not committed when bitmagnet was
                    -- asked; an answer lost on the way may yet be a change that commits.
                    PasswordChange.identified 1 (Identity.UserAuthenticated ada []) unknown
                        |> Tuple.mapFirst .unsettled
                        |> Expect.equal ( unknown.unsettled, Nothing )
            , test "again, after being found signed out, settles it" <|
                \_ ->
                    -- A sign-in succeeded since, with the new password or the old one, so there
                    -- is a password that works, and the person knows which.
                    PasswordChange.identified 2 (Identity.UserAuthenticated ada []) signedOut
                        |> Tuple.first
                        |> .unsettled
                        |> Expect.equal Nothing
            , test "as the User who sent it, but asked before it was found signed out, says nothing" <|
                \_ ->
                    -- Two looks at the Identity can answer out of order. One asked before the
                    -- change committed says signed in; landing after the look that found it
                    -- signed out, it must not pass for a sign-in since, which would drop the
                    -- change and the only copy of its new password.
                    PasswordChange.identified 1 (Identity.UserAuthenticated ada []) (Tuple.first (PasswordChange.identified 2 anonymous unknown))
                        |> Tuple.first
                        |> PasswordChange.offered
                        |> Expect.equal (Just { id = "ada", password = "new horse" })
            , test "an older look landing late does not find it signed out either" <|
                \_ ->
                    PasswordChange.identified 1 anonymous (Tuple.first (PasswordChange.identified 2 (Identity.UserAuthenticated ada []) unknown))
                        |> Expect.all
                            [ Tuple.second >> Expect.equal Nothing
                            , Tuple.first >> PasswordChange.offered >> Expect.equal Nothing
                            ]
            , test "as another User, drops it: it is not this Identity's to settle" <|
                \_ ->
                    -- A deliberate trade. Kept, its new password would stay in memory while
                    -- someone else uses the tab, and could be offered to them, or to whoever
                    -- comes next, on a login form. Dropped, the person who made the change loses
                    -- Magnes's copy of a password that may now be the only one that works.
                    PasswordChange.identified 2 (signedInAs "grace") signedOut
                        |> Tuple.first
                        |> .unsettled
                        |> Expect.equal Nothing
            , test "as another User while still unsettled, drops it too" <|
                \_ ->
                    PasswordChange.identified 1 (Identity.UserAuthenticated { ada | username = "grace" } []) unknown
                        |> Tuple.first
                        |> .unsettled
                        |> Expect.equal Nothing
            ]
        , describe "an answer once signed out"
            [ test "an acceptance still offers the credential" <|
                \_ ->
                    PasswordChange.answer sent (Ok ()) (Tuple.first (PasswordChange.identified 1 anonymous (Tuple.first waiting)))
                        |> Tuple.second
                        |> Expect.equal (PasswordChange.Made { id = "ada", password = "new horse" })
            , test "a refusal withdraws the offer the login form was given" <|
                \_ ->
                    PasswordChange.answer sent (Err (refused "USER_SESSION_REQUIRED")) (Tuple.first (PasswordChange.identified 1 anonymous (Tuple.first waiting)))
                        |> Expect.all
                            [ Tuple.second >> Expect.equal (PasswordChange.NotMade { identityStale = True, offerWithdrawn = True })
                            , Tuple.first >> PasswordChange.offered >> Expect.equal Nothing
                            ]
            , test "no usable answer keeps offering it" <|
                \_ ->
                    PasswordChange.answer sent (Err lost) (Tuple.first (PasswordChange.identified 1 anonymous (Tuple.first waiting)))
                        |> Tuple.first
                        |> PasswordChange.offered
                        |> Expect.equal (Just { id = "ada", password = "new horse" })
            ]
        , describe "an answer to a change already settled"
            [ test "an acceptance of an older change leaves a newer one unsettled" <|
                \_ ->
                    -- The older one was dropped by a sign-in, a newer one sent since, and then
                    -- the older one's acceptance arrives. It changed the password, so it is
                    -- acted on; the newer one is still waiting for its own answer.
                    let
                        newer =
                            signedOut
                                |> PasswordChange.signedIn
                                |> PasswordChange.withCurrent "new horse"
                                |> PasswordChange.withNew "newer horse"
                                |> PasswordChange.sending (signedInAs "ada")
                                |> Maybe.map Tuple.first
                                |> Maybe.withDefault PasswordChange.initial
                    in
                    PasswordChange.answer sent (Ok ()) newer
                        |> Expect.all
                            [ Tuple.second >> Expect.equal (PasswordChange.Made { id = "ada", password = "new horse" })
                            , Tuple.first >> Expect.equal newer
                            ]
            , test "a refusal changes nothing, though it says what it was" <|
                \_ ->
                    PasswordChange.answer sent (Err (refused "PASSWORD_INCORRECT")) (typed "old horse" "typed since")
                        |> Expect.equal ( typed "old horse" "typed since", notMade )
            , test "a refusal of the Identity still says it is stale" <|
                \_ ->
                    PasswordChange.answer sent (Err (refused "USER_SESSION_REQUIRED")) PasswordChange.initial
                        |> Tuple.second
                        |> Expect.equal (PasswordChange.NotMade { identityStale = True, offerWithdrawn = False })
            , test "no usable answer changes nothing" <|
                \_ ->
                    PasswordChange.answer sent (Err lost) (typed "old horse" "typed since")
                        |> Expect.equal ( typed "old horse" "typed since", PasswordChange.NotSettled )
            ]
        , describe "a sign-in in this tab"
            [ test "settles a change found signed out" <|
                \_ ->
                    -- Whichever password it used, one now works and the person knows which.
                    PasswordChange.signedIn signedOut
                        |> PasswordChange.offered
                        |> Expect.equal Nothing
            , test "keeps the form as it was" <|
                \_ ->
                    PasswordChange.signedIn (typed "old horse" "new horse")
                        |> Expect.equal (typed "old horse" "new horse")
            ]
        , describe "what an answer to the Identity means"
            [ test "found signed out with a change unsettled, the login form tries its new password, returning to the User overview" <|
                \_ ->
                    PasswordChange.identityAnswered mount 1 anonymous unknown Login.empty
                        |> Expect.all
                            [ .goTo >> Expect.equal (Just backToAccount)
                            , .login >> Expect.equal (Login.afterLostChange { id = "ada", password = "new horse" })
                            , .change >> PasswordChange.offered >> Expect.equal (Just { id = "ada", password = "new horse" })
                            ]
            , test "an older look landing late changes nothing, and goes nowhere" <|
                \_ ->
                    -- The one that found the browser signed out was asked later, and has spoken.
                    let
                        found =
                            PasswordChange.identityAnswered mount 2 anonymous unknown Login.empty
                    in
                    PasswordChange.identityAnswered mount 1 (Identity.UserAuthenticated ada []) found.change found.login
                        |> Expect.equal { change = found.change, login = found.login, goTo = Nothing }
            , test "found signed out a second time, goes nowhere and leaves the form alone" <|
                \_ ->
                    PasswordChange.identityAnswered mount 2 anonymous signedOut (Login.withPassword "typed" Login.empty)
                        |> Expect.equal { change = Tuple.first (PasswordChange.identified 2 anonymous signedOut), login = Login.withPassword "typed" Login.empty, goTo = Nothing }
            , test "with nothing unsettled, goes nowhere and leaves the form alone" <|
                \_ ->
                    PasswordChange.identityAnswered mount 1 anonymous PasswordChange.initial (Login.withUsername "carol" Login.empty)
                        |> Expect.all
                            [ .goTo >> Expect.equal Nothing
                            , .login >> Expect.equal (Login.withUsername "carol" Login.empty)
                            ]
            , test "never fills in a form that names another User" <|
                \_ ->
                    PasswordChange.identityAnswered mount 1 anonymous unknown (Login.withUsername "carol" Login.empty)
                        |> .login
                        |> Expect.equal (Login.withUsername "carol" Login.empty)
            ]
        , describe "the login form on arriving there"
            [ test "sent there by a page that needs a User, tries the change's new password" <|
                \_ ->
                    PasswordChange.loginOnArrival mount anonymous Route.UserOverview signedOut Login.empty
                        |> Expect.equal (Login.afterLostChange { id = "ada", password = "new horse" })
            , test "on the change's own User's name, fills the password in too" <|
                \_ ->
                    PasswordChange.loginOnArrival mount anonymous Route.APIKeys signedOut (Login.withUsername "ada" Login.empty)
                        |> Expect.equal (Login.afterLostChange { id = "ada", password = "new horse" })
            , test "arriving from registration keeps the new User the form was opened on" <|
                \_ ->
                    -- Someone registered another User in this tab. The form names them; filling
                    -- in the change's User would sign the wrong person in on Enter.
                    PasswordChange.loginOnArrival mount anonymous (Route.Register { code = Nothing }) signedOut (Login.withUsername "carol" Login.empty)
                        |> Expect.equal (Login.withUsername "carol" Login.empty)
            , test "sent there by the guard, still never fills in a form that names another User" <|
                \_ ->
                    PasswordChange.loginOnArrival mount anonymous Route.UserOverview signedOut (Login.withUsername "carol" Login.empty)
                        |> Expect.equal (Login.withUsername "carol" Login.empty)
            , test "arriving of the person's own accord, from a page that needs no User, keeps the form" <|
                \_ ->
                    -- The header's "Sign in" link is open to whoever is at the keyboard.
                    PasswordChange.loginOnArrival mount anonymous (Route.Search Route.emptySearch) signedOut Login.empty
                        |> Expect.equal Login.empty
            , test "with no change found signed out, keeps the form" <|
                \_ ->
                    PasswordChange.loginOnArrival mount anonymous Route.UserOverview unknown Login.empty
                        |> Expect.equal Login.empty
            ]
        , describe "what an answer to a change means"
            [ test "accepted on the User overview: the credential is offered, and the login form says why, returning there" <|
                \_ ->
                    PasswordChange.answered mount sent (Ok ()) (Tuple.first waiting) Login.empty
                        |> Expect.all
                            [ .offer >> Expect.equal (Just { id = "ada", password = "new horse" })
                            , .goTo >> Expect.equal (Just backToAccount)
                            , .signIn >> Expect.equal PasswordChange.Ended
                            , .login >> Expect.equal (Login.afterPasswordChange "ada")
                            ]
            , test "accepted while the login form tries its new password: the form keeps it, and says it worked" <|
                \_ ->
                    let
                        trying =
                            Login.afterLostChange { id = "ada", password = "new horse" }
                                |> Login.withState Login.Submitting
                    in
                    PasswordChange.answered mount sent (Ok ()) (Tuple.first (PasswordChange.identified 1 anonymous (Tuple.first waiting))) trying
                        |> Expect.all
                            [ .login >> Expect.equal (Login.changeConfirmed trying)
                            , .login >> .password >> Expect.equal "new horse"
                            , .offer >> Expect.equal (Just { id = "ada", password = "new horse" })
                            ]
            , test "the offer is never empty, nor any password but the one accepted" <|
                \_ ->
                    -- Whatever the login form holds by the time the answer lands.
                    [ Login.empty
                    , Login.withPassword "old horse" (Login.afterLostChange { id = "ada", password = "new horse" })
                    , Login.withPassword "" (Login.afterLostChange { id = "ada", password = "new horse" })
                    ]
                        |> List.map (PasswordChange.answered mount sent (Ok ()) (Tuple.first waiting) >> .offer)
                        |> Expect.equal (List.repeat 3 (Just { id = "ada", password = "new horse" }))
            , test "refused: nothing offered, nowhere to go, and the sign-in unaffected" <|
                \_ ->
                    PasswordChange.answered mount sent (Err (refused "PASSWORD_INCORRECT")) (Tuple.first waiting) Login.empty
                        |> Expect.all
                            [ .offer >> Expect.equal Nothing
                            , .goTo >> Expect.equal Nothing
                            , .signIn >> Expect.equal PasswordChange.Unaffected
                            , .change >> .form >> .refusal >> Expect.equal (Just ApiError.PasswordIncorrect)
                            ]
            , test "a refusal of the Identity asks for it again" <|
                \_ ->
                    PasswordChange.answered mount sent (Err (refused "USER_SESSION_REQUIRED")) (Tuple.first waiting) Login.empty
                        |> .signIn
                        |> Expect.equal PasswordChange.Stale
            , test "refused once the login form was offered its password: the offer is withdrawn" <|
                \_ ->
                    PasswordChange.answered mount sent (Err (refused "USER_SESSION_REQUIRED")) (Tuple.first (PasswordChange.identified 1 anonymous (Tuple.first waiting))) (Login.afterLostChange { id = "ada", password = "new horse" })
                        |> .login
                        |> Expect.all
                            [ .reason >> Expect.equal Nothing
                            , .password >> Expect.equal ""
                            , .username >> Expect.equal "ada"
                            ]
            , test "a withdrawn offer keeps a password the person typed, and a sign-in on its way" <|
                \_ ->
                    -- They may be signing in with the old password as the refusal lands.
                    let
                        typedOver =
                            Login.afterLostChange { id = "ada", password = "new horse" }
                                |> Login.withPassword "old horse"
                                |> Login.withState Login.Submitting
                    in
                    PasswordChange.answered mount sent (Err (refused "USER_SESSION_REQUIRED")) (Tuple.first (PasswordChange.identified 1 anonymous (Tuple.first waiting))) typedOver
                        |> .login
                        |> Expect.all
                            [ .password >> Expect.equal "old horse"
                            , .state >> Expect.equal Login.Submitting
                            ]
            , test "no usable answer: nothing offered, nowhere to go, the form untouched" <|
                \_ ->
                    PasswordChange.answered mount sent (Err lost) (Tuple.first waiting) (Login.withUsername "carol" Login.empty)
                        |> Expect.all
                            [ .offer >> Expect.equal Nothing
                            , .goTo >> Expect.equal Nothing
                            , .signIn >> Expect.equal PasswordChange.Unaffected
                            , .login >> Expect.equal (Login.withUsername "carol" Login.empty)
                            ]
            ]
        ]


offersCredential : PasswordChange.Settlement -> Bool
offersCredential settlement =
    case settlement of
        PasswordChange.Made _ ->
            True

        PasswordChange.NotMade _ ->
            False

        PasswordChange.NotSettled ->
            False
