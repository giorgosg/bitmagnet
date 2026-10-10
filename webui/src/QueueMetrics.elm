module QueueMetrics exposing (Bucket, Event(..), Occurrence, Total, allEvents, beganBefore, eventFromName, eventKey, eventName, occurrences, queues, totals)

{-| What bitmagnet's queue metrics say happened, worked out from its answer: the arithmetic
only, with no view in it. This is where the queue's charts can be wrong without looking wrong,
so it is kept apart and tested on its own.

`queue.metrics` groups the jobs the queue holds by queue, status, the bucket each was created
in and the bucket it last ran in (`internal/metrics/queuemetrics/client.go` in bitmagnet). So
every row is some jobs that are now in one status, and from it come the events the Angular UI
draws (`queue-chart-adapter.timeline.ts`, `queue-metrics.controller.ts`):

  - **created**, in the bucket the jobs were queued in, whatever became of them since;
  - **processed**, in the bucket they last ran in, for jobs that are now processed;
  - **failed**, in the bucket they last ran in, for jobs that are now failed, having run out of
    retries.

A job waiting to be tried again (`retry`) has run and failed, but is neither processed nor failed
yet, so it is created and no more, as the Angular UI has it; it is counted in the totals by
status. A job's status is only its latest, so a job that failed twice and then went through is
processed once, where it went through, and an earlier bucket's failures are not counted.

**bitmagnet's answer is not limited to the timeframe, so the timeframe is applied here.** It
means `startTime` to keep the pending jobs created since then and the other jobs that ran since
then, but it joins its conditions with `AND` and puts no brackets around each `OR` pair, so its
`WHERE` reads `status != 'pending' OR created_at >= ? AND status = 'pending' OR ran_at >= ?`,
and `AND` binds first: **every job that is not pending comes back, whatever the timeframe**, and
whatever `queues` or `statuses` ask for. (bitmagnet `trunk` at `e76818643`; the clause is from
`309e3b892`, "Webui revamp", 2024-10-14.) The Angular UI is not misled because it applies the
timeframe again itself (`queue-metrics.controller.ts`: a pending job by when it was created,
any other by when it ran), and so does this module, both to the events and to the totals.

A bucket that began before the timeframe but reaches into it is kept. bitmagnet cuts its days,
and its hours in a zone that is not a whole number of hours from UTC, in its database's time
zone, so against a database that is not on UTC the request opens part way through one of them,
and what it counts in that bucket is only some of what happened in it (`beganBefore`).

-}

import Buckets
import Dict
import Magnes.Api.Enum.QueueJobStatus as Status exposing (QueueJobStatus)
import StatsControls
import Time


{-| A row of bitmagnet's answer: `count` jobs of `queue`, now in `status`, created in the bucket
that began at `createdAt` and last run in the one that began at `ranAt`, if they have run.
-}
type alias Bucket =
    { queue : String
    , status : QueueJobStatus
    , createdAt : Time.Posix
    , ranAt : Maybe Time.Posix
    , count : Int
    }


type Event
    = Created
    | Processed
    | Failed


{-| In the order of a job's life, which is the order the lines are drawn in.
-}
allEvents : List Event
allEvents =
    [ Created, Processed, Failed ]


{-| The event's name, in the address and on the page.
-}
eventName : Event -> String
eventName event =
    case event of
        Created ->
            "created"

        Processed ->
            "processed"

        Failed ->
            "failed"


eventFromName : String -> Maybe Event
eventFromName name =
    List.filter (\event -> eventName event == name) allEvents |> List.head


{-| The event as something `comparable`, to key counts by: its place in a job's life, so that
keyed counts sort as the lines are drawn.
-}
eventKey : Event -> Int
eventKey event =
    case event of
        Created ->
            0

        Processed ->
            1

        Failed ->
            2


{-| `count` jobs of `queue` that `event` happened to in the bitmagnet bucket that began at `at`.
-}
type alias Occurrence =
    { queue : String
    , event : Event
    , at : Time.Posix
    , count : Int
    }


{-| Whether something in the bucket of `request`'s unit that began at `at` is the timeframe's:
the bucket reaches past the start of it, or there is no start.
-}
inTimeframe : StatsControls.Request -> Time.Posix -> Bool
inTimeframe request at =
    case request.startTime of
        Just start ->
            Buckets.endsAfter request.bucketDuration at start

        Nothing ->
            True


