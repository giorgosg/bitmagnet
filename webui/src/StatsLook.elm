module StatsLook exposing (Listing, Shown, State, answerFor, empty, failed, fetch, loaded, refreshing, shownOf, timerDue, view)

{-| The looks a statistics page takes at bitmagnet: the answer on screen, with the question it
answered; another look on its way over it; and a look that did not come. The torrent timeline
and the queue's statistics differ in what they ask and what they draw, not in how a look goes,
so this is one module for both, over the page's own `params` and `answer`.

Which look is the newest is `Main`'s to say: every look and every change of route moves its
epoch on, and an answer asked under an older one is dropped before it reaches here.

-}

import ApiError
import Bitmagnet
import Format
import Graphql.Http
import Graphql.Operation exposing (RootQuery)
import Graphql.SelectionSet exposing (SelectionSet)
import Html exposing (Html, div, p, text)
import Html.Attributes exposing (attribute, class, classList)
import Route
import StatsControls
import Task
import Time


{-| An answer, and the choices of the look it answered.
-}
type alias Shown params answer =
    { params : params
    , answer : answer
    }


type Listing params answer
    = Loading
    | Failed ApiError.Failure
    | Loaded (Shown params answer)


{-| `refreshing` is set while another look is on its way over one already shown, which stays on
screen, quieter, until it arrives. `lastFailure` is a look that did not arrive over an answer to
the same question: the answer stays, with the reason, because a refresh that fails is not a
reason to take away what was there.

`latest` is the last answer that came, kept across looks that fail, so that what a page offers
to choose from (the torrent sources by name, the queues) stays while the reason a look failed
is shown instead of a chart.

-}
type alias State params answer =
    { listing : Listing params answer
    , refreshing : Bool
    , lastFailure : Maybe ApiError.Failure
    , latest : Maybe answer
    }


empty : State params answer
empty =
    { listing = Loading, refreshing = False, lastFailure = Nothing, latest = Nothing }


{-| Another look has been asked for.
-}
refreshing : State params answer -> State params answer
refreshing state =
    { state | refreshing = True }


{-| Whether an answer is still to come, so a timer does not ask again over the top of it.
-}
inFlight : State params answer -> Bool
inFlight state =
    case state.listing of
        Loading ->
            True

        _ ->
            state.refreshing


{-| Whether the page's timer firing is to be a look: the controls ask it to keep itself fresh,
and no look is on its way. A tick can already be on its way when refreshing is turned off, and
fires once more, so the timer's own word that it was due is not enough.
-}
timerDue : StatsControls.Controls -> State params answer -> Bool
timerDue controls state =
    StatsControls.refreshMillis controls.refresh /= Nothing && not (inFlight state)


shownOf : State params answer -> Maybe (Shown params answer)
shownOf state =
    case state.listing of
        Loaded shown ->
            Just shown

        _ ->
            Nothing


loaded : params -> answer -> State params answer -> State params answer
loaded params answer state =
    { state
        | listing = Loaded { params = params, answer = answer }
        , refreshing = False
        , lastFailure = Nothing
        , latest = Just answer
    }


{-| A look that did not come, asked for under `params`, which `toRoute` makes the page's route
of. An answer to the same question stays with the reason (`Route.sameQuestion`), so that a poll
that fails does not take it away. One to another question would be left under chips that are
not its own, with a heading that says something else, so it goes, and the reason is shown
alone.
-}
failed : (params -> Route.Route) -> params -> ApiError.Failure -> State params answer -> State params answer
failed toRoute params failure state =
    case state.listing of
        Loaded shown ->
            if Route.sameQuestion (toRoute shown.params) (toRoute params) then
                { state | refreshing = False, lastFailure = Just failure }

            else
                { state | listing = Failed failure, refreshing = False, lastFailure = Nothing }

        _ ->
            { state | listing = Failed failure, refreshing = False }


{-| The look an answer is for, if it is still the page's: `page` is the look of the route now
shown, if that is the page's route, and the answer must have been asked under the epoch now
current. Every look, and every change of route, moves `Main`'s epoch on, so an answer asked under
an older one came after the controls moved on or a newer look was asked for, and is dropped.
-}
answerFor : { askedUnder : Int, current : Int } -> Maybe params -> Maybe params
answerFor epochs page =
    if epochs.askedUnder == epochs.current then
        page

    else
        Nothing


{-| Asks `query` of bitmagnet, as a query that can take it a while (`Bitmagnet.slowQueryRequest`).
The clock is read as the request is made and handed to `query`, so the start of the timeframe
and the end of the chart are the same moment, and the answer can carry it.
-}
fetch : String -> (Time.Posix -> SelectionSet answer RootQuery) -> (Result (Graphql.Http.Error answer) answer -> msg) -> Cmd msg
fetch apiUrl query toMsg =
    Time.now
        |> Task.andThen
            (\now ->
                query now
                    |> Bitmagnet.slowQueryRequest apiUrl
                    |> Graphql.Http.toTask
            )
        |> Task.attempt toMsg


{-| What a page shows of its looks: that one is coming, why one failed, or what `draw` makes of
the answer, dimmed and busy while another look is on its way, with the reason and the time of
the answer (`askedOf`) when a look over it failed.
-}
view : Time.Zone -> (answer -> Time.Posix) -> (Shown params answer -> List (Html msg)) -> State params answer -> Html msg
view zone askedOf draw state =
    case state.listing of
        Loading ->
            p [ class "notice" ] [ text "Loading statistics…" ]

        Failed failure ->
            p [ class "notice error", attribute "role" "alert" ] [ text (ApiError.toMessage failure) ]

        Loaded shown ->
            div
                [ classList [ ( "stats", True ), ( "stats-refreshing", state.refreshing ) ]
                , attribute "aria-busy"
                    (if state.refreshing then
                        "true"

                     else
                        "false"
                    )
                ]
                ((case state.lastFailure of
                    Just failure ->
                        p [ class "notice error", attribute "role" "alert" ]
                            [ text
                                (ApiError.toMessage failure
                                    ++ " Showing the answer as of "
                                    ++ Format.dateTime zone (askedOf shown.answer)
                                    ++ "."
                                )
                            ]

                    Nothing ->
                        text ""
                 )
                    :: draw shown
                )
