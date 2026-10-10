module StatsLookTest exposing (suite)

import ApiError
import Expect
import Html
import Html.Attributes
import Route
import StatsControls exposing (AutoRefresh(..), Timeframe(..))
import StatsLook
import Test exposing (Test, describe, test)
import Test.Html.Query as Query
import Test.Html.Selector as Selector
import Time


suite : Test
suite =
    describe "StatsLook"
        [ describe "timerDue"
            [ test "is a look an idle page that was asked to keep itself fresh makes when its timer fires" <|
                \_ ->
                    StatsLook.timerDue everyTen shown
                        |> Expect.equal True
            , test "is not one a tick that was already on its way makes after refreshing was turned off" <|
                \_ ->
                    StatsLook.timerDue params.controls shown
                        |> Expect.equal False
            , test "waits for a look that is still on its way, and for the first one" <|
                \_ ->
                    ( StatsLook.timerDue everyTen (StatsLook.refreshing shown)
                    , StatsLook.timerDue everyTen StatsLook.empty
                    )
                        |> Expect.equal ( False, False )
            ]
        , describe "answerFor"
            [ test "is the page's look for an answer asked under the epoch now current" <|
                \_ ->
                    StatsLook.answerFor { askedUnder = 3, current = 3 } (Just params)
                        |> Expect.equal (Just params)
            , test "is no look for an answer asked under an older epoch, since a newer look was asked for or the controls moved on" <|
                \_ ->
                    StatsLook.answerFor { askedUnder = 2, current = 3 } (Just params)
                        |> Expect.equal Nothing
            , test "is no look for an answer that comes after the page was left" <|
                \_ ->
                    StatsLook.answerFor { askedUnder = 3, current = 3 } pageLeft
                        |> Expect.equal Nothing
            ]
        , describe "a look that fails"
            [ test "keeps an answer to the same question, with the reason" <|
                \_ ->
                    StatsLook.failed Route.QueueStats { params | queues = [ "a" ] } ApiError.Unreachable (StatsLook.refreshing shown)
                        |> Expect.all
                            [ StatsLook.shownOf >> Maybe.map .answer >> Expect.equal (Just "the answer")
                            , .lastFailure >> Expect.equal (Just ApiError.Unreachable)
                            , .refreshing >> Expect.equal False
                            ]
            , test "drops an answer to another question, and keeps it only to choose from" <|
                \_ ->
                    StatsLook.failed Route.QueueStats aWeek ApiError.Unreachable (StatsLook.refreshing shown)
                        |> Expect.all
                            [ StatsLook.shownOf >> Expect.equal Nothing
                            , .lastFailure >> Expect.equal Nothing
                            , .latest >> Expect.equal (Just "the answer")
                            ]
            , test "is said alone when there was nothing to show" <|
                \_ ->
                    StatsLook.failed Route.QueueStats params ApiError.ServiceUnavailable StatsLook.empty
                        |> Expect.all
                            [ StatsLook.shownOf >> Expect.equal Nothing
                            , .refreshing >> Expect.equal False
                            ]
            ]
        , describe "a look that comes"
            [ test "replaces what was shown and what failed" <|
                \_ ->
                    StatsLook.failed Route.QueueStats params ApiError.Unreachable shown
                        |> StatsLook.loaded aWeek "a newer answer"
                        |> Expect.all
                            [ StatsLook.shownOf >> Expect.equal (Just { params = aWeek, answer = "a newer answer" })
                            , .lastFailure >> Expect.equal Nothing
                            , .latest >> Expect.equal (Just "a newer answer")
                            ]
            ]
        , describe "view"
            [ test "says it is loading" <|
                \_ ->
                    viewed StatsLook.empty
                        |> Query.has [ Selector.text "Loading statistics…" ]
            , test "says why a look failed, alone, when there is nothing to show" <|
                \_ ->
                    viewed (StatsLook.failed Route.QueueStats params ApiError.ServiceUnavailable StatsLook.empty)
                        |> Expect.all
                            [ Query.find [ Selector.attribute (Html.Attributes.attribute "role" "alert") ]
                                >> Query.has [ Selector.text (ApiError.toMessage ApiError.ServiceUnavailable) ]
                            , Query.hasNot [ Selector.text "the answer" ]
                            ]
            , test "draws the answer, dimmed and busy while another look is on its way" <|
                \_ ->
                    viewed (StatsLook.refreshing shown)
                        |> Query.find [ Selector.class "stats-refreshing" ]
                        |> Query.has [ Selector.attribute (Html.Attributes.attribute "aria-busy" "true"), Selector.text "the answer" ]
            , test "keeps the answer under the reason a look over it failed, and says as of when" <|
                \_ ->
                    viewed (StatsLook.failed Route.QueueStats params ApiError.Unreachable shown)
                        |> Expect.all
                            [ Query.find [ Selector.attribute (Html.Attributes.attribute "role" "alert") ]
                                >> Query.has [ Selector.text (ApiError.toMessage ApiError.Unreachable), Selector.text "2026-10-10 10:00" ]
                            , Query.has [ Selector.text "the answer" ]
                            ]
            ]
        ]


{-| What `Main` finds on a route that is not the page's any more: no look of it.
-}
pageLeft : Maybe Route.QueueStatsParams
pageLeft =
    Nothing


params : Route.QueueStatsParams
params =
    Route.emptyQueueStats


aWeek : Route.QueueStatsParams
aWeek =
    let
        controls =
            params.controls
    in
    { params | controls = { controls | timeframe = Weeks1 } }


everyTen : StatsControls.Controls
everyTen =
    let
        controls =
            params.controls
    in
    { controls | refresh = Every10Seconds }


shown : StatsLook.State Route.QueueStatsParams String
shown =
    StatsLook.loaded params "the answer" StatsLook.empty


viewed : StatsLook.State Route.QueueStatsParams String -> Query.Single msg
viewed state =
    Html.div [] [ StatsLook.view Time.utc (always (Time.millisToPosix 1791626400000)) (\look -> [ Html.text look.answer ]) state ]
        |> Query.fromHtml
