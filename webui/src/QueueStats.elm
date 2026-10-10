module QueueStats exposing
    ( Answer
    , Line
    , Messages
    , Plot
    , State
    , Statistics
    , answer
    , failed
    , fetch
    , loaded
    , plot
    , query
    , view
    )

{-| How bitmagnet's queue has behaved: jobs created, processed and failed per time bucket,
and the jobs of each queue by status.

The counts are bitmagnet's `queue.metrics`, and what they mean is worked out in
`QueueMetrics`, which has no view in it. What is looked at is in the URL
(`Route.QueueStatsParams`); the timeframe, resolution and refresh are `StatsControls`', shared
with the torrent timeline, the buckets are cut up by `Buckets`, and how a look goes is
`StatsLook`'s. This module is what is particular to the queue: the query, the lines and bars
its answer makes, and the page.

bitmagnet is asked about every queue, and the queues and events chosen are picked out of its
answer, so choosing one does not ask again (`Route.sameQuestion`). An answer is worked out once,
when it comes (`answer`: what happened in the timeframe, and the jobs of it by status), and the
charts are drawn from that at view time, with the queues and events the route chooses then.

-}

import ApiError
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
import Html.Attributes exposing (class)
import Magnes.Api.Enum.MetricsBucketDuration exposing (MetricsBucketDuration(..))
import Magnes.Api.InputObject as InputObject
import Magnes.Api.Object
import Magnes.Api.Object.QueueMetricsBucket as MetricsBucket
import Magnes.Api.Object.QueueMetricsQueryResult as MetricsResult
import Magnes.Api.Object.QueueQuery as QueueQuery
import Magnes.Api.Query as Query
import QueueMetrics
import Route
import StatsControls
import StatsLook
import Time


{-| bitmagnet's answer, with when it was asked for and what was asked. The window is read back
from that moment rather than from the clock at the time of drawing, so a chart redrawn later
still ends where its data does.
-}
type alias Statistics =
    { asked : Time.Posix
    , request : StatsControls.Request
    , buckets : List QueueMetrics.Bucket
    }



-- REQUESTS


fetch : String -> Route.QueueStatsParams -> (Result (Graphql.Http.Error Statistics) Statistics -> msg) -> Cmd msg
fetch apiUrl params =
    StatsLook.fetch apiUrl (\now -> query now params)


