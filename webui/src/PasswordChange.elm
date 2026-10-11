module PasswordChange exposing (Credential, Form, Messages, Outcome(..), Sent, Submission(..), answer, canSubmit, empty, measured, mutation, sending, submit, view, withCurrent, withNew)

{-| A signed-in User changing their own password, on `/account`.

`self.updatePassword` (bitmagnet `77f3fd9e3`) checks the current password, holds the new
one to the instance's minimum entropy, and ends every sign-in the User has, this one
included: no credential is re-issued, so success leaves the browser Anonymous. The form
says so before anything is sent. What follows a success is `Main`'s: offering the new
password to the browser's credential store, telling the other tabs, refetching the
Identity, and landing on the login form with the reason.

An answer is matched to the change it answers by number, and one the form is no longer
waiting for is not drawn on it. Success is the exception: the password changed whoever is
still looking, so `Changed` is acted on wherever it lands.

A GraphQL error is bitmagnet answering that it changed nothing. Anything else, no answer
or one that could not be read, leaves the change unknown, because the transaction may have
committed before the answer was lost; the form says so rather than claiming it failed.

-}

import ApiError
import Bitmagnet
import Graphql.Http
import Graphql.Operation exposing (RootMutation)
import Graphql.SelectionSet as SelectionSet exposing (SelectionSet)
import Html exposing (Html, button, h2, input, label, p, text)
import Html.Attributes exposing (attribute, class, disabled, for, id, type_, value)
import Html.Events exposing (onInput, onSubmit)
import Magnes.Api.InputObject as InputObject
import Magnes.Api.Mutation as Mutation
import Magnes.Api.Object.SelfMutation as SelfMutation
import PasswordEntropy


{-| What is typed, what bitmagnet last said about the new password, and how the last change
went.
-}
type alias Form =
    { current : String
    , new : String
    , entropy : PasswordEntropy.Entropy
    , state : Submission
    }


{-| How the last change went. A change on its way carries the number its answer will carry
back.
-}
type Submission
    = Ready
    | Changing Int
    | Refused ApiError.Failure
    | Unknown ApiError.Failure


{-| What the browser's password manager is offered: see `Main.storeCredential`.
-}
type alias Credential =
    { id : String
    , password : String
    }


{-| A change on its way: the number its answer will carry back, and the credential it
would leave the User with if bitmagnet accepts it.
-}
type alias Sent =
    { attempt : Int
    , credential : Credential
    }


empty : Form
empty =
    { current = "", new = "", entropy = PasswordEntropy.Unmeasured, state = Ready }


withCurrent : String -> Form -> Form
withCurrent current form =
    edited { form | current = current }


{-| A new password abandons the measurement of the old one: see
`PasswordEntropy.forPassword`. The current password is never measured: it is not the one
being chosen.
-}
withNew : String -> Form -> Form
withNew new form =
    edited { form | new = new, entropy = PasswordEntropy.forPassword new }


{-| bitmagnet's measurement of the new password. It lands only on a form still waiting for
one: a form emptied while it was on its way, because the Identity changed or a change was
accepted, has no password for it to describe.
-}
measured : PasswordEntropy.Entropy -> Form -> Form
measured measurement form =
    if form.entropy == PasswordEntropy.Measuring then
        { form | entropy = measurement }

    else
        form


{-| A keystroke does not forget a change on its way: its answer is still coming.
-}
edited : Form -> Form
edited form =
    case form.state of
        Changing _ ->
            form

        Ready ->
            form

        Refused _ ->
            { form | state = Ready }

        Unknown _ ->
            { form | state = Ready }


{-| Blank fields are not an error to report: the button is simply not ready yet. A change
already on its way is not sent twice. Neither password is trimmed, since a space there is
a character the User chose.
-}
canSubmit : Form -> Bool
canSubmit form =
    not (onItsWay form)
        && not (String.isEmpty form.current)
        && not (String.isEmpty form.new)


onItsWay : Form -> Bool
onItsWay form =
    case form.state of
        Changing _ ->
            True

        Ready ->
            False

        Refused _ ->
            False

        Unknown _ ->
            False


{-| The change the form asks for, numbered `attempt`, made as the User `username`, and the
form marked as waiting for it. Nothing when the form is not ready to send.
-}
sending : Int -> String -> Form -> Maybe ( Form, Sent )
sending attempt username form =
    if canSubmit form then
        Just
            ( { form | state = Changing attempt }
            , { attempt = attempt, credential = { id = username, password = form.new } }
            )

    else
        Nothing


{-| What bitmagnet's answer to a change means.

`Changed` is the only outcome that carries a credential, so nothing but an accepted change
can offer one to the browser's credential store. `IdentityStale` is a refusal saying the
Identity in hand is not the one bitmagnet sees: signed out elsewhere before the change
arrived, which changed nothing. `Answered` is the form as it should now be drawn.

-}
type Outcome
    = Changed Credential
    | IdentityStale
    | Answered Form


