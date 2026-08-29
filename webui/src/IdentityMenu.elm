module IdentityMenu exposing (Destination, Messages, destinations, view)

{-| Who you are, in the header, and where that lets you go.

The destinations are not a second opinion about authorization: each candidate is put
through `Route.guard`, the same function that decides what happens on arrival. A page the
guard would bounce is therefore never offered, and adding a route with a new guard cannot
leave a stale entry behind here.

This is presentation only. bitmagnet enforces; a menu that offered too much would waste a
click, not grant anything.

-}

import Html exposing (Html, a, button, div, li, text, ul)
import Html.Attributes exposing (attribute, class, href, type_)
import Html.Events exposing (onClick)
import Identity
import Route exposing (Route)
import Svg
import Svg.Attributes as SvgAttr


type alias Destination =
    { route : Route
    , label : String
    }


{-| Every page the menu could offer, in the order it offers them: your own first, then the
administration ones. The list is filtered by the guard, so an ordinary User simply does not
see the last three.
-}
candidates : List Destination
candidates =
    [ { route = Route.UserOverview, label = "Your User" }
    , { route = Route.APIKeys, label = "API keys" }
    , { route = Route.AdminUsers, label = "Users" }
    , { route = Route.AdminInvitations, label = "Invitations" }
    , { route = Route.AdminRoles, label = "Roles" }
    ]


destinations : Route.BasePath -> Identity.Identity -> List Destination
destinations mount identity =
    List.filter
        (\destination -> Route.guard mount identity destination.route == Route.Allowed)
        candidates


type alias Messages msg =
    { toggled : msg
    , dismissed : msg
    , signOutRequested : msg
    }


{-| `current` is where the menu is being drawn from, so an Anonymous Identity's way in can
come back to it.
-}
view : Route.BasePath -> Messages msg -> Identity.Identity -> Route -> Bool -> Html msg
view mount messages identity current open =
    case identity of
        Identity.UserAuthenticated user _ ->
            menu mount messages identity user.username open

        Identity.Anonymous _ ->
            signIn mount current

        -- An API-key Identity reports an owning User but is not that User: the guard
        -- refuses it every page below, so it is offered the way in instead.
        Identity.APIKeyAuthenticated _ _ _ ->
            signIn mount current

        -- Nothing is drawn until the server has answered. Guessing would make the header
        -- flicker between two states on every load.
        Identity.Unknown ->
            text ""

        Identity.Failed _ ->
            text ""


{-| A disclosure, not an ARIA menu: the entries are ordinary links, which screen readers
and the keyboard already handle. `role="menu"` would promise arrow-key navigation that
would then have to be implemented, and implemented correctly, to keep the promise.

The icon is `aria-hidden`, and the button is named for the User instead — a picture's own
accessible name is whatever the platform calls that picture, which is not the answer to
"whose menu is this".

-}
menu : Route.BasePath -> Messages msg -> Identity.Identity -> String -> Bool -> Html msg
menu mount messages identity username open =
    div [ class "identity" ]
        (button
            [ type_ "button"
            , class "identity-button"
            , attribute "aria-label" username
            , attribute "aria-haspopup" "true"
            , attribute "aria-expanded"
                (if open then
                    "true"

                 else
                    "false"
                )
            , onClick messages.toggled
            ]
            [ personIcon ]
            :: dropdown mount messages identity open
        )


{-| Nothing at all while closed, so the page behind it is untouched.

The backdrop catches a press anywhere else. It is a backdrop rather than a document
listener because a listener closing on `mousedown` would take the menu's own links out of
the document before their `click` could land, and one closing on `click` would arrive too
late to stop that press reaching whatever the menu was covering.

-}
dropdown : Route.BasePath -> Messages msg -> Identity.Identity -> Bool -> List (Html msg)
dropdown mount messages identity open =
    if not open then
        []

    else
        [ div
            [ class "identity-backdrop"
            , attribute "aria-hidden" "true"
            , onClick messages.dismissed
            ]
            []
        , ul [ class "identity-menu" ]
            (List.map (entry mount) (destinations mount identity)
                ++ [ li [ class "identity-signout" ]
                        [ button
                            [ type_ "button", onClick messages.signOutRequested ]
                            [ text "Sign out" ]
                        ]
                   ]
            )
        ]


{-| Drawn rather than typed. An emoji is painted by the platform's own colour font — 👤
arrives blue whatever the page is — and `currentColor` gives this one the same treatment
as the magnet in the results list: it takes the colour of the text around it, in both
schemes, and dims and brightens with the button it sits in.
-}
personIcon : Html msg
personIcon =
    Svg.svg
        [ SvgAttr.viewBox "0 0 24 24"
        , SvgAttr.width "18"
        , SvgAttr.height "18"
        , SvgAttr.fill "currentColor"
        , attribute "aria-hidden" "true"
        ]
        [ Svg.path
            [ SvgAttr.d "M12 4a4 4 0 1 1 0 8 4 4 0 0 1 0-8Zm0 10c4.4 0 8 2.7 8 6v1H4v-1c0-3.3 3.6-6 8-6Z" ]
            []
        ]


entry : Route.BasePath -> Destination -> Html msg
entry mount destination =
    li [] [ a [ href (Route.toHref mount destination.route) ] [ text destination.label ] ]


{-| The Anonymous form of the same corner: one link, carrying where it was asked from so
signing in returns there. Login and registration are excluded by `Route.returnDestination`
on the way back, so nothing is gained by excluding them here as well — but a return to the
login form would be no return at all, so they are left out anyway.
-}
signIn : Route.BasePath -> Route -> Html msg
signIn mount current =
    a
        [ class "identity-link"
        , href (Route.toHref mount (Route.Login { returnUrl = returnTo mount current }))
        ]
        [ text "Sign in" ]


returnTo : Route.BasePath -> Route -> Maybe String
returnTo mount current =
    case current of
        Route.Login _ ->
            Nothing

        Route.Register _ ->
            Nothing

        Route.NotFound ->
            Nothing

        route ->
            Just (Route.toHref mount route)
