module PaginationTest exposing (suite)

import Expect
import Html exposing (div)
import Html.Attributes as Attributes
import Main
import Test exposing (Test, describe, test)
import Test.Html.Query as Query
import Test.Html.Selector exposing (attribute, class, text)


suite : Test
suite =
    describe "Torrent pagination footer"
        [ test "shows a spinner before the next request starts" <|
            \_ ->
                Main.viewResultList { hasNextPage = True, fetching = False } (div [] [])
                    |> Query.fromHtml
                    |> Query.find [ class "pagination-indicator" ]
                    |> Expect.all
                        [ Query.has [ class "pagination-spinner" ]
                        , Query.has [ text "More results below" ]
                        ]
        , test "keeps the spinner while fetching and announces loading" <|
            \_ ->
                Main.viewResultList { hasNextPage = True, fetching = True } (div [] [])
                    |> Query.fromHtml
                    |> Query.find [ class "pagination-indicator" ]
                    |> Expect.all
                        [ Query.has [ attribute (Attributes.attribute "role" "status") ]
                        , Query.has [ class "pagination-spinner" ]
                        , Query.has [ text "Loading more results…" ]
                        ]
        , test "removes the footer after the final page" <|
            \_ ->
                Main.viewResultList { hasNextPage = False, fetching = False } (div [] [])
                    |> Query.fromHtml
                    |> Query.hasNot [ class "pagination-indicator" ]
        , test "places the footer after the rows inside the scroll container" <|
            \_ ->
                Main.viewResultList { hasNextPage = True, fetching = False } (div [ Attributes.class "torrent-rows" ] [])
                    |> Query.fromHtml
                    |> Query.children []
                    |> Query.index 1
                    |> Query.has [ class "pagination-indicator" ]
        ]
