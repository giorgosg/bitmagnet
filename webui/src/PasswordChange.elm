module PasswordChange exposing (Answered, Form, Messages, Sent, Settlement(..), SignIn(..), State, Unsettled(..), answer, answered, canSubmit, identified, identityAnswered, initial, left, loginOnArrival, measured, mutation, newPassword, offered, sending, signedIn, submit, view, withCurrent, withNew)

{-| A signed-in User changing their own password, on the User overview.

`self.updatePassword` (bitmagnet `77f3fd9e3`) checks the current password, holds the new
one to the instance's minimum entropy, and ends every sign-in the User has, this one
included: no credential is re-issued, so success leaves the browser Anonymous. The form
says so before anything is sent. What follows a success is `Main`'s: offering the new
password to the browser's credential store, telling the other tabs, asking for the Identity
again, and landing on the login form with the reason.

**An unsettled change outlives the form.** A change is unsettled from the moment it is sent
until bitmagnet's answer settles it, or, when that answer is lost, until a sign-in does. Its
new password is kept with it, beside the form rather than in it, because leaving the page
and an Identity refresh both drop the form, and those are exactly what follows a change that
went through. Nothing else may be sent while one is unsettled: two changes of unknown outcome
would leave no telling which new password bitmagnet holds.

An answer settles the change it answers, matched by number, with two exceptions that hold
whichever change it answers: acceptance is acted on wherever it lands, because the password
changed whoever is still looking, and a refusal of the Identity says the Identity is stale,
which it is either way. A refusal is bitmagnet saying
it changed nothing (`ApiError.mayHaveActed`). Anything else leaves the change unsettled: no
answer, one that could not be read, a bare failing status, or a fault inside bitmagnet, since
the transaction may have committed before the answer was lost.

When the Identity is next found signed out with a change still unsettled, the change may be
why. The login form is then opened on the User's name and the new password, saying so, and
signing in settles it: with the new password, it was made, and the sign-in offers it to the
credential store as any sign-in does; refused, it was not, and the old password still works.

Every decision `Main` makes about a change is a function here, taking plain values, so that
each is tested: what to send (`sending`), what an answer to it means (`answered`), what an
answer to the Identity means (`identityAnswered`), what the login form becomes on arriving at
it (`loginOnArrival`), and what a sign-in settles (`signedIn`). `Main` makes one call per
decision and carries out what comes back.

-}

import ApiError
import Bitmagnet
import Credential exposing (Credential)
import Graphql.Http
import Graphql.Operation exposing (RootMutation)
import Graphql.SelectionSet as SelectionSet exposing (SelectionSet)
import Html exposing (Html, button, h2, input, label, p, text)
import Html.Attributes exposing (attribute, class, disabled, for, id, type_, value)
import Html.Events exposing (onInput, onSubmit)
import Identity
import Login
import Magnes.Api.InputObject as InputObject
import Magnes.Api.Mutation as Mutation
import Magnes.Api.Object.SelfMutation as SelfMutation
import PasswordEntropy
import Route


{-| The form, the change not yet settled if there is one, how many changes have been sent,
which numbers the next, and the newest ask for the Identity whose answer has been heard, so
that an older one landing late is not taken for news.
-}
type alias State =
    { form : Form
    , unsettled : Maybe Unsettled
    , sent : Int
    , identityHeard : Int
    }


{-| What is typed, what bitmagnet last said about the new password, and its refusal of the
last change, if it refused it.
-}
type alias Form =
    { current : String
    , new : String
    , entropy : PasswordEntropy.Entropy
    , refusal : Maybe ApiError.Failure
    }


{-| A change sent and not yet settled. `Awaiting` is on its way. `Lost` was answered, but not
in a way that says whether it was made. `SignedOut` is either of those, found since with the
browser signed out, which it may have caused: the login form offers its new password first.
-}
type Unsettled
    = Awaiting Sent
    | Lost Sent ApiError.Failure
    | SignedOut Sent


{-| A change sent: the number its answer will carry back, and the credential it leaves the
User with if bitmagnet makes it.
-}
type alias Sent =
    { attempt : Int
    , credential : Credential
    }


