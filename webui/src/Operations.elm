module Operations exposing (view)

{-| bitmagnet's operational pages, listed on the status page under the health report. The
header's health indicator leads to the status page, so this is how they are reached, by
Anonymous as well as by a User (ticket 03).

Each is put through `Route.guard`, as the Identity menu's destinations are, so a page the
guard would refuse is never offered.

-}

import Html exposing (Html, a, h2, li, nav, text, ul)
import Html.Attributes exposing (attribute, class, href)
import Identity
import Route exposing (Route)


candidates : List { route : Route, label : String }
candidates =
    [ { route = Route.TorrentStats Route.emptyTorrentStats, label = "Torrent statistics" }
    , { route = Route.QueueJobs Route.emptyJobs, label = "Queue jobs" }
    ]


view : Route.BasePath -> Identity.Identity -> Html msg
view mount identity =
    case List.filter (\page -> Route.guard mount identity page.route == Route.Allowed) candidates of
        [] ->
            text ""

        offered ->
            nav [ class "panel health operations", attribute "aria-labelledby" "operations-heading" ]
                [ h2 [ Html.Attributes.id "operations-heading" ] [ text "Operations" ]
                , ul []
                    (List.map
                        (\page -> li [] [ a [ href (Route.toHref mount page.route) ] [ text page.label ] ])
                        offered
                    )
                ]
