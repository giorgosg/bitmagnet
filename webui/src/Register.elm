module Register exposing (Form, Messages, Submission(..), canSubmit, credentials, empty, entropy, invitationCode, measured, passwordOf, prefilled, state, submit, usernameFieldId, view, withInvitationCode, withPassword, withState, withUsername)

{-| Registering a User.

Registration deliberately does not authenticate the browser. `self.register` answers with
the User it created and sets no cookie; the spec requires the person to arrive at the
login form afterwards and sign in with what they just chose.

No email address is shown or collected. bitmagnet's `auth.email_verification` is inert —
documented as such in its own `docs/auth.md` — so a field here would imply an address had
been verified when nothing verifies it. An instance configured with `email_required` is
therefore not registrable from Magnes, and `ApiError.EmailRequired` says exactly that
rather than refusing without explanation.

-}

import ApiError
import Bitmagnet
import Graphql.Http
import Graphql.Operation exposing (RootMutation)
import Graphql.OptionalArgument as Opt
import Graphql.SelectionSet exposing (SelectionSet)
import Html exposing (Html, button, h1, input, label, p, text)
import Html.Attributes exposing (attribute, class, disabled, for, id, spellcheck, type_, value)
import Html.Events exposing (onInput, onSubmit)
import Magnes.Api.InputObject as InputObject
import Magnes.Api.Mutation as Mutation
import Magnes.Api.Object.RegisterResult as RegisterResult
import Magnes.Api.Object.SelfMutation as SelfMutation
import Magnes.Api.Object.User as ApiUser
import PasswordEntropy


{-| What is typed, what the server last said about the password, and how the last attempt
ended. The rejection keeps its `Failure` for the same reason `Login` does: the outcomes
are worth telling apart even where they are currently drawn the same way.
-}
type alias Form =
    { username : String
    , password : String
    , invitation : String
    , entropy : PasswordEntropy.Entropy
    , state : Submission
    }


type Submission
    = Ready
    | Submitting
    | Rejected ApiError.Failure


empty : Form
empty =
    { username = ""
    , password = ""
    , invitation = ""
    , entropy = PasswordEntropy.Unmeasured
    , state = Ready
    }


{-| The form as an Invitation link leaves it. The code is shown rather than hidden: it is
what makes registration possible here, and someone who followed a broken link needs to be
able to see and correct it.
-}
prefilled : Maybe String -> Form
prefilled code =
    { empty | invitation = Maybe.withDefault "" code }


{-| Typing is also how a rejection is dismissed: it described what was just refused, and
that is no longer what is in the form. Each field has its own setter so that clearing the
rejection cannot be bypassed by a caller writing the record directly.
-}
withUsername : String -> Form -> Form
withUsername username form =
    withState Ready { form | username = username }


{-| A new password abandons the old measurement: see `PasswordEntropy.forPassword`.
-}
withPassword : String -> Form -> Form
withPassword password form =
    withState Ready
        { form
            | password = password
            , entropy = PasswordEntropy.forPassword password
        }


withInvitationCode : String -> Form -> Form
withInvitationCode code form =
    withState Ready { form | invitation = code }


{-| bitmagnet's measurement of the password: see `PasswordEntropy.landed`.
-}
measured : PasswordEntropy.Entropy -> Form -> Form
measured measurement form =
    { form | entropy = PasswordEntropy.landed measurement form.entropy }


withState : Submission -> Form -> Form
withState submission form =
    { form | state = submission }


state : Form -> Submission
state form =
    form.state


entropy : Form -> PasswordEntropy.Entropy
entropy form =
    form.entropy


passwordOf : Form -> String
passwordOf form =
    form.password


{-| Blank fields are not an error to report — the button is simply not ready yet — and a
request already in flight is not sent twice. The Invitation is not required here even
where the instance requires one: only the server knows that setting, and it answers with
`INVITATION_REQUIRED`, which is a clearer thing to read than a button that will not press.
-}
canSubmit : Form -> Bool
canSubmit form =
    (form.state /= Submitting)
        && not (String.isEmpty (String.trim form.username))
        && not (String.isEmpty form.password)


{-| The Invitation as it will be sent: absent rather than empty, because bitmagnet
distinguishes an omitted code from a blank one.
-}
invitationCode : Form -> Maybe String
invitationCode form =
    case String.trim form.invitation of
        "" ->
            Nothing

        code ->
            Just code


type alias Credentials =
    { username : String
    , password : String
    , invitationCode : Maybe String
    }


{-| The username is trimmed because a trailing space is a typo rather than an intention;
the password never is, since a space there is a character the User chose.
-}
credentials : Form -> Credentials
credentials form =
    { username = String.trim form.username
    , password = form.password
    , invitationCode = invitationCode form
    }


