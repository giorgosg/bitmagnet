module QueueMetricsTest exposing (suite)

import Expect
import Magnes.Api.Enum.MetricsBucketDuration exposing (MetricsBucketDuration(..))
import Magnes.Api.Enum.QueueJobStatus as Status exposing (QueueJobStatus)
import QueueMetrics exposing (Event(..))
import Test exposing (Test, describe, test)
import Time


suite : Test
suite =
    describe "QueueMetrics"
        [ describe "occurrences"
            [ test "are none when the queue answered with nothing" <|
                \_ ->
                    QueueMetrics.occurrences lastDay []
                        |> Expect.equal []
            , test "count a job waiting to run as created, in the bucket it was queued in" <|
                \_ ->
                    QueueMetrics.occurrences lastDay [ job "process_torrent" Status.Pending -120 Nothing 3 ]
                        |> List.map written
                        |> Expect.equal [ ( "process_torrent", Created, ( -120, 3 ) ) ]
            , test "count a job that ran as created where it was queued, and as processed or failed where it last ran" <|
                \_ ->
                    QueueMetrics.occurrences lastDay
                        [ job "process_torrent" Status.Processed -180 (Just -120) 5
                        , job "process_torrent" Status.Failed -180 (Just -60) 2
                        ]
                        |> List.map written
                        |> Expect.equal
                            [ ( "process_torrent", Created, ( -180, 7 ) )
                            , ( "process_torrent", Processed, ( -120, 5 ) )
                            , ( "process_torrent", Failed, ( -60, 2 ) )
                            ]
            , test "count a job waiting to be tried again as created, and neither processed nor failed, since it is neither yet" <|
                \_ ->
                    QueueMetrics.occurrences lastDay [ job "process_torrent" Status.Retry -180 (Just -60) 4 ]
                        |> List.map written
                        |> Expect.equal [ ( "process_torrent", Created, ( -180, 4 ) ) ]
            , test "add up what happened in the same bucket, whatever became of the jobs since, and keep queues apart" <|
                \_ ->
                    QueueMetrics.occurrences lastDay
                        [ job "process_torrent_batch" Status.Pending -60 Nothing 1
                        , job "process_torrent" Status.Pending -60 Nothing 2
                        , job "process_torrent" Status.Processed -60 (Just -60) 3
                        , job "process_torrent" Status.Processed -60 (Just 0) 4
                        , job "process_torrent" Status.Retry -60 (Just 0) 5
                        ]
                        |> List.map written
                        |> Expect.equal
                            [ ( "process_torrent", Created, ( -60, 14 ) )
                            , ( "process_torrent", Processed, ( -60, 3 ) )
                            , ( "process_torrent", Processed, ( 0, 4 ) )
                            , ( "process_torrent_batch", Created, ( -60, 1 ) )
                            ]
            , test "leave out the creation of a job queued before the timeframe, which bitmagnet answers with because it ran inside it" <|
                \_ ->
                    -- The last day opens at 10:00 yesterday. A job queued three days ago that ran
                    -- an hour ago is in the answer, but it was not created in the last day.
                    QueueMetrics.occurrences lastDay [ job "process_torrent" Status.Processed (-3 * 1440) (Just -60) 6 ]
                        |> List.map written
                        |> Expect.equal [ ( "process_torrent", Processed, ( -60, 6 ) ) ]
            , test "leave out a New York day that began before the week, for jobs that ran, whose runs before the week bitmagnet answers with" <|
                \_ ->
                    QueueMetrics.occurrences newYorkWeek
                        [ job "process_torrent" Status.Processed newYorkStraddle (Just newYorkStraddle) 2
                        , job "process_torrent" Status.Failed (newYorkStraddle - 1440) (Just newYorkStraddle) 1
                        , job "process_torrent" Status.Processed newYorkFirstWhole (Just newYorkFirstWhole) 4
                        ]
                        |> List.map written
                        |> Expect.equal
                            [ ( "process_torrent", Created, ( newYorkFirstWhole, 4 ) )
                            , ( "process_torrent", Processed, ( newYorkFirstWhole, 4 ) )
                            ]
            , test "leave out a New York day that began before the week for jobs still pending too, so the first column is not half of one" <|
                \_ ->
                    QueueMetrics.occurrences newYorkWeek
                        [ job "process_torrent" Status.Pending newYorkStraddle Nothing 3
                        , job "process_torrent" Status.Pending newYorkFirstWhole Nothing 5
                        ]
                        |> List.map written
                        |> Expect.equal [ ( "process_torrent", Created, ( newYorkFirstWhole, 5 ) ) ]
            , test "keep everything for a timeframe with no start" <|
                \_ ->
                    QueueMetrics.occurrences { bucketDuration = Hour, startTime = Nothing }
                        [ job "process_torrent" Status.Failed (-30 * 1440) (Just (-29 * 1440)) 1 ]
                        |> List.map written
                        |> Expect.equal
                            [ ( "process_torrent", Created, ( -30 * 1440, 1 ) )
                            , ( "process_torrent", Failed, ( -29 * 1440, 1 ) )
                            ]
            , test "count a finished job bitmagnet has no run for as created only" <|
                \_ ->
                    QueueMetrics.occurrences { bucketDuration = Hour, startTime = Nothing }
                        [ job "process_torrent" Status.Processed -120 Nothing 2 ]
                        |> List.map written
                        |> Expect.equal [ ( "process_torrent", Created, ( -120, 2 ) ) ]
            ]
        , describe "totals"
            [ test "are none when the queue answered with nothing" <|
                \_ ->
                    QueueMetrics.totals lastDay []
                        |> Expect.equal []
            , test "add up the jobs of the timeframe by queue and status, the queues by name" <|
                \_ ->
                    QueueMetrics.totals lastDay
                        [ job "process_torrent_batch" Status.Failed -60 (Just -60) 1
                        , job "process_torrent" Status.Pending -60 Nothing 2
                        , job "process_torrent" Status.Pending -120 Nothing 3
                        , job "process_torrent" Status.Retry -60 (Just 0) 4
                        , job "process_torrent" Status.Failed -60 (Just 0) 5
                        , job "process_torrent" Status.Processed (-3 * 1440) (Just 0) 6
                        ]
                        |> Expect.equal
                            [ { queue = "process_torrent", pending = 5, retry = 4, failed = 5, processed = 6 }
                            , { queue = "process_torrent_batch", pending = 0, retry = 0, failed = 1, processed = 0 }
                            ]
            , test "leave out a job that last ran before the timeframe, which bitmagnet answers with whatever the timeframe" <|
                \_ ->
                    -- bitmagnet's filter lets every job that is not pending through (see the module's
                    -- comment), so the last day's answer has the jobs that ran two days ago too.
                    QueueMetrics.totals lastDay
                        [ job "process_torrent" Status.Processed (-3 * 1440) (Just (-2 * 1440)) 7
                        , job "process_torrent" Status.Failed (-3 * 1440) (Just (-2 * 1440)) 3
                        , job "process_torrent" Status.Processed -120 (Just -60) 1
                        ]
                        |> Expect.equal [ { queue = "process_torrent", pending = 0, retry = 0, failed = 0, processed = 1 } ]
            , test "count a pending job by when it was queued, and any other by when it last ran" <|
                \_ ->
                    QueueMetrics.totals lastDay
                        [ job "process_torrent" Status.Pending (-2 * 1440) Nothing 1
                        , job "process_torrent" Status.Pending -60 Nothing 2
                        , job "process_torrent" Status.Retry (-2 * 1440) (Just -60) 4
                        , job "process_torrent" Status.Processed -60 Nothing 8
                        ]
                        |> Expect.equal [ { queue = "process_torrent", pending = 2, retry = 4, failed = 0, processed = 0 } ]
            , test "leave out a New York day that began before the week, whatever the jobs' status, as the timeline does" <|
                \_ ->
                    QueueMetrics.totals newYorkWeek
                        [ job "process_torrent" Status.Processed (newYorkStraddle - 1440) (Just newYorkStraddle) 2
                        , job "process_torrent" Status.Retry newYorkStraddle (Just newYorkStraddle) 7
                        , job "process_torrent" Status.Pending newYorkStraddle Nothing 3
                        , job "process_torrent" Status.Failed newYorkFirstWhole (Just newYorkFirstWhole) 1
                        , job "process_torrent" Status.Pending newYorkFirstWhole Nothing 5
                        ]
                        |> Expect.equal [ { queue = "process_torrent", pending = 5, retry = 0, failed = 1, processed = 0 } ]
            , test "count everything the queue holds for a timeframe with no start, a job with no run among it" <|
                \_ ->
                    QueueMetrics.totals { bucketDuration = Hour, startTime = Nothing }
                        [ job "process_torrent" Status.Processed (-30 * 1440) (Just (-30 * 1440)) 2
                        , job "process_torrent" Status.Failed (-30 * 1440) Nothing 1
                        ]
                        |> Expect.equal [ { queue = "process_torrent", pending = 0, retry = 0, failed = 1, processed = 2 } ]
            ]
        , describe "queues"
            [ test "are every queue the answer names, by name, once each, whether or not it falls in the timeframe" <|
                \_ ->
                    QueueMetrics.queues
                        [ job "process_torrent_batch" Status.Processed (-3 * 1440) (Just (-2 * 1440)) 1
                        , job "process_torrent" Status.Pending -60 Nothing 1
                        , job "process_torrent" Status.Retry -60 (Just -30) 1
                        ]
                        |> Expect.equal [ "process_torrent", "process_torrent_batch" ]
            ]
        , describe "leftOut"
            [ test "is what fell in a bucket that began before the timeframe and reached into it, events and totals both" <|
                \_ ->
                    QueueMetrics.leftOut newYorkWeek
                        [ job "process_torrent" Status.Processed (newYorkStraddle - 1440) (Just newYorkStraddle) 2
                        , job "process_torrent_batch" Status.Pending newYorkStraddle Nothing 3
                        , job "process_torrent" Status.Processed newYorkFirstWhole (Just newYorkFirstWhole) 4
                        ]
                        |> Expect.equal
                            { occurrences =
                                [ { queue = "process_torrent", event = Processed, at = minutes newYorkStraddle, count = 2 }
                                , { queue = "process_torrent_batch", event = Created, at = minutes newYorkStraddle, count = 3 }
                                ]
                            , totals =
                                [ { queue = "process_torrent", pending = 0, retry = 0, failed = 0, processed = 2 }
                                , { queue = "process_torrent_batch", pending = 3, retry = 0, failed = 0, processed = 0 }
                                ]
                            }
            , test "is not what was wholly before the timeframe, nor anything on UTC, nor anything of everything" <|
                \_ ->
                    [ QueueMetrics.leftOut newYorkWeek [ job "process_torrent" Status.Processed (newYorkStraddle - 1440) (Just (newYorkStraddle - 1440)) 1 ]
                    , QueueMetrics.leftOut lastDay [ job "process_torrent" Status.Processed (-3 * 1440) (Just -60) 1, job "process_torrent" Status.Pending -1440 Nothing 1 ]
                    , QueueMetrics.leftOut { bucketDuration = Hour, startTime = Nothing } [ job "process_torrent" Status.Pending -60 Nothing 1 ]
                    ]
                        |> Expect.equal (List.repeat 3 { occurrences = [], totals = [] })
            ]
        , describe "eventKey"
            [ test "orders the events as a job lives them, and tells each apart" <|
                \_ ->
                    List.map QueueMetrics.eventKey QueueMetrics.allEvents
                        |> Expect.equal [ 0, 1, 2 ]
            ]
        , describe "events in the address"
            [ test "are written in Magnes's words and read back" <|
                \_ ->
                    List.map (QueueMetrics.eventName >> QueueMetrics.eventFromName) QueueMetrics.allEvents
                        |> Expect.equal [ Just Created, Just Processed, Just Failed ]
            , test "are created, processed and failed, and nothing else" <|
                \_ ->
                    ( List.map QueueMetrics.eventName QueueMetrics.allEvents
                    , QueueMetrics.eventFromName "retry"
                    )
                        |> Expect.equal ( [ "created", "processed", "failed" ], Nothing )
            ]
        ]


