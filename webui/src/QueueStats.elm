module QueueStats exposing
    ( Line
    , Messages
    , Plot
    , Shown
    , State
    , Statistics
    , empty
    , failed
    , fetch
    , loaded
    , plot
    , query
    , refreshing
    , timerDue
    , view
    )

{-| How bitmagnet's queue has behaved: jobs created, processed and failed per time bucket,
and the jobs of each queue by status.

The counts are bitmagnet's `queue.metrics`, and what they mean is worked out in
`QueueMetrics`, which has no view in it. What is looked at is in the URL
(`Route.QueueStatsParams`); the timeframe, resolution and refresh are `StatsControls`', shared
with the torrent timeline, and the buckets are cut up by `Buckets`. This module is what is
particular to the queue: the query, the lines and bars its answer makes, and the page.

bitmagnet is asked about every queue, and the queues and events chosen are picked out of its
answer, so choosing one does not ask again (`Route.question`). The chart on screen is therefore
drawn as it is shown, from the answer it was given and the choices in force.

-}

import ApiError
import Bitmagnet
import Buckets
import Charts
import Chip
import Dict
import Format
import Graphql.Http
import Graphql.Operation exposing (RootQuery)
import Graphql.OptionalArgument
import Graphql.SelectionSet as SelectionSet exposing (SelectionSet)
import Html exposing (Html, div, h1, p, text)
import Html.Attributes exposing (attribute, class, classList)
import Magnes.Api.InputObject as InputObject
import Magnes.Api.Object
import Magnes.Api.Object.QueueMetricsBucket as MetricsBucket
import Magnes.Api.Object.QueueMetricsQueryResult as MetricsResult
import Magnes.Api.Object.QueueQuery as QueueQuery
import Magnes.Api.Query as Query
import QueueMetrics
import Route
import StatsControls
import Task
import Time


{-| bitmagnet's answer, with when it was asked for. The window is read back from that moment
rather than from the clock at the time of drawing, so a chart redrawn later still ends where
its data does.
-}
type alias Statistics =
    { asked : Time.Posix
    , buckets : List QueueMetrics.Bucket
    }



-- REQUESTS


{-| The clock is read as the request is made, so the start of the timeframe and the end of
the chart are the same moment, and the answer carries it.
-}
fetch : String -> Route.QueueStatsParams -> (Result (Graphql.Http.Error Statistics) Statistics -> msg) -> Cmd msg
fetch apiUrl params toMsg =
    Time.now
        |> Task.andThen
            (\now ->
                query now params
                    |> Bitmagnet.queryRequest apiUrl
                    |> Graphql.Http.withTimeout StatsControls.requestTimeout
                    |> Graphql.Http.toTask
            )
        |> Task.attempt toMsg


{-| bitmagnet is asked what `StatsControls.request` says: the unit to bucket by, and where the
first column begins, or nothing for everything. `startTime` takes in the jobs created since
then that are still pending, and the jobs that ran since then, whenever they were created.

Neither `queues` nor `statuses` is sent: the queues and events chosen are picked out of the
answer, which is what the queue chips are named from. No `endTime` is sent, for the reason the
torrent timeline sends none: it would cut off jobs that ran after a browser clock that runs
behind the server's. `latency` is not asked for: it is not shown (ticket 05).

-}
query : Time.Posix -> Route.QueueStatsParams -> SelectionSet Statistics RootQuery
query now params =
    let
        asking =
            StatsControls.request now params.controls

        input =
            InputObject.buildQueueMetricsQueryInput
                { bucketDuration = asking.bucketDuration }
                (\optionals -> { optionals | startTime = Graphql.OptionalArgument.fromMaybe asking.startTime })
    in
    Query.queue (QueueQuery.metrics { input = input } (MetricsResult.buckets bucketSelection))
        |> SelectionSet.map (\buckets -> { asked = now, buckets = buckets })