{-| What bitmagnet's answer settled. `Made` is the only one that carries a credential, so only
an accepted change offers one to the credential store. `NotMade` says whether the refusal was
of the Identity, which is then stale, and whether the login form had been offered the change's
password, which it must stop offering.
-}
type Settlement
    = Made Credential
    | NotMade { identityStale : Bool, offerWithdrawn : Bool }
    | NotSettled


initial : State
initial =
    { form = emptyForm, unsettled = Nothing, sent = 0, identityHeard = 0 }


emptyForm : Form
emptyForm =
    { current = "", new = "", entropy = PasswordEntropy.Unmeasured, refusal = Nothing }


{-| Leaving the page, or the Identity being asked for again, drops the form and what is typed
into it. Not an unsettled change, nor its new password.
-}
left : State -> State
left state =
    { state | form = emptyForm }


{-| Typing is also how a refusal is dismissed: it described what was sent, and that is no
longer what is in the form.
-}
withCurrent : String -> State -> State
withCurrent current state =
    edited (\form -> { form | current = current }) state


{-| A new password abandons the measurement of the old one: see
`PasswordEntropy.forPassword`. The current password is never measured: it is not the one
being chosen.
-}
withNew : String -> State -> State
withNew new state =
    edited (\form -> { form | new = new, entropy = PasswordEntropy.forPassword new }) state


edited : (Form -> Form) -> State -> State
edited edit state =
    let
        form =
            edit state.form
    in
    { state | form = { form | refusal = Nothing } }


{-| bitmagnet's measurement of the new password: see `PasswordEntropy.landed`.
-}
measured : PasswordEntropy.Entropy -> State -> State
measured measurement state =
    let
        form =
            state.form
    in
    { state | form = { form | entropy = PasswordEntropy.landed measurement form.entropy } }


newPassword : State -> String
newPassword state =
    state.form.new


{-| Blank fields are not an error to report: the button is simply not ready yet. Nothing is
sent while a change is unsettled. Neither password is trimmed, since a space there is a
character the User chose.
-}
canSubmit : State -> Bool
canSubmit state =
    (state.unsettled == Nothing)
        && not (String.isEmpty state.form.current)
        && not (String.isEmpty state.form.new)


{-| The change the form asks for, made as the signed-in User, numbered, and unsettled until it
is answered. Nothing when the form is not ready to send, and nothing for any other Identity:
the credential a change leaves behind is that User's, and bitmagnet refuses every other
Identity a change anyway.
-}
sending : Identity.Identity -> State -> Maybe ( State, Sent )
sending identity state =
    case identity of
        Identity.UserAuthenticated user _ ->
            sendingAs user.username state

        Identity.Anonymous _ ->
            Nothing

        Identity.APIKeyAuthenticated _ _ _ ->
            Nothing

        Identity.Unknown ->
            Nothing

        Identity.Failed _ ->
            Nothing


sendingAs : String -> State -> Maybe ( State, Sent )
sendingAs username state =
    if canSubmit state then
        let
            change =
                { attempt = state.sent + 1
                , credential = { id = username, password = state.form.new }
                }

            form =
                state.form
        in
        Just
            ( { state | unsettled = Just (Awaiting change), sent = change.attempt, form = { form | refusal = Nothing } }
            , change
            )

    else
        Nothing


{-| Success is the absence of an error, as for `Login`: `updatePassword` answers `Void`. The
request is given up on after five minutes (`Bitmagnet.slowMutationRequest`), so that a change
never answered does not hold the form for good; given up on, it is unsettled like any other
whose answer was lost.
-}
submit : String -> State -> (Result (Graphql.Http.Error ()) () -> msg) -> Cmd msg
submit apiUrl state toMsg =
    mutation state
        |> Bitmagnet.slowMutationRequest apiUrl
        |> Graphql.Http.send toMsg