{-| What happened in the timeframe, from an answer to `request`: added up by queue, event and
bucket, in that order. An event in a bucket that was over before the timeframe began happened
before it and is left out; that is every creation of a job queued before the timeframe that ran
inside it, and, since bitmagnet's filter does not hold (see the module's comment), every run of a
job that ran before it.
-}
occurrences : StatsControls.Request -> List Bucket -> List Occurrence
occurrences request buckets =
    let
        happened bucket =
            ( Created, Just bucket.createdAt )
                :: (case bucket.status of
                        Status.Processed ->
                            [ ( Processed, bucket.ranAt ) ]

                        Status.Failed ->
                            [ ( Failed, bucket.ranAt ) ]

                        Status.Retry ->
                            []

                        Status.Pending ->
                            []
                   )
                |> List.filterMap
                    (\( event, at ) ->
                        at
                            |> Maybe.andThen
                                (\moment ->
                                    if inTimeframe request moment then
                                        Just ( ( bucket.queue, eventKey event, Time.posixToMillis moment ), bucket.count )

                                    else
                                        Nothing
                                )
                    )
    in
    buckets
        |> List.concatMap happened
        |> List.foldl (\( key, count ) -> Dict.update key (\seen -> Just (Maybe.withDefault 0 seen + count))) Dict.empty
        |> Dict.toList
        |> List.filterMap
            (\( ( queue, key, at ), count ) ->
                List.filter (\event -> eventKey event == key) allEvents
                    |> List.head
                    |> Maybe.map (\event -> { queue = queue, event = event, at = Time.millisToPosix at, count = count })
            )


{-| The jobs of one queue in the timeframe, by the status they are in now.
-}
type alias Total =
    { queue : String
    , pending : Int
    , retry : Int
    , failed : Int
    , processed : Int
    }


{-| The jobs of the timeframe, from an answer to `request`, by queue and status, the queues by
name. A pending job is the timeframe's if it was queued in it; any other if it last ran in it,
which is the Angular UI's test (`queue-metrics.controller.ts`), with the bucket that reaches into
the timeframe kept, as `occurrences` keeps it. Without a start, it is everything the queue holds,
a job that has no run among it.
-}
totals : StatsControls.Request -> List Bucket -> List Total
totals request buckets =
    buckets
        |> List.filter (windowed request)
        |> List.foldl
            (\bucket ->
                Dict.update bucket.queue
                    (Maybe.withDefault (emptyTotal bucket.queue) >> addTo bucket >> Just)
            )
            Dict.empty
        |> Dict.values


{-| Whether a row is the timeframe's. Without a start every row is, one with no run among them.
-}
windowed : StatsControls.Request -> Bucket -> Bool
windowed request bucket =
    case request.startTime of
        Nothing ->
            True

        Just _ ->
            windowedBy bucket |> Maybe.map (inTimeframe request) |> Maybe.withDefault False


{-| The moment a row is the timeframe's by: when it was queued, if it is pending, and when it
last ran otherwise.
-}
windowedBy : Bucket -> Maybe Time.Posix
windowedBy bucket =
    case bucket.status of
        Status.Pending ->
            Just bucket.createdAt

        Status.Retry ->
            bucket.ranAt

        Status.Failed ->
            bucket.ranAt

        Status.Processed ->
            bucket.ranAt


emptyTotal : String -> Total
emptyTotal queue =
    { queue = queue, pending = 0, retry = 0, failed = 0, processed = 0 }


addTo : Bucket -> Total -> Total
addTo bucket total =
    case bucket.status of
        Status.Pending ->
            { total | pending = total.pending + bucket.count }

        Status.Retry ->
            { total | retry = total.retry + bucket.count }

        Status.Failed ->
            { total | failed = total.failed + bucket.count }

        Status.Processed ->
            { total | processed = total.processed + bucket.count }


{-| Every queue the answer names, by name, whether or not anything of it is the timeframe's:
what there is to choose from.
-}
queues : List Bucket -> List String
queues buckets =
    Dict.keys (Dict.fromList (List.map (\bucket -> ( bucket.queue, () )) buckets))


{-| Whether something kept from the answer to `request` is in a bucket that began before the
timeframe did. That happens against a database not on UTC, where bitmagnet's days (or hours)
begin elsewhere than the request; what it counts in that bucket is only some of what happened in
it, so the first column is not to be read as a whole one.
-}
beganBefore : StatsControls.Request -> List Bucket -> Bool
beganBefore request buckets =
    case request.startTime of
        Nothing ->
            False

        Just start ->
            let
                straddles at =
                    Time.posixToMillis at < Time.posixToMillis start && inTimeframe request at
            in
            List.any (\bucket -> straddles bucket.createdAt || Maybe.map straddles bucket.ranAt == Just True) buckets
