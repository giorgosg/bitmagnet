module Chip exposing (facet, view)

{-| The toggle a filter value is drawn as, and the labelled row of them a facet is: the
search's filters and the queue's jobs both use them, so a chosen value looks and is read
out the same way on either page.
-}

import Format
import Html exposing (Html, button, div, span, text)
import Html.Attributes exposing (attribute, class, classList, type_)
import Html.Events exposing (onClick)


{-| A real button with `aria-pressed`, so a screen reader hears it as a toggle and whether
it is on. `count` is the number of matches choosing it would give, where one is known.
-}
view : { label : String, count : Maybe Int, selected : Bool, onToggle : msg } -> Html msg
view { label, count, selected, onToggle } =
    button
        [ class "chip"
        , classList [ ( "on", selected ) ]
        , type_ "button"
        , attribute "aria-pressed"
            (if selected then
                "true"

             else
                "false"
            )
        , onClick onToggle
        ]
        (text label
            :: (case count of
                    Just n ->
                        [ span [ class "chip-count" ] [ text (Format.count n) ] ]

                    Nothing ->
                        []
               )
        )


{-| Nothing at all when there is nothing to choose, rather than a label with no chips.
-}
facet : String -> List (Html msg) -> Html msg
facet label chips =
    if List.isEmpty chips then
        text ""

    else
        div [ class "facet" ]
            [ span [ class "facet-label" ] [ text label ]
            , div [ class "chips" ] chips
            ]
