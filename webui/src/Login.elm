module Login exposing (Form, Messages, Submission(..), canSubmit, credentials, empty, submit, usernameFieldId, view, withPassword, withState, withUsername)

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
import Graphql.Http
import Graphql.Operation exposing (RootMutation)
import Graphql.SelectionSet as SelectionSet exposing (SelectionSet)
import Html exposing (Html, button, h1, input, label, p, text)
import Html.Attributes exposing (attribute, class, disabled, for, id, spellcheck, type_, value)
import Html.Events exposing (onInput, onSubmit)
import Magnes.Api.Mutation as Mutation
import Magnes.Api.Object.SelfMutation as SelfMutation


{-| What is typed, and how the last attempt ended.

A rejection keeps its `Failure` rather than a sentence, because the form treats the
outcomes differently: invalid credentials belong beside the fields, and throttling does
not belong there at all. Flattening them to a string here would throw away the only thing
that distinguishes them.

-}
type alias Form =
    { username : String
    , password : String
    , state : Submission
    }


type Submission
    = Ready
    | Submitting
    | Rejected ApiError.Failure


empty : Form
empty =
    { username = "", password = "", state = Ready }


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


view : Messages msg -> Form -> Html msg
view messages form =
    let
        ( wait, fieldError ) =
            notices form.state
    in
    Html.form [ class "panel", onSubmit messages.submitted ]
        [ h1 [] [ text "Sign in" ]
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
            , describedByRejection form.state
            ]
            []
        , label [ for "login-password" ] [ text "Password" ]
        , input
            [ id "login-password"
            , type_ "password"
            , value form.password
            , onInput messages.passwordChanged
            , attribute "autocomplete" "current-password"
            , ariaInvalid form.state
            , describedByRejection form.state
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


describedByRejection : Submission -> Html.Attribute msg
describedByRejection state =
    case state of
        Rejected ApiError.LoginThrottled ->
            class ""

        Rejected _ ->
            attribute "aria-describedby" rejectionId

        _ ->
            class ""