{-| 2026-10-10T10:00:00Z. Every time below is this plus a number of minutes.
-}
tenOClock : Int
tenOClock =
    1791626400000


minutes : Int -> Time.Posix
minutes n =
    Time.millisToPosix (tenOClock + n * 60000)


{-| The last day, by the hour, as `StatsControls.request` asks for it at 10:00.
-}
lastDay : { bucketDuration : MetricsBucketDuration, startTime : Maybe Time.Posix }
lastDay =
    { bucketDuration = Hour, startTime = Just (minutes -1440) }


{-| A week by the day, asked at 10:00 UTC on the 10th, opens at 00:00 UTC on the 3rd. In New
York in winter a day runs from 05:00 UTC to 05:00 UTC, so bitmagnet's first day began at 05:00
UTC on the 2nd, before the week did (`newYorkStraddle`), and the first whole one at 05:00 UTC on
the 3rd (`newYorkFirstWhole`). In minutes from 10:00 on the 10th.
-}
newYorkWeek : { bucketDuration : MetricsBucketDuration, startTime : Maybe Time.Posix }
newYorkWeek =
    { bucketDuration = Day, startTime = Just (minutes -(7 * 1440 + 600)) }


newYorkStraddle : Int
newYorkStraddle =
    -(8 * 1440 + 300)


newYorkFirstWhole : Int
newYorkFirstWhole =
    -(7 * 1440 + 300)


{-| A row of bitmagnet's answer: `count` jobs of `queue` in `status`, queued in the bucket
`created` minutes from 10:00 and last run in the one `ran` minutes from it.
-}
job : String -> QueueJobStatus -> Int -> Maybe Int -> Int -> QueueMetrics.Bucket
job queue status created ran count =
    { queue = queue
    , status = status
    , createdAt = minutes created
    , ranAt = Maybe.map minutes ran
    , count = count
    }


written : QueueMetrics.Occurrence -> ( String, Event, ( Int, Int ) )
written occurrence =
    ( occurrence.queue, occurrence.event, ( (Time.posixToMillis occurrence.at - tenOClock) // 60000, occurrence.count ) )