mutation : State -> SelectionSet () RootMutation
mutation state =
    Mutation.self
        (SelfMutation.updatePassword
            { input =
                InputObject.buildUpdatePasswordInput
                    { currentPassword = state.form.current, newPassword = state.form.new }
            }
            |> SelectionSet.map (always ())
        )


{-| bitmagnet's answer to the change `change` sent. It settles that change if it is the one
unsettled, except that a refusal of the Identity says the Identity is stale whatever it
answers, and an acceptance is acted on wherever it lands.
-}
answer : Sent -> Result (Graphql.Http.Error ()) () -> State -> ( State, Settlement )
answer change result state =
    case result of
        Ok () ->
            case Maybe.andThen (answering change) state.unsettled of
                Just _ ->
                    ( { state | form = emptyForm, unsettled = Nothing }, Made change.credential )

                -- An older change, already dropped, whose acceptance arrives late. It changed
                -- the password, so it is acted on, but a newer change still waiting for its own
                -- answer is not this one's to settle, nor is a form the person is typing into.
                Nothing ->
                    ( state, Made change.credential )

        Err error ->
            let
                failure =
                    ApiError.fromError error
            in
            case Maybe.andThen (answering change) state.unsettled of
                Nothing ->
                    if ApiError.mayHaveActed failure then
                        ( state, NotSettled )

                    else
                        ( state, NotMade { identityStale = ApiError.isUnauthorized failure, offerWithdrawn = False } )

                Just unsettled ->
                    if ApiError.mayHaveActed failure then
                        ( { state | unsettled = Just (unanswered failure unsettled) }, NotSettled )

                    else
                        let
                            form =
                                state.form

                            identityStale =
                                ApiError.isUnauthorized failure
                        in
                        ( { state
                            | unsettled = Nothing
                            , form =
                                if identityStale then
                                    form

                                else
                                    { form | refusal = Just failure }
                          }
                        , NotMade { identityStale = identityStale, offerWithdrawn = offers unsettled }
                        )


{-| The unsettled change, if it is the one `change` was.
-}
answering : Sent -> Unsettled -> Maybe Unsettled
answering change unsettled =
    if (sentOf unsettled).attempt == change.attempt then
        Just unsettled

    else
        Nothing


{-| A lost answer leaves a change on its way lost, and one already found signed out as it was.
-}
unanswered : ApiError.Failure -> Unsettled -> Unsettled
unanswered failure unsettled =
    case unsettled of
        Awaiting change ->
            Lost change failure

        Lost change _ ->
            Lost change failure

        SignedOut change ->
            SignedOut change


sentOf : Unsettled -> Sent
sentOf unsettled =
    case unsettled of
        Awaiting change ->
            change

        Lost change _ ->
            change

        SignedOut change ->
            change


offers : Unsettled -> Bool
offers unsettled =
    case unsettled of
        SignedOut _ ->
            True

        Awaiting _ ->
            False

        Lost _ _ ->
            False


{-| What the Identity bitmagnet now reports, in answer to the `ask`th look, means for an
unsettled change.

An answer older than one already heard says nothing: two looks can answer out of order, and
one asked before the change committed would otherwise undo what a later one found.

Signed out, Anonymous or by an API key, the change may be why. The first time, its credential
is returned for the login form to offer. Signed in as the User who sent it, nothing is known
yet: still signed in says only that it had not committed when bitmagnet was asked. Signed in
again after being found signed out, a sign-in has settled it.

Signed in as anyone else, the change is dropped, and with it Magnes's copy of its new password.
That is a deliberate trade. Kept, the password would stay in memory while someone else uses
the tab, and could be offered on a login form to them or to whoever comes next. The cost falls
on the person who made the change: if it went through, the only copy may be the one their
password manager kept from the field.

-}
identified : Int -> Identity.Identity -> State -> ( State, Maybe Credential )
identified ask identity heard =
    if ask <= heard.identityHeard then
        ( heard, Nothing )

    else
        identifiedNewer identity { heard | identityHeard = ask }


