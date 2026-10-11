module Login exposing (Form, Messages, Reason(..), Submission(..), afterLostChange, afterPasswordChange, canSubmit, credentials, empty, focusFieldId, submit, usernameFieldId, view, withPassword, withState, withUsername, withoutReason)

{-| Establishing a User-backed Identity in a browser.

`loginBrowser` is deliberately not `login`: it answers with `Void` and sets bitmagnet's
HttpOnly cookie itself, so the credential never reaches JavaScript or Elm. There is
nothing to persist here, and nothing to attach to the next request — the browser does
both. See `docs/adr/0005-use-an-http-only-cookie-for-browser-authentication.md`.

Success is therefore the absence of an error. What the new Identity may do is not in this
answer and is never guessed at: `Identity.fetch` asks the server.

-}

import ApiError
import Bitmagnet
import Credential exposing (Credential)
import Graphql.Http
import Graphql.Operation exposing (RootMutation)
import Graphql.SelectionSet as SelectionSet exposing (SelectionSet)
import Html exposing (Html, button, h1, input, label, p, text)
import Html.Attributes exposing (attribute, class, disabled, for, id, spellcheck, type_, value)
import Html.Events exposing (onInput, onSubmit)
import Magnes.Api.Mutation as Mutation
import Magnes.Api.Object.SelfMutation as SelfMutation


{-| What is typed, how the last attempt ended, and why the form was opened when that is
worth saying.

A rejection keeps its `Failure` rather than a sentence, because the form treats the
outcomes differently: invalid credentials belong beside the fields, and throttling does
not belong there at all. Flattening them to a string here would throw away the only thing
that distinguishes them.

-}
type alias Form =
    { username : String
    , password : String
    , state : Submission
    , reason : Maybe Reason
    }


{-| Why the form is on screen, when that is something other than a person choosing to sign
in. Only one thing Magnes itself does sends someone here who was signed in a moment ago and
did not ask to be signed out: changing their password, which bitmagnet answers by ending
every sign-in the User has. There are two reasons because that answer can be lost: then the
change may or may not have been made, and the form says so (see `PasswordChange`).
-}
type Reason
    = PasswordChanged
    | PasswordMayHaveChanged


type Submission
    = Ready
    | Submitting
    | Rejected ApiError.Failure


empty : Form
empty =
    { username = "", password = "", state = Ready, reason = Nothing }


{-| The form as a password change leaves it: on the User's own name, which is the one that
signs in, and saying why the person is here. The new password is not carried across; it
was offered to the browser's credential store on the way out (see `Main.storeCredential`).
-}
afterPasswordChange : String -> Form
afterPasswordChange username =
    { empty | username = username, reason = Just PasswordChanged }


{-| The form as a password change whose answer was lost leaves it: on the User's own name and
the new password, saying that the change may have been made. Signing in with it is how the
person finds out. Accepted, the change was made, and the sign-in offers the password to the
credential store as any sign-in does; refused, it was not, and the old password still works.
The password exists here only in memory, as it did in the password change's form.
-}
afterLostChange : Credential -> Form
afterLostChange credential =
    { empty | username = credential.id, password = credential.password, reason = Just PasswordMayHaveChanged }


{-| The reason describes an arrival, so leaving the form ends it. Coming back later is not
the moment the password changed. A password Magnes filled in for that reason goes with it:
without the reason, it is a password nobody typed. One the person typed stays, as it always
has.
-}
withoutReason : Form -> Form
withoutReason form =
    case form.reason of
        Just PasswordMayHaveChanged ->
            { form | reason = Nothing, password = "" }

        Just PasswordChanged ->
            { form | reason = Nothing }

        Nothing ->
            form


{-| Typing is also how a rejection is dismissed: the message described the credentials
that were just refused, and those are no longer the credentials in the form.

Each field has its own setter rather than one that takes an arbitrary change, so that
clearing the rejection cannot be bypassed by a caller writing the record directly.

-}
withUsername : String -> Form -> Form
withUsername username form =
    withState Ready { form | username = username }


withPassword : String -> Form -> Form
withPassword password form =
    withState Ready { form | password = password }


withState : Submission -> Form -> Form
withState state form =
    { form | state = state }


{-| Blank fields are not an error to report — the button is simply not ready yet. A
request already in flight is not sent twice.

The username is trimmed because a trailing space is a typo rather than an intention; the
password never is, since a space there is a character the User chose.

-}
canSubmit : Form -> Bool
canSubmit form =
    (form.state /= Submitting)
        && not (String.isEmpty (String.trim form.username))
        && not (String.isEmpty form.password)


type alias Credentials =
    { username : String
    , password : String
    }


credentials : Form -> Credentials
credentials form =
    { username = String.trim form.username, password = form.password }


submit : String -> Credentials -> (Result (Graphql.Http.Error ()) () -> msg) -> Cmd msg
submit apiUrl offered toMsg =
    selection offered
        |> Bitmagnet.mutationRequest apiUrl
        |> Graphql.Http.send toMsg


selection : Credentials -> SelectionSet () RootMutation
selection offered =
    Mutation.self
        (SelfMutation.loginBrowser
            { username = offered.username
            , password = offered.password
            }
            |> SelectionSet.map (always ())
        )