{-| Answers with the username bitmagnet created, which is the one the login form should
open on. Registration is the only place that name is known to be right — it may differ
from what was typed only in the trimming, but it is the server's copy of it.
-}
submit : String -> Credentials -> (Result (Graphql.Http.Error String) String -> msg) -> Cmd msg
submit apiUrl offered toMsg =
    registration offered
        |> Bitmagnet.mutationRequest apiUrl
        |> Graphql.Http.send toMsg


registration : Credentials -> SelectionSet String RootMutation
registration offered =
    Mutation.self
        (SelfMutation.register
            { input =
                InputObject.buildRegisterInput
                    { username = offered.username, password = offered.password }
                    (\optional ->
                        { optional | invitationCode = Opt.fromMaybe offered.invitationCode }
                    )
            }
            (RegisterResult.user ApiUser.username)
        )


{-| The form reports interactions; it holds no state of its own and runs no update.
-}
type alias Messages msg =
    { usernameChanged : String -> msg
    , passwordChanged : String -> msg
    , invitationCodeChanged : String -> msg
    , submitted : msg
    }


{-| Exposed so the page that shows the form can move focus here on arrival, for the same
reason as `Login.usernameFieldId`: `autofocus` does not fire under a virtual-DOM patch.
-}
usernameFieldId : String
usernameFieldId =
    "register-username"


view : Messages msg -> Form -> Html msg
view messages form =
    Html.form [ class "panel", onSubmit messages.submitted ]
        [ h1 [] [ text "Register" ]
        , label [ for usernameFieldId ] [ text "Username" ]
        , input
            [ id usernameFieldId
            , type_ "text"
            , value form.username
            , onInput messages.usernameChanged
            , attribute "autocomplete" "username"
            , attribute "autocapitalize" "none"
            , spellcheck False
            , ariaInvalid form.state Username
            , describedByRejection form.state Username
            ]
            []
        , label [ for "register-password" ] [ text "Password" ]
        , input
            [ id "register-password"
            , type_ "password"
            , value form.password
            , onInput messages.passwordChanged
            , attribute "autocomplete" "new-password"
            , ariaInvalid form.state Password
            , describedByRejection form.state Password
            ]
            []
        , PasswordEntropy.meter form.entropy
        , label [ for "register-invitation" ] [ text "Invitation code" ]
        , input
            [ id "register-invitation"
            , type_ "text"
            , value form.invitation
            , onInput messages.invitationCodeChanged
            , attribute "autocomplete" "off"
            , spellcheck False
            , ariaInvalid form.state InvitationCode
            , describedByRejection form.state InvitationCode
            ]
            []
        , rejection form.state
        , button
            [ type_ "submit"
            , class "submit"
            , disabled (not (canSubmit form))
            ]
            [ text
                (if form.state == Submitting then
                    "Registering…"

                 else
                    "Register"
                )
            ]
        ]


rejection : Submission -> Html msg
rejection submission =
    case submission of
        Rejected failure ->
            p
                [ id rejectionId
                , class "notice error field-error"
                , attribute "role" "alert"
                ]
                [ text (ApiError.toMessage failure) ]

        Ready ->
            text ""

        Submitting ->
            text ""


rejectionId : String
rejectionId =
    "register-rejection"


{-| Which field a refusal is about. Every refusal here is a statement about something in
the form, but not about all of it: an expired Invitation says nothing about the username,
and marking all three fields invalid points at two that are fine.
-}
type Field
    = Username
    | Password
    | InvitationCode


blames : Submission -> Field -> Bool
blames submission field =
    case submission of
        Rejected failure ->
            field == about failure

        Ready ->
            False

        Submitting ->
            False


{-| Where each documented outcome belongs. A failure Magnes cannot place — a transport
error, or a code from a newer bitmagnet — is put on the username: it is the first field,
so the message is read on the way down rather than after everything it is not about.
-}
about : ApiError.Failure -> Field
about failure =
    case failure of
        ApiError.InvitationRequired ->
            InvitationCode

        ApiError.InvitationInvalid ->
            InvitationCode

        ApiError.InvitationExpired ->
            InvitationCode

        ApiError.InvitationClaimed ->
            InvitationCode

        ApiError.PasswordInsufficientEntropy ->
            Password

        _ ->
            Username


ariaInvalid : Submission -> Field -> Html.Attribute msg
ariaInvalid submission field =
    attribute "aria-invalid"
        (if blames submission field then
            "true"

         else
            "false"
        )


describedByRejection : Submission -> Field -> Html.Attribute msg
describedByRejection submission field =
    if blames submission field then
        attribute "aria-describedby" rejectionId

    else
        class ""