identifiedNewer : Identity.Identity -> State -> ( State, Maybe Credential )
identifiedNewer identity state =
    case ( identity, state.unsettled ) of
        ( _, Nothing ) ->
            ( state, Nothing )

        ( Identity.Anonymous _, Just unsettled ) ->
            foundSignedOut unsettled state

        ( Identity.APIKeyAuthenticated _ _ _, Just unsettled ) ->
            foundSignedOut unsettled state

        ( Identity.UserAuthenticated user _, Just unsettled ) ->
            if offers unsettled || user.username /= (sentOf unsettled).credential.id then
                ( { state | unsettled = Nothing }, Nothing )

            else
                ( state, Nothing )

        ( Identity.Unknown, Just _ ) ->
            ( state, Nothing )

        ( Identity.Failed _, Just _ ) ->
            ( state, Nothing )


foundSignedOut : Unsettled -> State -> ( State, Maybe Credential )
foundSignedOut unsettled state =
    if offers unsettled then
        ( state, Nothing )

    else
        ( { state | unsettled = Just (SignedOut (sentOf unsettled)) }
        , Just (sentOf unsettled).credential
        )


{-| The credential the login form should offer first: the new password of a change found
signed out and not yet settled.
-}
offered : State -> Maybe Credential
offered state =
    state.unsettled
        |> Maybe.andThen
            (\unsettled ->
                if offers unsettled then
                    Just (sentOf unsettled).credential

                else
                    Nothing
            )


{-| A sign-in that succeeded in this tab settles any unsettled change: whichever password it
used, one now works, and the person knows which.
-}
signedIn : State -> State
signedIn state =
    { state | unsettled = Nothing }


{-| What `Main` is to do about an answer to the Identity: keep the change and the login form as
they come back, and go where `goTo` says, if anywhere.
-}
identityAnswered : Route.BasePath -> Int -> Identity.Identity -> State -> Login.Form -> { change : State, login : Login.Form, goTo : Maybe Route.Route }
identityAnswered mount ask identity state login =
    let
        ( change, offer ) =
            identified ask identity state
    in
    case offer of
        -- Signed out with a change unsettled, which may be why: whatever asked, and wherever
        -- the person is, the login form tries the change's new password first and says so.
        Just credential ->
            { change = change
            , login = filledWith credential login
            , goTo = Just (Route.loginReturningTo mount Route.UserOverview)
            }

        Nothing ->
            { change = change, login = login, goTo = Nothing }


{-| The login form on arriving at it, from the page `cameFrom`. It is opened on a change's new
password only when a page that needs a User sent the person there, which is where a change
found signed out leaves them; not when they came of their own accord, by the header's link
that is open to whoever is at the keyboard, nor from registering someone else.
-}
loginOnArrival : Route.BasePath -> Identity.Identity -> Route.Route -> State -> Login.Form -> Login.Form
loginOnArrival mount identity cameFrom state login =
    case Route.guard mount identity cameFrom of
        Route.RedirectTo (Route.Login _) ->
            offered state
                |> Maybe.map (\credential -> filledWith credential login)
                |> Maybe.withDefault login

        Route.RedirectTo _ ->
            login

        Route.Allowed ->
            login

        Route.PendingIdentity ->
            login

        Route.Refused _ ->
            login


{-| The login form opened on `credential`, unless it already names another User: filled in
over someone else's name, Enter would sign the wrong person in.
-}
filledWith : Credential -> Login.Form -> Login.Form
filledWith credential login =
    if namesAnother credential login then
        login

    else
        Login.afterLostChange credential


namesAnother : Credential -> Login.Form -> Bool
namesAnother credential login =
    let
        name =
            String.trim login.username
    in
    not (String.isEmpty name) && name /= credential.id


{-| What `Main` is to do about an answer to a change: keep the change and the login form as
they come back, offer `offer` to the credential store, go where `goTo` says, and treat the
browser's sign-in as `signIn` says.
-}
type alias Answered =
    { change : State
    , login : Login.Form
    , offer : Maybe Credential
    , goTo : Maybe Route.Route
    , signIn : SignIn
    }