bucketSelection : SelectionSet QueueMetrics.Bucket Magnes.Api.Object.QueueMetricsBucket
bucketSelection =
    SelectionSet.map5 QueueMetrics.Bucket
        MetricsBucket.queue
        MetricsBucket.status
        MetricsBucket.createdAtBucket
        MetricsBucket.ranAtBucket
        MetricsBucket.count



-- PLOT


{-| One line of the timeline: which count it follows in each column, what it is called, and
how it is drawn. A count is keyed by the queue's place among the lines and the event's name.
-}
type alias Line =
    { key : ( Int, String )
    , label : String
    , ink : Charts.Ink
    }


{-| The timeline's columns, in time order, and the lines that run through them; and the bars of
the totals, one a queue. `grid` is what the resolution came to, so a page can say what a bucket
is, and `wanted` what it would have come to had a chart been able to draw any number of
columns. `events` are those the lines follow, and `others` names the queues that were added
together into one set of lines.
-}
type alias Plot =
    { grid : Buckets.Grid
    , wanted : Buckets.Grid
    , events : List QueueMetrics.Event
    , lines : List Line
    , others : List String
    , slots : List (Buckets.Slot ( Int, String ))
    , totals : List QueueMetrics.Total
    }


{-| How a queue's lines are inked, by its place in the order the queues go in: the event says
the colour, and the place says whether it is held back. Failures are in the accent, the one
colour for what wants attention; what was processed is in the text colour; what was created is
grey. The status a job ends in keeps its colour in the totals (`statusSegments`).
-}
inkOf : Int -> QueueMetrics.Event -> Charts.Ink
inkOf place event =
    case ( place, event ) of
        ( 0, QueueMetrics.Created ) ->
            Charts.Muted

        ( 0, QueueMetrics.Processed ) ->
            Charts.Strong

        ( 0, QueueMetrics.Failed ) ->
            Charts.Accent

        ( _, QueueMetrics.Created ) ->
            Charts.Faint

        ( _, QueueMetrics.Processed ) ->
            Charts.StrongSoft

        ( _, QueueMetrics.Failed ) ->
            Charts.AccentSoft


{-| How many queues are drawn apart: a queue is a set of lines in the inks of its place, and
there are two such sets. bitmagnet has two queues.
-}
largestGroups : Int
largestGroups =
    2


{-| The name of the set of lines that adds up the queues there are no more inks for.
-}
otherQueues : String
otherQueues =
    "Other queues"


{-| One set of lines, and the queues counted in it: one, or the rest.
-}
type alias Group =
    { name : String
    , members : List String
    }