{-| The form reports interactions; it holds no state of its own and runs no update. This
is a function that draws a `Form`, not a component — see the module structure notes in
`.claude/skills/elm/SKILL.md`.
-}
type alias Messages msg =
    { usernameChanged : String -> msg
    , passwordChanged : String -> msg
    , submitted : msg
    }


{-| Exposed so the page that shows the form can move focus here on arrival. `autofocus`
is not usable: `Browser.application` renders the login route as a virtual-DOM patch
rather than a fresh document, and the attribute only acts when the element first mounts.
-}
usernameFieldId : String
usernameFieldId =
    "login-username"


passwordFieldId : String
passwordFieldId =
    "login-password"


{-| Where the cursor belongs when the form appears. With a reason to give, the username is
already filled, so it is the password field; that field is described by the reason, so the
reason is read out as the cursor lands, which a status present from the start often is not.
-}
focusFieldId : Form -> String
focusFieldId form =
    case form.reason of
        Just _ ->
            passwordFieldId

        Nothing ->
            usernameFieldId


view : Messages msg -> Form -> Html msg
view messages form =
    let
        ( wait, fieldError ) =
            notices form.state
    in
    Html.form [ class "panel", onSubmit messages.submitted ]
        [ h1 [] [ text "Sign in" ]
        , because form.reason
        , wait
        , label [ for usernameFieldId ] [ text "Username" ]
        , input
            [ id usernameFieldId
            , type_ "text"
            , value form.username
            , onInput messages.usernameChanged
            , attribute "autocomplete" "username"
            , attribute "autocapitalize" "none"
            , spellcheck False
            , ariaInvalid form.state
            , describedBy (rejectionIds form.state)
            ]
            []
        , label [ for passwordFieldId ] [ text "Password" ]
        , input
            [ id passwordFieldId
            , type_ "password"
            , value form.password
            , onInput messages.passwordChanged
            , attribute "autocomplete" "current-password"
            , ariaInvalid form.state
            , describedBy (reasonIds form.reason ++ rejectionIds form.state)
            ]
            []
        , fieldError
        , button
            [ type_ "submit"
            , class "submit"
            , disabled (not (canSubmit form))
            ]
            [ text
                (if form.state == Submitting then
                    "Signing in…"

                 else
                    "Sign in"
                )
            ]
        ]


{-| Above the fields, in the neutral colour of a status rather than the accent of an error:
nothing has gone wrong. After a change, this has to read as the change having worked; after
a lost answer, as a question the next sign-in answers.
-}
because : Maybe Reason -> Html msg
because reason =
    case reason of
        Just PasswordChanged ->
            p [ id reasonId, class "notice because", attribute "role" "status" ]
                [ text "Your password was changed, and that signed you out everywhere, this browser included. Sign in with the new password." ]

        Just PasswordMayHaveChanged ->
            p [ id reasonId, class "notice because", attribute "role" "status" ]
                [ text "Your password may have changed: the answer to the change was lost, and you are signed out, as a change would sign you out. Try the new password first; it is filled in. If it is refused, the change was not made, and your old password still works." ]

        Nothing ->
            text ""


reasonId : String
reasonId =
    "login-reason"


reasonIds : Maybe Reason -> List String
reasonIds reason =
    case reason of
        Just _ ->
            [ reasonId ]

        Nothing ->
            []


{-| One decision, two slots, so the two cannot drift into being shown together.

Throttling is drawn above the fields and the rest below them, deliberately. Invalid
credentials and a disabled User are statements about what was typed, so they belong with
the inputs; being throttled is not — the same credentials may well be right, and the
answer is to wait rather than to edit anything. In the field position it would read as
"try another password", which is the one thing that does not help.

-}
notices : Submission -> ( Html msg, Html msg )
notices state =
    case state of
        Rejected ApiError.LoginThrottled ->
            ( p
                [ class "notice wait", attribute "role" "status" ]
                [ text (ApiError.toMessage ApiError.LoginThrottled) ]
            , text ""
            )

        Rejected failure ->
            ( text ""
            , p
                [ id rejectionId
                , class "notice error field-error"
                , attribute "role" "alert"
                ]
                [ text (ApiError.toMessage failure) ]
            )

        Ready ->
            ( text "", text "" )

        Submitting ->
            ( text "", text "" )


rejectionId : String
rejectionId =
    "login-rejection"


{-| Only a rejection that is actually about the fields marks them, and only then is there
an element to point at.
-}
ariaInvalid : Submission -> Html.Attribute msg
ariaInvalid state =
    case state of
        Rejected ApiError.LoginThrottled ->
            attribute "aria-invalid" "false"

        Rejected _ ->
            attribute "aria-invalid" "true"

        _ ->
            attribute "aria-invalid" "false"


rejectionIds : Submission -> List String
rejectionIds state =
    case state of
        Rejected ApiError.LoginThrottled ->
            []

        Rejected _ ->
            [ rejectionId ]

        Ready ->
            []

        Submitting ->
            []


describedBy : List String -> Html.Attribute msg
describedBy ids =
    case ids of
        [] ->
            class ""

        _ ->
            attribute "aria-describedby" (String.join " " ids)
