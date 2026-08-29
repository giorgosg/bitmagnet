module UserOverview exposing (Messages, SignOut(..), signOut, view)

{-| What the signed-in User is, and how to stop being it.

`logoutBrowser` is the counterpart to `loginBrowser`: bitmagnet clears the HttpOnly cookie
itself, so signing out is a request rather than a local erasure, and until it answers the
User is still signed in. Nothing is cleared here on the way — there is no credential in
JavaScript or Elm to clear. See
`docs/adr/0005-use-an-http-only-cookie-for-browser-authentication.md`.

Success is the absence of an error, exactly as in `Login`. What the Identity has become is
not in the answer and is never guessed at: `Identity.fetch` asks the server.

There is deliberately no password section. bitmagnet exposes no password-change mutation
(`docs/auth-api.md`, "Mutations"), and a disabled control would advertise an affordance
that does not exist.

-}

import ApiError
import Bitmagnet
import Format
import Graphql.Http
import Graphql.Operation exposing (RootMutation)
import Graphql.SelectionSet as SelectionSet exposing (SelectionSet)
import Html exposing (Html, button, dd, div, dl, dt, h1, p, text)
import Html.Attributes exposing (attribute, class, disabled, type_)
import Html.Events exposing (onClick)
import Identity
import Magnes.Api.Mutation as Mutation
import Magnes.Api.Object.SelfMutation as SelfMutation
import Time


{-| How the last sign-out attempt ended.

A refusal keeps its `Failure` rather than a sentence for the same reason `Login` does: the
page shows the mapped message, and anything that later wants to tell the outcomes apart
still can.

-}
type SignOut
    = Ready
    | SigningOut
    | Refused ApiError.Failure


signOut : String -> (Result (Graphql.Http.Error ()) () -> msg) -> Cmd msg
signOut apiUrl toMsg =
    selection
        |> Bitmagnet.mutationRequest apiUrl
        |> Graphql.Http.send toMsg


selection : SelectionSet () RootMutation
selection =
    Mutation.self (SelfMutation.logoutBrowser |> SelectionSet.map (always ()))


{-| The page reports interactions; it holds no state of its own and runs no update.
-}
type alias Messages msg =
    { signOutRequested : msg }


view : Time.Zone -> Messages msg -> SignOut -> Identity.User -> Html msg
view zone messages state user =
    div [ class "panel" ]
        [ h1 [] [ text "Your User" ]
        , dl [ class "facts" ]
            [ fact "Username" user.username
            , fact "Role" user.role
            , fact "Last signed in" (lastSignIn zone user)
            , fact "User since" (Format.date zone user.createdAt)
            ]
        , refusal state
        , button
            [ type_ "button"
            , class "submit"
            , disabled (state == SigningOut)
            , onClick messages.signOutRequested
            ]
            [ text
                (if state == SigningOut then
                    "Signing out…"

                 else
                    "Sign out"
                )
            ]
        ]


fact : String -> String -> Html msg
fact term value =
    div [ class "fact" ]
        [ dt [] [ text term ]
        , dd [] [ text value ]
        ]


{-| bitmagnet leaves `lastLoginAt` null for a User who has never signed in — a User
created through registration and reached by API key, for instance. Saying so is honest;
substituting the date the User was created would not be.
-}
lastSignIn : Time.Zone -> Identity.User -> String
lastSignIn zone user =
    user.lastLoginAt
        |> Maybe.map (Format.dateTime zone)
        |> Maybe.withDefault "Not recorded"


{-| A refused sign-out means the cookie is still there and the User is still signed in, so
this reports the failure and leaves the button offering the action again.
-}
refusal : SignOut -> Html msg
refusal state =
    case state of
        Refused failure ->
            p
                [ class "notice error signed-out-refusal"
                , attribute "role" "alert"
                ]
                [ text (ApiError.toMessage failure) ]

        Ready ->
            text ""

        SigningOut ->
            text ""