answer : Sent -> Result (Graphql.Http.Error ()) () -> Form -> Outcome
answer sent result form =
    case result of
        Ok () ->
            Changed sent.credential

        Err error ->
            let
                failure =
                    ApiError.fromError error
            in
            if ApiError.isUnauthorized failure then
                IdentityStale

            else if form.state /= Changing sent.attempt then
                Answered form

            else
                case error of
                    Graphql.Http.GraphqlError _ _ ->
                        Answered { form | state = Refused failure }

                    Graphql.Http.HttpError _ ->
                        Answered { form | state = Unknown failure }


{-| Success is the absence of an error, as for `Login`: `updatePassword` answers `Void`.
-}
submit : String -> Form -> (Result (Graphql.Http.Error ()) () -> msg) -> Cmd msg
submit apiUrl form toMsg =
    mutation form
        |> Bitmagnet.mutationRequest apiUrl
        |> Graphql.Http.send toMsg


mutation : Form -> SelectionSet () RootMutation
mutation form =
    Mutation.self
        (SelfMutation.updatePassword
            { input =
                InputObject.buildUpdatePasswordInput
                    { currentPassword = form.current, newPassword = form.new }
            }
            |> SelectionSet.map (always ())
        )


{-| The form reports interactions; it holds no state of its own and runs no update.
-}
type alias Messages msg =
    { currentChanged : String -> msg
    , newChanged : String -> msg
    , submitted : msg
    }


view : Messages msg -> Form -> Html msg
view messages form =
    Html.form [ class "panel", onSubmit messages.submitted ]
        [ h2 [] [ text "Change your password" ]
        , p [ class "notice password-change-warning" ]
            [ text "Changing your password signs you out everywhere: every device and browser signed in as you, this one included. You then sign in again with the new password." ]
        , p [ class "notice password-change-warning" ]
            [ text "Your API keys are not affected and keep working. Revoke any you no longer trust on the API keys page." ]
        , label [ for currentFieldId ] [ text "Current password" ]
        , input
            [ id currentFieldId
            , type_ "password"
            , value form.current
            , onInput messages.currentChanged
            , attribute "autocomplete" "current-password"
            , ariaInvalid form.state Current
            , describedBy form.state Current
            ]
            []
        , fieldError form.state Current
        , label [ for newFieldId ] [ text "New password" ]
        , input
            [ id newFieldId
            , type_ "password"
            , value form.new
            , onInput messages.newChanged
            , attribute "autocomplete" "new-password"
            , ariaInvalid form.state New
            , describedBy form.state New
            ]
            []
        , fieldError form.state New
        , PasswordEntropy.meter form.entropy
        , formError form.state
        , button
            [ type_ "submit"
            , class "submit"
            , disabled (not (canSubmit form))
            ]
            [ text
                (if onItsWay form then
                    "Changing…"

                 else
                    "Change password"
                )
            ]
        ]


currentFieldId : String
currentFieldId =
    "password-current"


newFieldId : String
newFieldId =
    "password-new"


{-| Which field a refusal is about, when it is about one.
-}
type Field
    = Current
    | New


about : ApiError.Failure -> Maybe Field
about failure =
    case failure of
        ApiError.PasswordIncorrect ->
            Just Current

        ApiError.PasswordInsufficientEntropy ->
            Just New

        _ ->
            Nothing


blames : Submission -> Field -> Bool
blames submission field =
    case submission of
        Refused failure ->
            about failure == Just field

        Ready ->
            False

        Changing _ ->
            False

        Unknown _ ->
            False


fieldId : Field -> String
fieldId field =
    case field of
        Current ->
            currentFieldId

        New ->
            newFieldId


errorId : Field -> String
errorId field =
    fieldId field ++ "-error"


ariaInvalid : Submission -> Field -> Html.Attribute msg
ariaInvalid submission field =
    attribute "aria-invalid"
        (if blames submission field then
            "true"

         else
            "false"
        )


describedBy : Submission -> Field -> Html.Attribute msg
describedBy submission field =
    if blames submission field then
        attribute "aria-describedby" (errorId field)

    else
        class ""


{-| Directly under the field it is about.
-}
fieldError : Submission -> Field -> Html msg
fieldError submission field =
    case submission of
        Refused failure ->
            if blames submission field then
                p
                    [ id (errorId field)
                    , class "notice error field-error"
                    , attribute "role" "alert"
                    ]
                    [ text (ApiError.toMessage failure) ]

            else
                text ""

        Ready ->
            text ""

        Changing _ ->
            text ""

        Unknown _ ->
            text ""


{-| A refusal about neither field marks neither: pointing at a field that is fine sends the
reader to fix the wrong thing. It is said above the button it will be tried from again.
-}
formError : Submission -> Html msg
formError submission =
    case submission of
        Refused failure ->
            if about failure == Nothing then
                p [ class "notice error field-error", attribute "role" "alert" ]
                    [ text ("Your password was not changed. " ++ ApiError.toMessage failure) ]

            else
                text ""

        Unknown failure ->
            p [ class "notice error field-error", attribute "role" "alert" ]
                [ text "No usable answer to the change came back. "
                , text (ApiError.toMessage failure)
                , text " Whether your password was changed is not known. "
                , text "If it was, you are signed out everywhere and only the new password signs in."
                ]

        Ready ->
            text ""

        Changing _ ->
            text ""