{-| What became of the browser's sign-in. `Stale`: a refusal said the Identity in hand is not
the one bitmagnet sees, so it is asked for again. `Ended`: the change ended every sign-in the
User had, so the other tabs are told and it is asked for again.
-}
type SignIn
    = Unaffected
    | Stale
    | Ended


answered : Route.BasePath -> Sent -> Result (Graphql.Http.Error ()) () -> State -> Login.Form -> Answered
answered mount change result state login =
    let
        ( next, settlement ) =
            answer change result state
    in
    case settlement of
        Made credential ->
            { change = next
            , login = confirmedOn credential login
            , offer = Just credential
            , goTo = Just (Route.loginReturningTo mount Route.UserOverview)
            , signIn = Ended
            }

        NotMade { identityStale, offerWithdrawn } ->
            { change = next
            , login =
                -- The login form was offering this change's new password as the one to try
                -- first. Refused, it was never made, and is no longer offered; a password the
                -- person typed over it, and a sign-in on its way, stay.
                if offerWithdrawn then
                    Login.withoutReason login

                else
                    login
            , offer = Nothing
            , goTo = Nothing
            , signIn =
                if identityStale then
                    Stale

                else
                    Unaffected
            }

        NotSettled ->
            { change = next, login = login, offer = Nothing, goTo = Nothing, signIn = Unaffected }


{-| The login form once a change is accepted. If it is already trying that change's new
password, it keeps what is in it and says the change was made: the password is now known to
work, and may be Magnes's only copy where the browser has no credential store. Otherwise it is
opened on the User's name, saying why.
-}
confirmedOn : Credential -> Login.Form -> Login.Form
confirmedOn credential login =
    if login.reason == Just Login.PasswordMayHaveChanged && String.trim login.username == credential.id then
        Login.changeConfirmed login

    else
        Login.afterPasswordChange credential.id


{-| The form reports interactions; it holds no state of its own and runs no update.
-}
type alias Messages msg =
    { currentChanged : String -> msg
    , newChanged : String -> msg
    , submitted : msg
    }


view : Messages msg -> State -> Html msg
view messages state =
    Html.form [ class "panel", onSubmit messages.submitted ]
        [ h2 [] [ text "Change your password" ]
        , p [ class "notice password-change-warning" ]
            [ text "Changing your password signs you out everywhere: every device and browser signed in as you, this one included. You then sign in again with the new password." ]
        , p [ class "notice password-change-warning" ]
            [ text "Your API keys are not affected and keep working. Revoke any you no longer trust on the API keys page." ]
        , label [ for (fieldId Current) ] [ text "Current password" ]
        , input
            ([ id (fieldId Current)
             , type_ "password"
             , value state.form.current
             , onInput messages.currentChanged
             , attribute "autocomplete" "current-password"
             ]
                ++ blame Current state.form
            )
            []
        , fieldError Current state.form
        , label [ for (fieldId New) ] [ text "New password" ]
        , input
            ([ id (fieldId New)
             , type_ "password"
             , value state.form.new
             , onInput messages.newChanged
             , attribute "autocomplete" "new-password"
             ]
                ++ blame New state.form
            )
            []
        , fieldError New state.form
        , PasswordEntropy.meter state.form.entropy
        , formError state.form
        , unsettledNotice state.unsettled
        , button
            [ type_ "submit"
            , class "submit"
            , disabled (not (canSubmit state))
            ]
            [ text
                (case state.unsettled of
                    Just (Awaiting _) ->
                        "Changing…"

                    _ ->
                        "Change password"
                )
            ]
        ]


{-| Which field a refusal is about, when it is about one.
-}
type Field
    = Current
    | New


fieldId : Field -> String
fieldId field =
    case field of
        Current ->
            "password-current"

        New ->
            "password-new"


errorId : Field -> String
errorId field =
    fieldId field ++ "-error"


