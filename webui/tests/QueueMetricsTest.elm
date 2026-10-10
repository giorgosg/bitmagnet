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
            , test "keep a bucket that began before the timeframe but reaches into it, as bitmagnet's days do in a zone that is not UTC" <|
                \_ ->
                    -- In Athens the day began at 21:00 UTC; the request opened at midnight UTC.
                    QueueMetrics.occurrences { bucketDuration = Day, startTime = Just (minutes -600) }
                        [ job "process_torrent" Status.Processed -780 (Just -780) 2
                        , job "process_torrent" Status.Processed (-780 - 1440) (Just -780) 1
                        ]
                        |> List.map written
                        |> Expect.equal
                            [ ( "process_torrent", Created, ( -780, 2 ) )
                            , ( "process_torrent", Processed, ( -780, 3 ) )
                            ]
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
                    QueueMetrics.totals []
                        |> Expect.equal []
            , test "add up every job in the answer by queue and status, the queues by name" <|
                \_ ->
                    QueueMetrics.totals
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