{-| bitmagnet is asked what `StatsControls.request` says: the unit to bucket by, and where the
first column begins, or nothing for everything. `startTime` is meant to keep the pending jobs
created since then and the jobs that ran since then; it keeps the first, but lets every job that
is not pending through (`QueueMetrics` has why), so the answer is put to the timeframe there.

Neither `queues` nor `statuses` is sent: the queues and events chosen are picked out of the
answer, which is what the queue chips are named from, and neither would hold back a job that is
not pending anyway. No `endTime` is sent, for the reason the torrent timeline sends none: it
would cut off jobs that ran after a browser clock that runs behind the server's. `latency` is
not asked for: it is not shown (ticket 05).

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
        |> SelectionSet.map (\buckets -> { asked = now, request = asking, buckets = buckets })


bucketSelection : SelectionSet QueueMetrics.Bucket Magnes.Api.Object.QueueMetricsBucket
bucketSelection =
    SelectionSet.map5 QueueMetrics.Bucket
        MetricsBucket.queue
        MetricsBucket.status
        MetricsBucket.createdAtBucket
        MetricsBucket.ranAtBucket
        MetricsBucket.count



-- ANSWER


{-| An answer as the page keeps it, worked out once when it comes: what was asked, what
happened in the timeframe, the jobs of the timeframe by queue and status, every queue the answer
names (to choose from), and what was left out with a bucket that began before the timeframe.
-}
type alias Answer =
    { asked : Time.Posix
    , request : StatsControls.Request
    , occurrences : List QueueMetrics.Occurrence
    , totals : List QueueMetrics.Total
    , queues : List String
    , leftOut : { occurrences : List QueueMetrics.Occurrence, totals : List QueueMetrics.Total }
    }


answer : Statistics -> Answer
answer statistics =
    { asked = statistics.asked
    , request = statistics.request
    , occurrences = QueueMetrics.occurrences statistics.request statistics.buckets
    , totals = QueueMetrics.totals statistics.request statistics.buckets
    , queues = QueueMetrics.queues statistics.buckets
    , leftOut = QueueMetrics.leftOut statistics.request statistics.buckets
    }



-- PLOT


{-| One line of the timeline: which count it follows in each column, what it is called, and
how it is drawn. A count is keyed by the queue's group and the event (`QueueMetrics.eventKey`).
-}
type alias Line =
    { key : ( Int, Int )
    , label : String
    , ink : Charts.Ink
    }


{-| The timeline's columns, in time order, and the lines that run through them; and the bars of
the totals, one a queue. `grid` is what the resolution came to, so a page can say what a bucket
is, and `wanted` what it would have come to had a chart been able to draw any number of
columns. `events` are those the lines follow, and `others` names the queues that were added
together into one set of lines. `leftOut` is whether a bucket that began before the timeframe
was left out with something in it that the charts would have shown.
-}
type alias Plot =
    { grid : Buckets.Grid
    , wanted : Buckets.Grid
    , events : List QueueMetrics.Event
    , lines : List Line
    , others : List String
    , slots : List (Buckets.Slot ( Int, Int ))
    , totals : List QueueMetrics.Total
    , leftOut : Bool
    }


{-| The inks of one queue's lines, an ink an event.
-}
type alias EventInks =
    { created : Charts.Ink
    , processed : Charts.Ink
    , failed : Charts.Ink
    }


{-| The first queue's lines: the event says the colour. Failures are in the accent, the one
colour for what wants attention; what was processed is in the text colour; what was created is
grey.
-}
firstInks : EventInks
firstInks =
    { created = Charts.Muted, processed = Charts.Strong, failed = Charts.Accent }


{-| The second queue's: the same colours, held back.
-}
heldBackInks : EventInks
heldBackInks =
    { created = Charts.Faint, processed = Charts.StrongSoft, failed = Charts.AccentSoft }


{-| How each group of lines is inked, by its place in the order the queues go in.
-}
inkSets : List EventInks
inkSets =
    [ firstInks, heldBackInks ]


{-| How many groups of lines there are inks for. While there are no more queues than this, each
is a group of its own; with more, one fewer are, and the rest are added together into the last.
bitmagnet has two queues.
-}
groupLimit : Int
groupLimit =
    List.length inkSets


inkOf : Int -> QueueMetrics.Event -> Charts.Ink
inkOf index event =
    let
        inks =
            List.drop index inkSets |> List.head |> Maybe.withDefault heldBackInks
    in
    case event of
        QueueMetrics.Created ->
            inks.created

        QueueMetrics.Processed ->
            inks.processed

        QueueMetrics.Failed ->
            inks.failed


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

The timeline's lines are for the queues with something on it: by name, or, where some were
chosen, those of them, in the order they were chosen in. Each gets a line for each event chosen
(all three where none was), inked by its place in that order, which the events chosen do not
change, so a queue's ink does not move as events are chosen. A queue with nothing on the
timeline, a chosen one with nothing in the timeframe or one with only jobs waiting to be retried
that were queued before it, takes no place. With more queues than inks the first is drawn apart
and the rest added together as "Other queues", so the chart still adds up to everything counted.
A line for an event that did not happen to a queue that had others happen is drawn along zero;
where nothing chosen happened at all there is no chart.

The totals' bars are for the queues with jobs in the timeframe, in the same order: by name, or
the chosen ones among them.

The timeline's columns are cut from what is drawn, so for everything it begins at the earliest
of that. A timeframe begins at the first bucket that began in it: against a database not on UTC,
the one before it straddled the start and is left out (`QueueMetrics`).

-}
plot : Route.QueueStatsParams -> Answer -> Plot
plot params drawnFrom =
    let
        counted =
            Dict.fromList (List.map (\occurrence -> ( occurrence.queue, () )) drawnFrom.occurrences)

        chosen queue =
            List.isEmpty params.queues || List.member queue params.queues

        inOrder present =
            if List.isEmpty params.queues then
                present

            else
                List.filter (\queue -> List.member queue present) params.queues

        order =
            inOrder (Dict.keys counted)

        groups =
            groupsOf order

        groupOf queue =
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

        drawn occurrence =
            List.member occurrence.event events && chosen occurrence.queue

        samples =
            List.filterMap
                (\occurrence ->
                    if drawn occurrence then
                        groupOf occurrence.queue
                            |> Maybe.map
                                (\index ->
                                    { series = ( index, QueueMetrics.eventKey occurrence.event )
                                    , at = occurrence.at
                                    , count = occurrence.count
                                    }
                                )

                    else
                        Nothing
                )
                drawnFrom.occurrences

        window =
            StatsControls.window drawnFrom.asked params.controls

        resolved =
            Buckets.grid params.controls.resolution window samples

        -- The timeframe as drawn: from the first bucket that began in it.
        drawnWindow =
            case drawnFrom.request.startTime of
                Just start ->
                    { window | from = Just (Buckets.firstBucketFrom resolved start) }

                Nothing ->
                    window
    in
    { grid = resolved
    , wanted = Buckets.unlimited params.controls.resolution window samples
    , events = events
    , lines =
        groups
            |> List.indexedMap Tuple.pair
            |> List.concatMap
                (\( index, group ) ->
                    List.map
                        (\event ->
                            { key = ( index, QueueMetrics.eventKey event )
                            , label = group.name ++ ": " ++ QueueMetrics.eventName event
                            , ink = inkOf index event
                            }
                        )
                        events
                )
    , others =
        if List.length order > groupLimit then
            List.drop (groupLimit - 1) order

        else
            []
    , slots =
        if List.isEmpty samples then
            []

        else
            Buckets.slots resolved drawnWindow samples
    , totals =
        List.filterMap
            (\queue -> List.filter (\total -> total.queue == queue) drawnFrom.totals |> List.head)
            (inOrder (List.map .queue drawnFrom.totals))
    , leftOut =
        List.any drawn drawnFrom.leftOut.occurrences
            || List.any (.queue >> chosen) drawnFrom.leftOut.totals
    }