{-| The charts of an answer, with the choices in force.

Each queue with anything on the timeline gets a line for each event chosen (all three where
none was), inked by its place in the order the queues go in: by name, or the order they were
chosen in, where some were. A queue's ink is its place's, not a matter of which others have
anything counted. With more queues than inks the first is drawn apart and the rest added
together as "Other queues", so the chart still adds up to everything bitmagnet counted. A line
for an event that did not happen to a queue that had others happen is drawn along zero; where
nothing chosen happened at all there is no chart.

The timeline's columns are cut from what is drawn, so for everything it begins at the earliest
of that.

-}
plot : Route.QueueStatsParams -> Statistics -> Plot
plot params statistics =
    let
        happened =
            QueueMetrics.occurrences (StatsControls.request statistics.asked params.controls) statistics.buckets

        totals =
            QueueMetrics.totals statistics.buckets

        order =
            if List.isEmpty params.queues then
                List.map .queue totals

            else
                params.queues

        groups =
            groupsOf order

        placeOf queue =
            groups
                |> List.indexedMap Tuple.pair
                |> List.filter (\( _, group ) -> List.member queue group.members)
                |> List.head
                |> Maybe.map Tuple.first

        events =
            if List.isEmpty params.events then
                QueueMetrics.allEvents

            else
                List.filter (\event -> List.member event params.events) QueueMetrics.allEvents

        samples =
            List.filterMap
                (\occurrence ->
                    if List.member occurrence.event events then
                        placeOf occurrence.queue
                            |> Maybe.map
                                (\place ->
                                    { series = ( place, QueueMetrics.eventName occurrence.event )
                                    , at = occurrence.at
                                    , count = occurrence.count
                                    }
                                )

                    else
                        Nothing
                )
                happened

        onTimeline =
            Dict.fromList (List.map (\occurrence -> ( occurrence.queue, () )) happened)

        hasCounts group =
            List.any (\member -> Dict.member member onTimeline) group.members

        window =
            StatsControls.window statistics.asked params.controls

        resolved =
            Buckets.grid params.controls.resolution window samples
    in
    { grid = resolved
    , wanted = Buckets.unlimited params.controls.resolution window samples
    , events = events
    , lines =
        groups
            |> List.indexedMap Tuple.pair
            |> List.filter (Tuple.second >> hasCounts)
            |> List.concatMap
                (\( place, group ) ->
                    List.map
                        (\event ->
                            { key = ( place, QueueMetrics.eventName event )
                            , label = group.name ++ ": " ++ QueueMetrics.eventName event
                            , ink = inkOf place event
                            }
                        )
                        events
                )
    , others =
        if List.length order > largestGroups then
            List.drop (largestGroups - 1) order
                |> List.filter (\queue -> Dict.member queue onTimeline)

        else
            []
    , slots =
        if List.isEmpty samples then
            []

        else
            Buckets.slots resolved window samples
    , totals =
        List.filterMap
            (\queue -> List.filter (\total -> total.queue == queue) totals |> List.head)
            order
    }


{-| The sets of lines the queues in `order` are drawn as: each its own, or the first its own and
the rest, however many, one set that adds them up.
-}
groupsOf : List String -> List Group
groupsOf order =
    let
        alone queue =
            { name = queue, members = [ queue ] }
    in
    if List.length order <= largestGroups then
        List.map alone order

    else
        List.map alone (List.take (largestGroups - 1) order)
            ++ [ { name = otherQueues, members = List.drop (largestGroups - 1) order } ]


{-| The totals' bars, a segment a status, in the order of a job's life from the top of a bar
down, so that what was processed, the most of it, is at the foot. A status is inked as the line
of its event is: failed in the accent, processed in the text colour, waiting in grey, and
waiting to be tried again in the accent held back, since it is a failure for now.
-}
statusSegments : List (Charts.Series QueueMetrics.Total)
statusSegments =
    [ { label = "pending", value = .pending, ink = Charts.Muted }
    , { label = "retry", value = .retry, ink = Charts.AccentSoft }
    , { label = "failed", value = .failed, ink = Charts.Accent }
    , { label = "processed", value = .processed, ink = Charts.Strong }
    ]



-- STATE


{-| What is on screen: an answer, with the choices it was asked for. What it is drawn as also
takes the queues and events now chosen, which are picked out of it.
-}
type alias Shown =
    { params : Route.QueueStatsParams
    , statistics : Statistics
    }


type Listing
    = Loading
    | Failed ApiError.Failure
    | Loaded Shown


{-| `refreshing` is set while another look is on its way over one already shown, which stays
on screen, quieter, until it arrives. `lastFailure` is a look that did not arrive over a chart
asked the same question: the chart stays, with the reason, because a refresh that fails is not a
reason to take away what was there.

`queues` are those of the last answer, kept across looks that fail, so the queue chips stay.

-}
type alias State =
    { listing : Listing
    , refreshing : Bool
    , lastFailure : Maybe ApiError.Failure
    , queues : List String
    }


empty : State
empty =
    { listing = Loading, refreshing = False, lastFailure = Nothing, queues = [] }


{-| Another look has been asked for.
-}
refreshing : State -> State
refreshing state =
    { state | refreshing = True }


{-| Whether an answer is still to come, so a timer does not ask again over the top of it.
-}
inFlight : State -> Bool
inFlight state =
    case state.listing of
        Loading ->
            True

        _ ->
            state.refreshing


