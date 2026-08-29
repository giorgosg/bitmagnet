module RegisterTest exposing (suite)

import ApiError
import Expect
import Html.Attributes
import Register
import Test exposing (Test, describe, test)
import Test.Html.Query as Query
import Test.Html.Selector as Selector


filled : Register.Form
filled =
    Register.empty
        |> Register.withUsername "ada"
        |> Register.withPassword "correct horse battery staple"


messages : Register.Messages ()
messages =
    { usernameChanged = always ()
    , passwordChanged = always ()
    , invitationCodeChanged = always ()
    , submitted = ()
    }


rendered : Register.Form -> Query.Single ()
rendered form =
    Register.view messages form |> Query.fromHtml


measured : Float -> Float -> Register.Strength
measured entropy minEntropy =
    { entropy = entropy, minEntropy = minEntropy }


suite : Test
suite =
    describe "Register"
        [ describe "canSubmit"
            [ test "a username and a password are enough" <|
                \_ ->
                    Register.canSubmit filled
                        |> Expect.equal True
            , test "an empty form is not ready" <|
                \_ ->
                    Register.canSubmit Register.empty
                        |> Expect.equal False
            , test "a username of only spaces is not a username" <|
                \_ ->
                    Register.canSubmit (Register.withUsername "   " filled)
                        |> Expect.equal False
            , test "a request in flight is not sent twice" <|
                \_ ->
                    Register.withState Register.Submitting filled
                        |> Register.canSubmit
                        |> Expect.equal False
            , test "a password the meter calls too weak is still offered to the server, which decides" <|
                \_ ->
                    -- The meter is advice from a query that may be stale or may have
                    -- failed. Refusing to submit on it would make a broken query look
                    -- like a rejected password.
                    filled
                        |> Register.withEntropy (Register.Measured (measured 12 70))
                        |> Register.canSubmit
                        |> Expect.equal True
            ]
        , describe "the Invitation"
            [ test "arrives pre-filled from the route" <|
                \_ ->
                    Register.prefilled (Just "abc123")
                        |> Register.invitationCode
                        |> Expect.equal (Just "abc123")
            , test "is absent rather than empty when the route carries no code" <|
                \_ ->
                    Register.prefilled Nothing
                        |> Register.invitationCode
                        |> Expect.equal Nothing
            , test "is shown in the form so it can be seen and corrected" <|
                \_ ->
                    rendered (Register.prefilled (Just "abc123"))
                        |> Query.has
                            [ Selector.attribute (Html.Attributes.value "abc123") ]
            ]
        , describe "editing"
            [ test "typing dismisses the rejection it no longer describes" <|
                \_ ->
                    filled
                        |> Register.withState (Register.Rejected ApiError.UserAlreadyExists)
                        |> Register.withUsername "grace"
                        |> Register.state
                        |> Expect.equal Register.Ready
            , test "a new password abandons the measurement of the old one" <|
                \_ ->
                    filled
                        |> Register.withEntropy (Register.Measured (measured 80 70))
                        |> Register.withPassword "something else"
                        |> Register.entropy
                        |> Expect.equal Register.Measuring
            , test "clearing the password leaves nothing to measure" <|
                \_ ->
                    filled
                        |> Register.withEntropy (Register.Measured (measured 80 70))
                        |> Register.withPassword ""
                        |> Register.entropy
                        |> Expect.equal Register.Unmeasured
            ]
        , describe "the entropy meter"
            [ test "reports the measurement against the minimum the server named" <|
                \_ ->
                    rendered (Register.withEntropy (Register.Measured (measured 42 70)) filled)
                        |> Query.has [ Selector.text "42 of 70" ]
            , test "draws a progressbar a screen reader can read" <|
                \_ ->
                    rendered (Register.withEntropy (Register.Measured (measured 42 70)) filled)
                        |> Query.has [ Selector.tag "progress" ]
            , test "says nothing at all before a password is typed" <|
                \_ ->
                    rendered Register.empty
                        |> Query.hasNot [ Selector.tag "progress" ]
            , test "a failed measurement is not drawn as a weak password" <|
                \_ ->
                    -- Zero of seventy is what an unanswered query would look like on a
                    -- meter, and it is a lie about the password.
                    rendered (Register.withEntropy Register.Unmeasurable filled)
                        |> Query.hasNot [ Selector.tag "progress" ]
            , test "a measurement in flight says so rather than reading as weak" <|
                \_ ->
                    rendered (Register.withEntropy Register.Measuring filled)
                        |> Query.has [ Selector.text "Measuring…" ]
            , test "a measurement in flight claims no reading of its own" <|
                \_ ->
                    -- An indeterminate progress element carries no value attribute; one
                    -- that did would be a number nobody measured.
                    rendered (Register.withEntropy Register.Measuring filled)
                        |> Query.find [ Selector.tag "progress" ]
                        |> Query.hasNot [ Selector.attribute (Html.Attributes.value "0") ]
            ]
        , describe "collecting no email"
            -- bitmagnet's email verification is inert, so the spec defers email
            -- entirely: Magnes neither shows nor collects an address in this phase.
            [ test "renders no email field" <|
                \_ ->
                    rendered filled
                        |> Query.findAll
                            [ Selector.attribute (Html.Attributes.type_ "email") ]
                        |> Query.count (Expect.equal 0)
            ]
        , describe "rejection"
            [ test "marks only the field it is about" <|
                \_ ->
                    -- An expired Invitation says nothing about the username.
                    rendered (Register.withState (Register.Rejected ApiError.InvitationExpired) filled)
                        |> Expect.all
                            [ Query.find [ Selector.id "register-invitation" ]
                                >> Query.has
                                    [ Selector.attribute (Html.Attributes.attribute "aria-invalid" "true") ]
                            , Query.find [ Selector.id "register-username" ]
                                >> Query.has
                                    [ Selector.attribute (Html.Attributes.attribute "aria-invalid" "false") ]
                            ]
            , test "a weak password is marked on the password" <|
                \_ ->
                    rendered (Register.withState (Register.Rejected ApiError.PasswordInsufficientEntropy) filled)
                        |> Query.find [ Selector.id "register-password" ]
                        |> Query.has
                            [ Selector.attribute (Html.Attributes.attribute "aria-invalid" "true") ]
            , test "a taken username is marked on the username" <|
                \_ ->
                    rendered (Register.withState (Register.Rejected ApiError.UserAlreadyExists) filled)
                        |> Query.find [ Selector.id "register-username" ]
                        |> Query.has
                            [ Selector.attribute (Html.Attributes.attribute "aria-invalid" "true") ]
            , test "a failure Magnes cannot place is still put somewhere it will be read" <|
                \_ ->
                    rendered (Register.withState (Register.Rejected ApiError.Unreachable) filled)
                        |> Query.find [ Selector.id "register-username" ]
                        |> Query.has
                            [ Selector.attribute (Html.Attributes.attribute "aria-invalid" "true") ]
            , test "is announced beside the fields" <|
                \_ ->
                    rendered (Register.withState (Register.Rejected ApiError.UserAlreadyExists) filled)
                        |> Query.has
                            [ Selector.attribute (Html.Attributes.attribute "role" "alert")
                            , Selector.text (ApiError.toMessage ApiError.UserAlreadyExists)
                            ]
            , test "keeps each documented outcome distinguishable" <|
                \_ ->
                    [ ApiError.UserAlreadyExists
                    , ApiError.UsernameInvalid
                    , ApiError.InvitationRequired
                    , ApiError.InvitationInvalid
                    , ApiError.InvitationExpired
                    , ApiError.InvitationClaimed
                    , ApiError.PasswordInsufficientEntropy
                    ]
                        |> List.map ApiError.toMessage
                        |> List.map String.toLower
                        |> (\messageList ->
                                Expect.equal
                                    (List.length messageList)
                                    (List.length (unique messageList))
                           )
            ]
        ]


unique : List String -> List String
unique values =
    List.foldl
        (\value seen ->
            if List.member value seen then
                seen

            else
                seen ++ [ value ]
        )
        []
        values