{-| The sets of lines the queues in `order` are drawn as: each its own, or the first ones each
its own and the rest, however many, one set that adds them up.
-}
groupsOf : List String -> List Group
groupsOf order =
    let
        alone queue =
            { name = queue, members = [ queue ] }
    in
    if List.length order <= groupLimit then
        List.map alone order

    else
        List.map alone (List.take (groupLimit - 1) order)
            ++ [ { name = otherQueues, members = List.drop (groupLimit - 1) order } ]


{-| The totals' bars, a segment a status, in the order of a job's life from the top of a bar
down, so that what was processed, the most of it, is at the foot. Processed and failed are inked
as the first queue's lines of those events are, and waiting in its grey; waiting to be tried
again, which has no line, is the accent as an outline, an ink no line has, since it is a failure
for now.
-}
statusSegments : List (Charts.Series QueueMetrics.Total)
statusSegments =
    [ { label = "pending", value = .pending, ink = Charts.Muted }
    , { label = "retry", value = .retry, ink = Charts.AccentHollow }
    , { label = "failed", value = .failed, ink = Charts.Accent }
    , { label = "processed", value = .processed, ink = Charts.Strong }
    ]



-- STATE


type alias State =
    StatsLook.State Route.QueueStatsParams Answer


loaded : Route.QueueStatsParams -> Statistics -> State -> State
loaded params statistics =
    StatsLook.loaded params (answer statistics)