{-| Whether the page's timer firing is to be a look: it was asked to keep itself fresh, and no
look is on its way. A tick can already be on its way when refreshing is turned off, and fires
once more, so the timer's own word that it was due is not enough.
-}
timerDue : Route.QueueStatsParams -> State -> Bool
timerDue params state =
    Route.refreshInterval (Route.QueueStats params) /= Nothing && not (inFlight state)


loaded : Route.QueueStatsParams -> Statistics -> State -> State
loaded params statistics state =
    { state
        | listing = Loaded { params = params, statistics = statistics }
        , refreshing = False
        , lastFailure = Nothing
        , queues = List.map .queue (QueueMetrics.totals statistics.buckets)
    }


{-| A look that did not come, asked for under `params`. A chart that answered the same question
stays with the reason, so that a poll that fails does not take it away. One that answered
another would be left under chips that are not its own, with a heading that says something
else, so it goes, and the reason is shown alone.
-}
failed : Route.QueueStatsParams -> ApiError.Failure -> State -> State
failed params failure state =
    case state.listing of
        Loaded shown ->
            if Route.question (Route.QueueStats shown.params) == Route.question (Route.QueueStats params) then
                { state | refreshing = False, lastFailure = Just failure }

            else
                { state | listing = Failed failure, refreshing = False, lastFailure = Nothing }

        _ ->
            { state | listing = Failed failure, refreshing = False }



-- VIEW


type alias Messages msg =
    { navigate : Route.QueueStatsParams -> msg
    , refreshRequested : msg
    }


view : Time.Zone -> Messages msg -> Route.QueueStatsParams -> State -> Html msg
view zone messages params state =
    div [ class "page queue-stats" ]
        (h1 [] [ text "Queue statistics" ]
            :: (case state.listing of
                    Loading ->
                        [ choices messages params state Nothing
                        , p [ class "notice" ] [ text "Loading statistics…" ]
                        ]

                    Failed failure ->
                        [ choices messages params state Nothing
                        , p [ class "notice error", attribute "role" "alert" ] [ text (ApiError.toMessage failure) ]
                        ]

                    Loaded shown ->
                        let
                            -- Drawn for the question it answered, with the queues and events now chosen.
                            plotted =
                                plot { params | controls = shown.params.controls } shown.statistics
                        in
                        [ choices messages params state (Just ( shown, plotted ))
                        , viewShown zone state shown plotted
                        ]
               )
        )


choices : Messages msg -> Route.QueueStatsParams -> State -> Maybe ( Shown, Plot ) -> Html msg
choices messages params state drawn =
    let
        navigate controlsChosen =
            messages.navigate { params | controls = controlsChosen }

        -- The multiplier the chart on screen came to, in the unit now chosen: a chart that was
        -- drawn for another unit says nothing of this one.
        picked =
            drawn
                |> Maybe.andThen
                    (\( shown, plotted ) ->
                        if shown.params.controls.resolution.unit == params.controls.resolution.unit then
                            Just (Buckets.widthIn params.controls.resolution.unit plotted.grid)

                        else
                            Nothing
                    )
    in
    div [ class "facets stats-controls" ]
        (StatsControls.rows
            { timeframes = StatsControls.allTimeframes
            , picked = picked
            , change = navigate
            , refreshRequested = messages.refreshRequested
            }
            params.controls
            ++ [ Chip.facet "queue"
                    (List.map
                        (\queue ->
                            Chip.view
                                { label = queue
                                , count = Nothing
                                , selected = List.member queue params.queues
                                , onToggle = messages.navigate { params | queues = toggleIn queue params.queues }
                                }
                        )
                        (withChosen params.queues state.queues)
                    )
               , Chip.facet "event"
                    (List.map
                        (\event ->
                            Chip.view
                                { label = QueueMetrics.eventName event
                                , count = Nothing
                                , selected = List.member event params.events
                                , onToggle = messages.navigate { params | events = toggleIn event params.events }
                                }
                        )
                        QueueMetrics.allEvents
                    )
               ]
        )


