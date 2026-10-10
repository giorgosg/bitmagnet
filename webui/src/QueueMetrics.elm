module QueueMetrics exposing (Bucket, Event(..), Occurrence, Total, allEvents, eventFromName, eventName, occurrences, totals)

{-| What bitmagnet's queue metrics say happened, worked out from its answer: the arithmetic
only, with no view in it. This is where the queue's chart can be wrong without looking wrong,
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

bitmagnet answers with the jobs queued in the timeframe that are still pending, and the jobs
that ran in it (`ran_at >= startTime`, whenever they were queued). So a job queued before the
timeframe can be in the answer, with a creation that is not the timeframe's: such a creation is
left out here, and its run is not.

-}

import Buckets
import Dict
import Magnes.Api.Enum.MetricsBucketDuration exposing (MetricsBucketDuration)
import Magnes.Api.Enum.QueueJobStatus as Status exposing (QueueJobStatus)
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


{-| `count` jobs of `queue` that `event` happened to in the bitmagnet bucket that began at `at`.
-}
type alias Occurrence =
    { queue : String
    , event : Event
    , at : Time.Posix
    , count : Int
    }


{-| What happened, from an answer to a request asked as `StatsControls.request` said: added up
by queue, event and bucket, in that order. An event in a bucket that was over before the request's
`startTime` happened before the timeframe and is left out. One in a bucket that began before it
and reaches into it is kept, as bitmagnet counted it: in a time zone that is not UTC, its days
(and half-hour hours) begin elsewhere than the request does.
-}
occurrences : { bucketDuration : MetricsBucketDuration, startTime : Maybe Time.Posix } -> List Bucket -> List Occurrence
occurrences asked buckets =
    let
        inTimeframe at =
            case asked.startTime of
                Just start ->
                    Buckets.endsAfter asked.bucketDuration at start

                Nothing ->
                    True

        happened bucket =
            ( Created, Just bucket.createdAt )
                :: (case bucket.status of
                        Status.Processed ->
                            [ ( Processed, bucket.ranAt ) ]

                        Status.Failed ->
                            [ ( Failed, bucket.ranAt ) ]

                        _ ->
                            []
                   )
                |> List.filterMap
                    (\( event, at ) ->
                        at
                            |> Maybe.andThen
                                (\moment ->
                                    if inTimeframe moment then
                                        Just ( ( bucket.queue, rank event, Time.posixToMillis moment ), bucket.count )

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
            (\( ( queue, eventRank, at ), count ) ->
                List.drop eventRank allEvents
                    |> List.head
                    |> Maybe.map (\event -> { queue = queue, event = event, at = Time.millisToPosix at, count = count })
            )


rank : Event -> Int
rank event =
    case event of
        Created ->
            0

        Processed ->
            1

        Failed ->
            2


{-| The jobs of one queue in the answer, by the status they are in now.
-}
type alias Total =
    { queue : String
    , pending : Int
    , retry : Int
    , failed : Int
    , processed : Int
    }


{-| Every job in the answer, by queue and status, the queues by name. bitmagnet's answer is
already the timeframe's: the jobs queued in it that are still pending, and the jobs that ran in
it, by what became of them.
-}
totals : List Bucket -> List Total
totals buckets =
    buckets
        |> List.foldl
            (\bucket ->
                Dict.update bucket.queue
                    (Maybe.withDefault (emptyTotal bucket.queue) >> addTo bucket >> Just)
            )
            Dict.empty
        |> Dict.values


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
