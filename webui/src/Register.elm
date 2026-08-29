module Register exposing (Entropy(..), Form, Messages, Strength, Submission(..), canSubmit, credentials, empty, entropy, invitationCode, measure, passwordOf, prefilled, state, submit, usernameFieldId, view, withEntropy, withInvitationCode, withPassword, withState, withUsername)

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
import Graphql.Operation exposing (RootMutation, RootQuery)
import Graphql.OptionalArgument as Opt
import Graphql.SelectionSet as SelectionSet exposing (SelectionSet)
import Html exposing (Html, button, div, h1, input, label, p, text)
import Html.Attributes exposing (attribute, class, disabled, for, id, spellcheck, type_, value)
import Html.Events exposing (onInput, onSubmit)
import Magnes.Api.InputObject as InputObject
import Magnes.Api.Mutation as Mutation
import Magnes.Api.Object.PasswordEntropyResult as PasswordEntropyResult
import Magnes.Api.Object.RegisterResult as RegisterResult
import Magnes.Api.Object.SelfMutation as SelfMutation
import Magnes.Api.Object.SelfQuery as SelfQuery
import Magnes.Api.Object.User as ApiUser
import Magnes.Api.Query as Query


{-| What is typed, what the server last said about the password, and how the last attempt
ended. The rejection keeps its `Failure` for the same reason `Login` does: the outcomes
are worth telling apart even where they are currently drawn the same way.
-}
type alias Form =
    { username : String
    , password : String
    , invitation : String
    , entropy : Entropy
    , state : Submission
    }


type Submission
    = Ready
    | Submitting
    | Rejected ApiError.Failure


{-| What the server last said about the password being typed.

`Unmeasurable` is not "weak". A meter drawn from a query that never answered would read as
zero against the minimum, which is a statement about the password rather than about the
request — so a failed measurement draws no meter at all. Nothing here gates submission:
bitmagnet decides, and this is advice on the way.

-}
type Entropy
    = Unmeasured
    | Measuring
    | Measured Strength
    | Unmeasurable


{-| `minEntropy` comes from the server rather than being a constant here, because it is a
configurable instance setting. The server also returns its own `valid` verdict on the
pair, which is deliberately not selected: nothing here may act on it — submission is not
gated on the measurement — so carrying it would be storing an answer to a question this
form does not ask.
-}
type alias Strength =
    { entropy : Float
    , minEntropy : Float
    }


empty : Form
empty =
    { username = ""
    , password = ""
    , invitation = ""
    , entropy = Unmeasured
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


{-| A new password abandons the old measurement rather than leaving it on screen, where it
would describe a password that is no longer typed. An empty field has nothing to measure
and nothing to say.
-}
withPassword : String -> Form -> Form
withPassword password form =
    withState Ready
        { form
            | password = password
            , entropy =
                if String.isEmpty password then
                    Unmeasured

                else
                    Measuring
        }


withInvitationCode : String -> Form -> Form
withInvitationCode code form =
    withState Ready { form | invitation = code }


withEntropy : Entropy -> Form -> Form
withEntropy measurement form =
    { form | entropy = measurement }


withState : Submission -> Form -> Form
withState submission form =
    { form | state = submission }


state : Form -> Submission
state form =
    form.state


entropy : Form -> Entropy
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


{-| The password is sent to bitmagnet to be scored. That is what the query is for and what
bitmagnet's own UI does; the caller's job is to ask rarely, since the Angular UI asks once
per keystroke. It answers anonymously, so the meter works before anyone has signed in.
-}
measure : String -> String -> (Result (Graphql.Http.Error Strength) Strength -> msg) -> Cmd msg
measure apiUrl password toMsg =
    strength password
        |> Bitmagnet.queryRequest apiUrl
        |> Graphql.Http.send toMsg


strength : String -> SelectionSet Strength RootQuery
strength password =
    Query.self
        (SelfQuery.passwordEntropy { password = password }
            (SelectionSet.map2 Strength
                PasswordEntropyResult.entropy
                PasswordEntropyResult.minEntropy
            )
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
        , meter form.entropy
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


{-| A real `progress` element, so the platform draws it and assistive technology reads it
without any of it being reimplemented here. The numbers are shown as well as the bar: "42
of 70" is the actionable form, and the bar alone cannot say how far short it falls.
-}
meter : Entropy -> Html msg
meter measurement =
    case measurement of
        Measured strengthOf ->
            div [ class "entropy" ]
                [ Html.progress
                    [ attribute "max" (String.fromFloat strengthOf.minEntropy)
                    , value (String.fromFloat (min strengthOf.entropy strengthOf.minEntropy))
                    , attribute "aria-label" "Password strength"
                    ]
                    []
                , p [ class "entropy-reading" ]
                    [ text
                        (bits strengthOf.entropy
                            ++ " of "
                            ++ bits strengthOf.minEntropy
                            ++ " bits needed"
                        )
                    ]
                ]

        -- A `progress` with no value is the platform's indeterminate bar, which is
        -- exactly what is true here. The alternatives are both worse: an empty bar reads
        -- as a weak password, and the previous score describes a password that is no
        -- longer typed. Keeping the element also stops the meter appearing and vanishing
        -- on every pause in typing.
        Measuring ->
            div [ class "entropy" ]
                [ Html.progress [ attribute "aria-label" "Password strength" ] []
                , p [ class "entropy-reading" ] [ text "Measuring…" ]
                ]

        Unmeasured ->
            text ""

        Unmeasurable ->
            text ""


{-| bitmagnet returns these as floats. They are bit counts, so the decimals are noise.
-}
bits : Float -> String
bits value =
    String.fromInt (round value)


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