{-| A chosen queue stays offered when the answer has none of it, so a link naming one can
still be undone.
-}
withChosen : List String -> List String -> List String
withChosen chosen known =
    known ++ List.filter (\queue -> not (List.member queue known)) chosen


toggleIn : a -> List a -> List a
toggleIn value values =
    if List.member value values then
        List.filter ((/=) value) values

    else
        values ++ [ value ]


{-| The charts, dimmed while another look is on its way, with the reason when a look did not
come, and how to read what they count.
-}
viewShown : Time.Zone -> State -> Shown -> Plot -> Html msg
viewShown zone state shown plotted =
    div
        [ classList [ ( "stats", True ), ( "stats-refreshing", state.refreshing ) ]
        , attribute "aria-busy"
            (if state.refreshing then
                "true"

             else
                "false"
            )
        ]
        [ case state.lastFailure of
            Just failure ->
                p [ class "notice error", attribute "role" "alert" ]
                    [ text
                        (ApiError.toMessage failure
                            ++ " Showing the answer as of "
                            ++ Format.dateTime zone shown.statistics.asked
                            ++ "."
                        )
                    ]

            Nothing ->
                text ""
        , Charts.timeline
            { title = "Jobs per " ++ Buckets.label plotted.grid
            , description = "Line chart: jobs " ++ eventsSaid plotted ++ " per " ++ Buckets.label plotted.grid ++ ", by queue."
            , zone = zone
            , time = .start
            , series =
                List.map
                    (\line ->
                        { label = line.label
                        , value = \slot -> Dict.get line.key slot.counts |> Maybe.withDefault 0
                        , ink = line.ink
                        }
                    )
                    plotted.lines
            }
            plotted.slots
        , viewCapNote plotted
        , viewOthers plotted
        , Charts.stackedBars
            { title = "Jobs by queue and status"
            , description = "Bar chart: the jobs of each queue in the timeframe, by status."
            , categoryHeading = "Queue"
            , category = .queue
            , segments = statusSegments
            }
            plotted.totals
        , p [ class "stats-note" ] [ text ("As of " ++ Format.dateTime zone shown.statistics.asked) ]
        , p [ class "stats-note" ]
            [ text "A job is counted as created in the bucket it was queued in, and as processed or failed in the bucket it last ran in; one waiting to be tried again is neither yet. The totals are the jobs queued in the timeframe that are still pending, and the jobs that ran in it, by status. bitmagnet deletes a processed or failed job a while after it ran (a week, by default), so a bucket older than that counts only what is left." ]
        ]


{-| The events the lines follow, for the chart's description: "created, processed and failed".
-}
eventsSaid : Plot -> String
eventsSaid plotted =
    case List.reverse (List.map QueueMetrics.eventName plotted.events) of
        [] ->
            "counted"

        [ only ] ->
            only

        last :: rest ->
            String.join ", " (List.reverse rest) ++ " and " ++ last


{-| Said whenever the chart was cut down to fit, however the multiplier came about, of the
chart that was drawn and not of the choices since made.
-}
viewCapNote : Plot -> Html msg
viewCapNote drawn =
    if drawn.grid.unit == drawn.wanted.unit && drawn.grid.every == drawn.wanted.every then
        text ""

    else
        p [ class "stats-note" ]
            [ text
                ("Drawn per "
                    ++ Buckets.label drawn.grid
                    ++ ", not per "
                    ++ Buckets.label drawn.wanted
                    ++ ": that many buckets are more than the chart can draw."
                )
            ]


{-| Who "Other queues" are, since a set of lines that adds up several cannot say.
-}
viewOthers : Plot -> Html msg
viewOthers drawn =
    if List.isEmpty drawn.others then
        text ""

    else
        p [ class "stats-note" ] [ text (otherQueues ++ " are " ++ String.join ", " drawn.others ++ ".") ]