{-| Every outcome is listed, so that a new one is placed here deliberately rather than falling
through to "neither".
-}
about : ApiError.Failure -> Maybe Field
about failure =
    case failure of
        ApiError.PasswordIncorrect ->
            Just Current

        ApiError.PasswordInsufficientEntropy ->
            Just New

        ApiError.InvalidCredentials ->
            Nothing

        ApiError.UserDisabled ->
            Nothing

        ApiError.LoginThrottled ->
            Nothing

        ApiError.UserAlreadyExists ->
            Nothing

        ApiError.UsernameInvalid ->
            Nothing

        ApiError.InvitationRequired ->
            Nothing

        ApiError.InvitationInvalid ->
            Nothing

        ApiError.InvitationExpired ->
            Nothing

        ApiError.InvitationClaimed ->
            Nothing

        ApiError.EmailRequired ->
            Nothing

        ApiError.Unauthorized _ ->
            Nothing

        ApiError.AuthenticationRequired _ ->
            Nothing

        ApiError.UserAuthenticationRequired ->
            Nothing

        ApiError.ApiKeyManagementForbidden ->
            Nothing

        ApiError.ServiceUnavailable ->
            Nothing

        ApiError.ServerFault ->
            Nothing

        ApiError.ServerRejected _ ->
            Nothing

        ApiError.RequestInvalid _ ->
            Nothing

        ApiError.Forbidden _ ->
            Nothing

        ApiError.Unreachable ->
            Nothing

        ApiError.Timeout ->
            Nothing

        ApiError.BadStatus _ ->
            Nothing

        ApiError.BadUrl _ ->
            Nothing

        ApiError.SchemaMismatch ->
            Nothing


{-| The refusal on the form if it is about `field`.
-}
refusalOf : Field -> Form -> Maybe ApiError.Failure
refusalOf field form =
    form.refusal
        |> Maybe.andThen
            (\failure ->
                if about failure == Just field then
                    Just failure

                else
                    Nothing
            )


blame : Field -> Form -> List (Html.Attribute msg)
blame field form =
    case refusalOf field form of
        Just _ ->
            [ attribute "aria-invalid" "true", attribute "aria-describedby" (errorId field) ]

        Nothing ->
            [ attribute "aria-invalid" "false" ]


{-| Directly under the field it is about.
-}
fieldError : Field -> Form -> Html msg
fieldError field form =
    case refusalOf field form of
        Just failure ->
            p
                [ id (errorId field)
                , class "notice error field-error"
                , attribute "role" "alert"
                ]
                [ text (ApiError.toMessage failure) ]

        Nothing ->
            text ""


{-| A refusal about neither field marks neither: pointing at a field that is fine sends the
reader to fix the wrong thing. It is said above the button it will be tried from again.
-}
formError : Form -> Html msg
formError form =
    case form.refusal of
        Just failure ->
            if about failure == Nothing then
                p [ class "notice error field-error", attribute "role" "alert" ]
                    [ text ("Your password was not changed. " ++ ApiError.toMessage failure) ]

            else
                text ""

        Nothing ->
            text ""


{-| A change whose outcome is not known, said for as long as it is not, coming back to the page
included, and a change on its way, which says how it will be settled.
-}
unsettledNotice : Maybe Unsettled -> Html msg
unsettledNotice unsettled =
    case unsettled of
        Just (Lost _ failure) ->
            p [ class "notice error field-error", attribute "role" "alert" ]
                [ text "No usable answer to the change came back. "
                , text (ApiError.toMessage failure)
                , text " Whether your password was changed is not known. "
                , text "If it was, you are signed out everywhere and only the new password signs in. Magnes keeps it until this is settled, and the login form will offer it first."
                , text " "
                , text stillUnsettled
                ]

        Just (SignedOut _) ->
            p [ class "notice error field-error", attribute "role" "alert" ]
                [ text "Whether your password was changed is not known. "
                , text stillUnsettled
                ]

        Just (Awaiting _) ->
            p [ class "notice password-change-warning", attribute "role" "status" ]
                [ text "Waiting for bitmagnet's answer to the change. Another cannot be sent until it comes; if none has come within five minutes, Magnes stops waiting and says what it knows." ]

        Nothing ->
            text ""


stillUnsettled : String
stillUnsettled =
    "Another change cannot be sent until this one is settled, which signing in again does: if you are still signed in, sign out first."