failed : Route.QueueStatsParams -> ApiError.Failure -> State -> State
failed =
    StatsLook.failed Route.QueueStats



-- VIEW


type alias Messages msg =
    { navigate : Route.QueueStatsParams -> msg
    , refreshRequested : msg
    }


view : Time.Zone -> Messages msg -> Route.QueueStatsParams -> State -> Html msg
view zone messages params state =
    let
        -- Drawn for the question it answered, with the queues and events now chosen.
        drawn =
            StatsLook.shownOf state
                |> Maybe.map (\shown -> ( shown, plot { params | controls = shown.params.controls } shown.answer ))
    in
    div [ class "page queue-stats" ]
        [ h1 [] [ text "Queue statistics" ]
        , choices messages params state drawn
        , StatsLook.view zone
            .asked
            (\_ -> drawn |> Maybe.map (\( shown, plotted ) -> viewCharts zone params shown.answer plotted) |> Maybe.withDefault [])
            state
        ]


choices : Messages msg -> Route.QueueStatsParams -> State -> Maybe ( StatsLook.Shown Route.QueueStatsParams Answer, Plot ) -> Html msg
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

        known =
            state.latest |> Maybe.map .queues |> Maybe.withDefault []
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
                                , onToggle = messages.navigate { params | queues = Chip.toggle queue params.queues }
                                }
                        )
                        (withChosen params.queues known)
                    )
               , Chip.facet "event"
                    (List.map
                        (\event ->
                            Chip.view
                                { label = QueueMetrics.eventName event
                                , count = Nothing
                                , selected = List.member event params.events
                                , onToggle = messages.navigate { params | events = Chip.toggle event params.events }
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


{-| The charts, and how to read what they count.
-}
viewCharts : Time.Zone -> Route.QueueStatsParams -> Answer -> Plot -> List (Html msg)
viewCharts zone params drawnFrom plotted =
    [ Charts.timeline
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
    , StatsControls.capNote plotted
    , viewOthers plotted
    , Charts.stackedBars
        { title = "Jobs by queue and status"
        , description = "Bar chart: the jobs of each queue in the timeframe, by status."
        , categoryHeading = "Queue"
        , category = .queue
        , segments = statusSegments
        }
        plotted.totals
    , if List.isEmpty params.events then
        text ""

      else
        p [ class "stats-note" ]
            [ text
                ("The events chosen narrow the timeline only: the totals are every job of "
                    ++ (if List.isEmpty params.queues then
                            "the timeframe"

                        else
                            "the chosen queues in the timeframe"
                       )
                    ++ ", by status."
                )
            ]
    , if plotted.leftOut then
        let
            bucket =
                case drawnFrom.request.bucketDuration of
                    Day ->
                        "day"

                    Hour ->
                        "hour"

                    Minute ->
                        "minute"
        in
        p [ class "stats-note" ]
            [ text
                ("bitmagnet's time zone began the first "
                    ++ bucket
                    ++ " before the timeframe did, so that "
                    ++ bucket
                    ++ " is left out of both charts, which begin with the first whole one."
                )
            ]

      else
        text ""
    , p [ class "stats-note" ] [ text ("As of " ++ Format.dateTime zone drawnFrom.asked) ]
    , p [ class "stats-note" ]
        [ text "A job is counted as created in the bucket it was queued in, and as processed or failed in the bucket it last ran in; one waiting to be tried again is neither yet. The totals are the jobs of the timeframe by status: those still pending that were queued in it, and the others that last ran in it. bitmagnet deletes a processed or failed job a while after it ran (a week, by default), so a bucket older than that counts only what is left." ]
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


{-| Who "Other queues" are, since a set of lines that adds up several cannot say.
-}
viewOthers : Plot -> Html msg
viewOthers drawn =
    if List.isEmpty drawn.others then
        text ""

    else
        p [ class "stats-note" ] [ text (otherQueues ++ " are " ++ String.join ", " drawn.others ++ ".") ]
