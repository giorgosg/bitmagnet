module QueueStatsTest exposing (suite)

import ApiError
import Charts
import Dict
import Expect
import Graphql.Document
import Html.Attributes
import Json.Decode as Decode
import Json.Encode as Encode
import Magnes.Api.Enum.MetricsBucketDuration exposing (MetricsBucketDuration(..))
import Magnes.Api.Enum.QueueJobStatus as Status exposing (QueueJobStatus)
import QueueMetrics exposing (Event(..))
import QueueStats
import Route
import StatsControls exposing (AutoRefresh(..), Timeframe(..))
import StatsLook
import Svg.Attributes
import Test exposing (Test, describe, test)
import Test.Html.Event as Event
import Test.Html.Query as Query
import Test.Html.Selector as Selector
import Time


suite : Test
suite =
    describe "QueueStats"
        [ describe "query"
            [ test "asks for everything by the hour, with no start, and for what each row is" <|
                \_ ->
                    serialized asked emptyParams
                        |> Expect.all
                            [ String.contains "bucketDuration: hour" >> Expect.equal True
                            , String.contains "startTime" >> Expect.equal False
                            , String.contains "buckets { queue status createdAtBucket ranAtBucket count }" >> Expect.equal True
                            ]
            , test "asks from where the timeframe's first column begins, in the unit the resolution comes to" <|
                \_ ->
                    -- A day of minutes at 10:07:30 is drawn by the hour, from 10:00 yesterday.
                    serialized (Time.millisToPosix (tenOClock + 450000)) (withTimeframe Days1 minutesParams)
                        |> Expect.all
                            [ String.contains "bucketDuration: hour" >> Expect.equal True
                            , String.contains "startTime: \"2026-10-09T10:00:00.000Z\"" >> Expect.equal True
                            ]
            , test "asks about every queue and status whichever are chosen, and sends no end time or latency" <|
                \_ ->
                    serialized asked { emptyParams | queues = [ "process_torrent" ], events = [ Failed ] }
                        |> Expect.all
                            [ String.contains "queues" >> Expect.equal False
                            , String.contains "statuses" >> Expect.equal False
                            , String.contains "endTime" >> Expect.equal False
                            , String.contains "latency" >> Expect.equal False
                            ]
            , test "reads bitmagnet's answer, and when it was asked" <|
                \_ ->
                    Decode.decodeString (Graphql.Document.decoder (QueueStats.query asked emptyParams)) answer
                        |> Expect.equal
                            (Ok
                                { asked = asked
                                , request = { bucketDuration = Hour, startTime = Nothing }
                                , buckets =
                                    [ job "process_torrent" Status.Pending -120 Nothing 2
                                    , job "process_torrent" Status.Processed -120 (Just -60) 3
                                    ]
                                }
                            )
            ]
        , describe "plot"
            [ test "has nothing to draw when the queue answered with nothing" <|
                \_ ->
                    plotOf emptyParams []
                        |> Expect.all
                            [ .slots >> Expect.equal []
                            , .lines >> Expect.equal []
                            , .totals >> Expect.equal []
                            ]
            , test "draws a line for each event of each queue, inked by the event, the queues by name" <|
                \_ ->
                    plotOf emptyParams
                        [ job "process_torrent_batch" Status.Pending -120 Nothing 1
                        , job "process_torrent" Status.Pending -120 Nothing 1
                        ]
                        |> .lines
                        |> List.map (\line -> ( line.label, line.ink ))
                        |> Expect.equal
                            [ ( "process_torrent: created", Charts.Muted )
                            , ( "process_torrent: processed", Charts.Strong )
                            , ( "process_torrent: failed", Charts.Accent )
                            , ( "process_torrent_batch: created", Charts.Faint )
                            , ( "process_torrent_batch: processed", Charts.StrongSoft )
                            , ( "process_torrent_batch: failed", Charts.AccentSoft )
                            ]
            , test "counts each event in its own bucket, merged by the multiplier, with the empty buckets between" <|
                \_ ->
                    let
                        plotted =
                            plotOf (withResolution { unit = Hour, every = Just 2 } (withTimeframe Hours12 emptyParams))
                                [ job "process_torrent" Status.Processed -600 (Just -540) 3
                                , job "process_torrent" Status.Failed -540 (Just -120) 2
                                , job "process_torrent" Status.Pending -60 Nothing 4
                                ]
                    in
                    Expect.all
                        [ .grid >> Expect.equal { unit = Hour, every = 2, offset = 0, bucketedBy = Hour }
                        , .slots
                            >> List.map (\slot -> ( minutesFrom slot.start, valuesOf plotted slot ))
                            >> Expect.equal
                                [ ( -720, [ 0, 0, 0 ] )
                                , ( -600, [ 5, 3, 0 ] )
                                , ( -480, [ 0, 0, 0 ] )
                                , ( -360, [ 0, 0, 0 ] )
                                , ( -240, [ 0, 0, 0 ] )
                                , ( -120, [ 4, 0, 2 ] )
                                , ( 0, [ 0, 0, 0 ] )
                                ]
                        ]
                        plotted
            , test "picks the multiplier itself when none was chosen, from the earliest thing counted for everything" <|
                \_ ->
                    -- Twenty days of hours is 480 of them: twenty columns of 20 hours.
                    plotOf emptyParams [ job "process_torrent" Status.Pending (-20 * 1440) Nothing 1 ]
                        |> .grid
                        |> Expect.equal { unit = Hour, every = 20, offset = 0, bucketedBy = Hour }
            , test "leaves out the creation of a job queued before the timeframe, so the chart does not reach back to it" <|
                \_ ->
                    plotOf (withTimeframe Hours1 minutesParams)
                        [ job "process_torrent" Status.Processed (-3 * 1440) (Just -30) 1 ]
                        |> .slots
                        |> List.head
                        |> Maybe.map .start
                        |> Expect.equal (Just (minutes -60))
            , describe "with queues and events chosen"
                [ test "draws only the chosen events, in the order of a job's life" <|
                    \_ ->
                        plotOf { emptyParams | events = [ Failed, Created ] } [ job "process_torrent" Status.Pending -120 Nothing 1 ]
                            |> .lines
                            |> List.map .label
                            |> Expect.equal [ "process_torrent: created", "process_torrent: failed" ]
                , test "draws only the chosen queues, inked by their place in the order they were chosen in" <|
                    \_ ->
                        plotOf { emptyParams | queues = [ "process_torrent_batch" ] }
                            [ job "process_torrent" Status.Pending -120 Nothing 1
                            , job "process_torrent_batch" Status.Pending -120 Nothing 1
                            ]
                            |> Expect.all
                                [ .lines
                                    >> List.map (\line -> ( line.label, line.ink ))
                                    >> Expect.equal
                                        [ ( "process_torrent_batch: created", Charts.Muted )
                                        , ( "process_torrent_batch: processed", Charts.Strong )
                                        , ( "process_torrent_batch: failed", Charts.Accent )
                                        ]
                                , .totals >> List.map .queue >> Expect.equal [ "process_torrent_batch" ]
                                ]
                , test "gives a chosen queue with nothing in the timeframe no place, so the others keep their inks and are not added together" <|
                    \_ ->
                        ( plotOf { emptyParams | queues = [ "ghost", "a", "b" ] } [ job "a" Status.Pending -60 Nothing 1, job "b" Status.Pending -60 Nothing 1 ]
                        , plotOf { emptyParams | queues = [ "ghost", "a" ] } [ job "a" Status.Pending -60 Nothing 1 ]
                        )
                            |> Expect.all
                                [ Tuple.first
                                    >> .lines
                                    >> List.map .label
                                    >> Expect.equal [ "a: created", "a: processed", "a: failed", "b: created", "b: processed", "b: failed" ]
                                , Tuple.first >> .others >> Expect.equal []
                                , Tuple.second
                                    >> .lines
                                    >> List.map (\line -> ( line.label, line.ink ))
                                    >> Expect.equal [ ( "a: created", Charts.Muted ), ( "a: processed", Charts.Strong ), ( "a: failed", Charts.Accent ) ]
                                ]
                , test "gives a queue that is in the answer but not the timeframe no place either" <|
                    \_ ->
                        -- bitmagnet answers the last hour with jobs that ran days ago (see QueueMetrics).
                        plotOf (withTimeframe Hours1 minutesParams)
                            [ job "old" Status.Processed (-3 * 1440) (Just (-2 * 1440)) 9
                            , job "a" Status.Pending -30 Nothing 1
                            , job "b" Status.Pending -30 Nothing 1
                            ]
                            |> Expect.all
                                [ .lines >> List.map .label >> List.filter (String.startsWith "b: ") >> Expect.equal [ "b: created", "b: processed", "b: failed" ]
                                , .others >> Expect.equal []
                                , .totals >> List.map .queue >> Expect.equal [ "a", "b" ]
                                ]
                , test "gives ink places only to queues with something on the timeline, not to one with only a total" <|
                    \_ ->
                        -- `a` has jobs waiting to be retried that were queued before the day and ran
                        -- in it: a total, and nothing on the timeline.
                        plotOf (withTimeframe Days1 emptyParams)
                            [ job "a" Status.Retry (-3 * 1440) (Just -60) 2
                            , job "b" Status.Pending -60 Nothing 1
                            ]
                            |> Expect.all
                                [ .lines
                                    >> List.map (\line -> ( line.label, line.ink ))
                                    >> Expect.equal [ ( "b: created", Charts.Muted ), ( "b: processed", Charts.Strong ), ( "b: failed", Charts.Accent ) ]
                                , .totals >> List.map .queue >> Expect.equal [ "a", "b" ]
                                ]
                , test "does not add two queues together because a third has only a total" <|
                    \_ ->
                        plotOf (withTimeframe Days1 emptyParams)
                            [ job "a" Status.Retry (-3 * 1440) (Just -60) 2
                            , job "b" Status.Pending -60 Nothing 1
                            , job "c" Status.Pending -60 Nothing 1
                            ]
                            |> Expect.all
                                [ .lines
                                    >> List.map (\line -> ( line.label, line.ink ))
                                    >> List.filter (Tuple.first >> String.endsWith "created")
                                    >> Expect.equal [ ( "b: created", Charts.Muted ), ( "c: created", Charts.Faint ) ]
                                , .others >> Expect.equal []
                                , .totals >> List.map .queue >> Expect.equal [ "a", "b", "c" ]
                                ]
                , test "has no chart when nothing chosen was counted, rather than lines along zero" <|
                    \_ ->
                        plotOf { emptyParams | events = [ Failed ] } [ job "process_torrent" Status.Pending -120 Nothing 1 ]
                            |> .slots
                            |> Expect.equal []
                , test "keeps a chosen event at zero for a queue that had something else happen, beside one that had it" <|
                    \_ ->
                        let
                            plotted =
                                plotOf { emptyParams | events = [ Failed ] }
                                    [ job "process_torrent" Status.Failed -120 (Just -120) 2
                                    , job "process_torrent_batch" Status.Pending -120 Nothing 1
                                    ]
                        in
                        ( List.map .label plotted.lines
                        , plotted.slots |> List.map (valuesOf plotted) |> List.filter (List.any ((/=) 0))
                        )
                            |> Expect.equal
                                ( [ "process_torrent: failed", "process_torrent_batch: failed" ]
                                , [ [ 2, 0 ] ]
                                )
                ]
            , describe "with more queues than inks"
                [ test "draws the first apart and adds the rest together, so the chart still adds up, and names them" <|
                    \_ ->
                        let
                            plotted =
                                plotOf emptyParams
                                    [ job "a" Status.Pending -60 Nothing 1
                                    , job "b" Status.Pending -60 Nothing 2
                                    , job "c" Status.Pending -60 Nothing 3
                                    ]
                        in
                        ( List.map .label plotted.lines
                        , plotted.slots |> List.map (valuesOf plotted) |> List.filter (List.any ((/=) 0))
                        , plotted.others
                        )
                            |> Expect.equal
                                ( [ "a: created", "a: processed", "a: failed", "Other queues: created", "Other queues: processed", "Other queues: failed" ]
                                , [ [ 1, 0, 0, 5, 0, 0 ] ]
                                , [ "b", "c" ]
                                )
                , test "has none to name when each is drawn apart" <|
                    \_ ->
                        plotOf emptyParams [ job "a" Status.Pending -60 Nothing 1, job "b" Status.Pending -60 Nothing 1 ]
                            |> .others
                            |> Expect.equal []
                ]
            , test "totals only the jobs of the timeframe, though bitmagnet answers with jobs that ran before it" <|
                \_ ->
                    plotOf (withTimeframe Hours1 minutesParams)
                        [ job "process_torrent" Status.Processed (-3 * 1440) (Just (-2 * 1440)) 9
                        , job "process_torrent" Status.Failed (-3 * 1440) (Just (-2 * 1440)) 5
                        , job "process_torrent" Status.Processed -40 (Just -30) 1
                        , job "process_torrent" Status.Pending -20 Nothing 2
                        ]
                        |> .totals
                        |> Expect.equal [ { queue = "process_torrent", pending = 2, retry = 0, failed = 0, processed = 1 } ]
            , test "totals the jobs of each queue by status, whatever was chosen of the events" <|
                \_ ->
                    plotOf { emptyParams | events = [ Created ] }
                        [ job "process_torrent" Status.Retry (-3 * 1440) (Just -60) 4
                        , job "process_torrent" Status.Pending -60 Nothing 2
                        ]
                        |> .totals
                        |> Expect.equal [ { queue = "process_torrent", pending = 2, retry = 4, failed = 0, processed = 0 } ]
            , describe "against a database not on UTC, as in New York, whose days begin at 05:00 UTC"
                [ test "leaves out the day that began before the week, for every status, and begins the chart at the first whole one" <|
                    \_ ->
                        let
                            plotted =
                                plotOf aWeekOfDays
                                    [ job "process_torrent" Status.Processed (newYorkStraddle - 1440) (Just newYorkStraddle) 2
                                    , job "process_torrent" Status.Pending newYorkStraddle Nothing 3
                                    , job "process_torrent" Status.Pending newYorkFirstWhole Nothing 5
                                    , job "process_torrent" Status.Failed newYorkFirstWhole (Just (newYorkFirstWhole + 1440)) 1
                                    ]
                        in
                        ( plotted.slots |> List.head |> Maybe.map (.start >> minutesFrom)
                        , plotted.slots |> List.concatMap (.counts >> Dict.values) |> List.sum
                        , ( plotted.totals, plotted.leftOut )
                        )
                            |> Expect.equal
                                ( Just newYorkFirstWhole
                                , 7
                                , ( [ { queue = "process_torrent", pending = 5, retry = 0, failed = 1, processed = 0 } ], True )
                                )
                , test "begins the chart at the first whole day too where the week's first moment falls in the day left out, as in Athens" <|
                    \_ ->
                        -- In Athens a day begins at 21:00 UTC: the week, from 10:00 UTC on the 3rd,
                        -- begins inside the day that began at 21:00 UTC on the 2nd, which straddles
                        -- the start at 00:00 UTC and is left out.
                        let
                            athensStraddle =
                                -(7 * 1440 + 780)

                            athensFirstWhole =
                                -(7 * 1440) + 660
                        in
                        plotOf aWeekOfDays
                            [ job "process_torrent" Status.Pending athensStraddle Nothing 3
                            , job "process_torrent" Status.Pending athensFirstWhole Nothing 5
                            , job "process_torrent" Status.Pending (athensFirstWhole + 1440) Nothing 1
                            ]
                            |> .slots
                            |> List.head
                            |> Maybe.map (\slot -> ( minutesFrom slot.start, Dict.values slot.counts ))
                            |> Expect.equal (Just ( athensFirstWhole, [ 5 ] ))
                , test "is no different on UTC, where no day began before the week" <|
                    \_ ->
                        let
                            plotted =
                                plotOf aWeekOfDays
                                    [ job "process_torrent" Status.Pending weekStart Nothing 3
                                    , job "process_torrent" Status.Processed (weekStart - 1440) (Just weekStart) 2
                                    ]
                        in
                        ( plotted.slots |> List.head |> Maybe.map (.start >> minutesFrom)
                        , List.map .processed plotted.totals
                        , plotted.leftOut
                        )
                            |> Expect.equal ( Just weekStart, [ 2 ], False )
                , test "says something was left out only when the page would have drawn or counted it" <|
                    \_ ->
                        let
                            -- Queued on the day that began before the week, and run on the next:
                            -- its creation is left out, its run is counted.
                            createdBefore =
                                [ job "a" Status.Processed newYorkStraddle (Just newYorkFirstWhole) 1
                                , job "b" Status.Pending newYorkFirstWhole Nothing 1
                                ]

                            leftOutFor chosen =
                                (plotOf chosen createdBefore).leftOut
                        in
                        [ leftOutFor aWeekOfDays
                        , leftOutFor { aWeekOfDays | events = [ Processed ] }
                        , leftOutFor { aWeekOfDays | queues = [ "b" ] }
                        , leftOutFor { aWeekOfDays | queues = [ "a" ], events = [ Created ] }
                        ]
                            |> Expect.equal [ True, False, False, True ]
                ]
            , test "says what would have been drawn, where the chart was cut down to fit" <|
                \_ ->
                    let
                        aWeekOfMinutes =
                            withResolution { unit = Minute, every = Just 1 } (withTimeframe Weeks1 emptyParams)

                        plotted =
                            plotOf aWeekOfMinutes [ job "process_torrent" Status.Pending -2 Nothing 4 ]
                    in
                    ( plotted.grid.every, plotted.wanted.every )
                        |> Expect.equal ( 6, 1 )
            ]
        , describe "view"
            [ test "says it is loading, with the controls already there to change, everything among the timeframes" <|
                \_ ->
                    viewed emptyParams StatsLook.empty
                        |> Expect.all
                            [ Query.has [ Selector.text "Loading statistics…" ]
                            , Query.find [ Selector.class "chip", Selector.attribute (Html.Attributes.attribute "aria-pressed" "true"), Selector.containing [ Selector.text "all time" ] ]
                                >> Query.has [ Selector.text "all time" ]
                            , Query.find [ Selector.class "chip", Selector.attribute (Html.Attributes.attribute "aria-pressed" "true"), Selector.containing [ Selector.text "hours" ] ]
                                >> Query.has [ Selector.text "hours" ]
                            ]
            , test "names the drawing by what its lines follow, for a screen reader" <|
                \_ ->
                    ( viewed emptyParams shown
                        |> Query.find [ Selector.tag "svg", Selector.attribute (Html.Attributes.attribute "aria-label" "Line chart: jobs created, processed and failed per hour, by queue.") ]
                        |> Query.has [ Selector.tag "svg" ]
                    , viewed { emptyParams | events = [ Failed, Processed ] } shown
                        |> Query.find [ Selector.tag "svg", Selector.attribute (Html.Attributes.attribute "aria-label" "Line chart: jobs processed and failed per hour, by queue.") ]
                        |> Query.has [ Selector.tag "svg" ]
                    )
                        |> (\( all, chosen ) -> Expect.all [ always all, always chosen ] ())
            , test "draws the timeline headed by what a bucket is, and the totals, each with a legend" <|
                \_ ->
                    viewed emptyParams shown
                        |> Expect.all
                            [ Query.findAll [ Selector.tag "figcaption" ]
                                >> Expect.all
                                    [ Query.count (Expect.equal 2)
                                    , Query.index 0 >> Query.has [ Selector.text "Jobs per hour" ]
                                    , Query.index 1 >> Query.has [ Selector.text "Jobs by queue and status" ]
                                    ]
                            , Query.findAll [ Selector.class "chart-legend" ]
                                >> Expect.all
                                    [ Query.index 0 >> Query.findAll [ Selector.tag "li" ] >> Query.count (Expect.equal 6)
                                    , Query.index 1
                                        >> Query.findAll [ Selector.tag "li" ]
                                        >> Expect.all
                                            [ Query.count (Expect.equal 4)
                                            , Query.index 0 >> Query.has [ Selector.text "pending" ]
                                            , Query.index 3 >> Query.has [ Selector.text "processed" ]
                                            ]
                                    ]
                            ]
            , test "draws the answer it has with the queues and events now chosen, without asking again" <|
                \_ ->
                    viewed { emptyParams | queues = [ "process_torrent" ], events = [ Failed ] } shown
                        |> Expect.all
                            [ Query.findAll [ Selector.class "chart-legend" ]
                                >> Query.index 0
                                >> Query.findAll [ Selector.tag "li" ]
                                >> Expect.all [ Query.count (Expect.equal 1), Query.index 0 >> Query.has [ Selector.text "process_torrent: failed" ] ]
                            , Query.findAll [ Selector.tag "figure" ]
                                >> Query.index 1
                                >> Query.find [ Selector.tag "tbody" ]
                                >> Query.findAll [ Selector.tag "tr" ]
                                >> Expect.all [ Query.count (Expect.equal 1), Query.index 0 >> Query.has [ Selector.text "process_torrent" ] ]
                            , Query.findAll [ Selector.class "stats-refreshing" ] >> Query.count (Expect.equal 0)
                            ]
            , describe "a look that fails"
                [ test "says why, alone, when there was nothing to show" <|
                    \_ ->
                        viewed emptyParams (QueueStats.failed emptyParams ApiError.ServiceUnavailable StatsLook.empty)
                            |> Expect.all
                                [ Query.find [ Selector.attribute (Html.Attributes.attribute "role" "alert") ]
                                    >> Query.has [ Selector.text (ApiError.toMessage ApiError.ServiceUnavailable) ]
                                , Query.findAll [ Selector.tag "figure" ] >> Query.count (Expect.equal 0)
                                ]
                , test "keeps a chart that answered the same question, whichever queues and events are chosen, and says why and as of when" <|
                    \_ ->
                        let
                            narrowed =
                                withRefresh Every10Seconds { emptyParams | queues = [ "process_torrent" ], events = [ Created ] }
                        in
                        viewed narrowed (QueueStats.failed narrowed ApiError.Unreachable shown)
                            |> Expect.all
                                [ Query.find [ Selector.attribute (Html.Attributes.attribute "role" "alert") ]
                                    >> Query.has [ Selector.text (ApiError.toMessage ApiError.Unreachable), Selector.text "2026-10-10 10:00" ]
                                , Query.findAll [ Selector.tag "figure" ] >> Query.count (Expect.equal 2)
                                ]
                , test "shows why alone when the chart answered another question, so it is not left under chips that are not its own" <|
                    \_ ->
                        let
                            aWeek =
                                withTimeframe Weeks1 emptyParams
                        in
                        viewed aWeek (QueueStats.failed aWeek ApiError.Unreachable shown)
                            |> Expect.all
                                [ Query.find [ Selector.attribute (Html.Attributes.attribute "role" "alert") ]
                                    >> Query.has [ Selector.text (ApiError.toMessage ApiError.Unreachable) ]
                                , Query.findAll [ Selector.tag "figure" ] >> Query.count (Expect.equal 0)
                                ]
                , test "goes on offering the queues of the last answer" <|
                    \_ ->
                        viewed (withTimeframe Weeks1 emptyParams) (QueueStats.failed (withTimeframe Weeks1 emptyParams) ApiError.Unreachable shown)
                            |> Query.findAll [ Selector.class "chip", Selector.containing [ Selector.text "process_torrent_batch" ] ]
                            |> Query.count (Expect.equal 1)
                ]
            , test "keeps the old chart, dimmed and busy, while another look is on its way" <|
                \_ ->
                    viewed emptyParams (StatsLook.refreshing shown)
                        |> Query.find [ Selector.class "stats-refreshing" ]
                        |> Query.has [ Selector.attribute (Html.Attributes.attribute "aria-busy" "true"), Selector.tag "figure" ]
            , test "with nothing counted, says so rather than drawing either chart" <|
                \_ ->
                    viewed emptyParams (QueueStats.loaded emptyParams (statistics []) StatsLook.empty)
                        |> Expect.all
                            [ Query.findAll [ Selector.class "chart-empty" ] >> Query.count (Expect.equal 2)
                            , Query.findAll [ Selector.tag "svg", Selector.attribute (Html.Attributes.attribute "role" "img") ] >> Query.count (Expect.equal 0)
                            ]
            , test "draws retry in an ink no line has, so a held-back failure line is not read as retry" <|
                \_ ->
                    viewed emptyParams shown
                        |> Query.findAll [ Selector.class "chart-legend" ]
                        |> Expect.all
                            [ Query.index 1
                                >> Query.findAll [ Selector.tag "li" ]
                                >> Query.index 1
                                >> Query.has [ Selector.text "retry", Selector.attribute (Svg.Attributes.fill "var(--bg)"), Selector.attribute (Svg.Attributes.stroke "var(--accent)") ]
                            , Query.index 0 >> Query.hasNot [ Selector.attribute (Svg.Attributes.fill "var(--bg)") ]
                            ]
            , test "says that the events chosen narrow the timeline and not the totals, when some are chosen" <|
                \_ ->
                    ( viewed { emptyParams | events = [ Failed ] } shown
                        |> Query.has [ Selector.text "The events chosen narrow the timeline only: the totals are every job of the timeframe, by status." ]
                    , viewed emptyParams shown
                        |> Query.hasNot [ Selector.text "The events chosen narrow the timeline only" ]
                    )
                        |> (\( chosen, none ) -> Expect.all [ always chosen, always none ] ())
            , test "says the totals are of the chosen queues, when queues are chosen too" <|
                \_ ->
                    viewed { emptyParams | events = [ Failed ], queues = [ "process_torrent" ] } shown
                        |> Query.has [ Selector.text "The events chosen narrow the timeline only: the totals are every job of the chosen queues in the timeframe, by status." ]
            , test "says the day that began before the timeframe is left out, when it is" <|
                \_ ->
                    let
                        newYork =
                            [ job "process_torrent" Status.Pending newYorkStraddle Nothing 1, job "process_torrent" Status.Pending newYorkFirstWhole Nothing 1 ]

                        utc =
                            [ job "process_torrent" Status.Pending weekStart Nothing 1 ]

                        said =
                            "bitmagnet's time zone began the first day before the timeframe did, so that day is left out of both charts, which begin with the first whole one."
                    in
                    ( viewed aWeekOfDays (QueueStats.loaded aWeekOfDays (statisticsFor aWeekOfDays newYork) StatsLook.empty)
                        |> Query.has [ Selector.text said ]
                    , viewed aWeekOfDays (QueueStats.loaded aWeekOfDays (statisticsFor aWeekOfDays utc) StatsLook.empty)
                        |> Query.hasNot [ Selector.text "began the first day before the timeframe" ]
                    )
                        |> (\( leftOut, whole ) -> Expect.all [ always leftOut, always whole ] ())
            , test "says when it was asked, and how to read the counts" <|
                \_ ->
                    viewed emptyParams shown
                        |> Expect.all
                            [ Query.has [ Selector.text "As of 2026-10-10 10:00" ]
                            , Query.has [ Selector.text "one waiting to be tried again is neither yet" ]
                            ]
            , test "names the queues that were added together, and says when the chart was cut down" <|
                \_ ->
                    let
                        aWeekOfMinutes =
                            withResolution { unit = Minute, every = Just 1 } (withTimeframe Weeks1 emptyParams)

                        many =
                            [ job "a" Status.Pending -60 Nothing 1, job "b" Status.Pending -60 Nothing 1, job "c" Status.Pending -60 Nothing 1 ]
                    in
                    viewed aWeekOfMinutes (QueueStats.loaded aWeekOfMinutes (statisticsFor aWeekOfMinutes many) StatsLook.empty)
                        |> Expect.all
                            [ Query.has [ Selector.text "Other queues are b, c." ]
                            , Query.has [ Selector.text "Drawn per 6 minutes, not per minute: that many buckets are more than the chart can draw." ]
                            ]
            , describe "controls"
                [ test "choosing a queue adds it, and choosing it again takes it away" <|
                    \_ ->
                        ( viewed emptyParams shown
                            |> Query.find [ Selector.class "chip", Selector.containing [ Selector.text "process_torrent_batch" ] ]
                            |> Event.simulate Event.click
                            |> Event.expect (Just { emptyParams | queues = [ "process_torrent_batch" ] })
                        , viewed { emptyParams | queues = [ "process_torrent", "process_torrent_batch" ] } shown
                            |> Query.find [ Selector.class "chip", Selector.containing [ Selector.text "process_torrent_batch" ] ]
                            |> Event.simulate Event.click
                            |> Event.expect (Just { emptyParams | queues = [ "process_torrent" ] })
                        )
                            |> (\( added, removed ) -> Expect.all [ always added, always removed ] ())
                , test "choosing an event adds it, and choosing it again takes it away" <|
                    \_ ->
                        ( viewed emptyParams shown
                            |> Query.find [ Selector.class "chip", Selector.containing [ Selector.text "failed" ] ]
                            |> Event.simulate Event.click
                            |> Event.expect (Just { emptyParams | events = [ Failed ] })
                        , viewed { emptyParams | events = [ Failed, Processed ] } shown
                            |> Query.find [ Selector.class "chip", Selector.containing [ Selector.text "failed" ] ]
                            |> Event.simulate Event.click
                            |> Event.expect (Just { emptyParams | events = [ Processed ] })
                        )
                            |> (\( added, removed ) -> Expect.all [ always added, always removed ] ())
                , test "a chosen queue the answer does not have is still offered, so it can be unchosen" <|
                    \_ ->
                        viewed { emptyParams | queues = [ "elsewhere" ] } shown
                            |> Query.find [ Selector.class "chip", Selector.attribute (Html.Attributes.attribute "aria-pressed" "true"), Selector.containing [ Selector.text "elsewhere" ] ]
                            |> Event.simulate Event.click
                            |> Event.expect (Just emptyParams)
                , test "choosing a timeframe asks for it and keeps the rest" <|
                    \_ ->
                        viewed { emptyParams | events = [ Failed ] } shown
                            |> Query.find [ Selector.class "chip", Selector.containing [ Selector.text "1 day" ] ]
                            |> Event.simulate Event.click
                            |> Event.expect (Just (withTimeframe Days1 { emptyParams | events = [ Failed ] }))
                , test "shows the multiplier the chart came to where none was chosen" <|
                    \_ ->
                        viewed emptyParams shown
                            |> Query.find [ Selector.tag "input" ]
                            |> Query.has [ Selector.attribute (Html.Attributes.value ""), Selector.attribute (Html.Attributes.placeholder "1") ]
                , test "typing a multiplier asks for it" <|
                    \_ ->
                        viewed emptyParams shown
                            |> Query.find [ Selector.tag "input" ]
                            |> Event.simulate (Event.custom "change" (Encode.object [ ( "target", Encode.object [ ( "value", Encode.string "3" ) ] ) ]))
                            |> Event.expect (Just (withResolution { unit = Hour, every = Just 3 } emptyParams))
                , test "offers a look right now" <|
                    \_ ->
                        viewed emptyParams shown
                            |> Query.find [ Selector.tag "button", Selector.containing [ Selector.text "Refresh now" ] ]
                            |> Event.simulate Event.click
                            |> Event.expect Nothing
                ]
            ]
        ]


{-| The query as one line, so a test does not depend on how it is indented.
-}
serialized : Time.Posix -> Route.QueueStatsParams -> String
serialized now params =
    Graphql.Document.serializeQuery (QueueStats.query now params)
        |> String.words
        |> String.join " "


{-| 2026-10-10T10:00:00Z, when the data was asked for.
-}
tenOClock : Int
tenOClock =
    1791626400000


asked : Time.Posix
asked =
    Time.millisToPosix tenOClock


minutes : Int -> Time.Posix
minutes n =
    Time.millisToPosix (tenOClock + n * 60000)


minutesFrom : Time.Posix -> Int
minutesFrom at =
    (Time.posixToMillis at - tenOClock) // 60000


emptyParams : Route.QueueStatsParams
emptyParams =
    Route.emptyQueueStats


minutesParams : Route.QueueStatsParams
minutesParams =
    withResolution { unit = Minute, every = Nothing } emptyParams


withTimeframe : StatsControls.Timeframe -> Route.QueueStatsParams -> Route.QueueStatsParams
withTimeframe timeframe params =
    let
        controls =
            params.controls
    in
    { params | controls = { controls | timeframe = timeframe } }


withResolution : StatsControls.Resolution -> Route.QueueStatsParams -> Route.QueueStatsParams
withResolution resolution params =
    let
        controls =
            params.controls
    in
    { params | controls = { controls | resolution = resolution } }


{-| A week by the day. Asked at 10:00 UTC on the 10th, it opens at 00:00 UTC on the 3rd
(`weekStart`). In New York in winter a day runs from 05:00 UTC, so bitmagnet's first day began
at 05:00 UTC on the 2nd (`newYorkStraddle`), and the first whole one at 05:00 UTC on the 3rd
(`newYorkFirstWhole`). In minutes from 10:00 on the 10th.
-}
aWeekOfDays : Route.QueueStatsParams
aWeekOfDays =
    withResolution { unit = Day, every = Nothing } (withTimeframe Weeks1 emptyParams)


weekStart : Int
weekStart =
    -(7 * 1440 + 600)


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


{-| An answer to the look `params` asks for at 10:00.
-}
statisticsFor : Route.QueueStatsParams -> List QueueMetrics.Bucket -> QueueStats.Statistics
statisticsFor params buckets =
    { asked = asked, request = StatsControls.request asked params.controls, buckets = buckets }


statistics : List QueueMetrics.Bucket -> QueueStats.Statistics
statistics =
    statisticsFor emptyParams


{-| What `params` draws of an answer to the look it asks for.
-}
plotOf : Route.QueueStatsParams -> List QueueMetrics.Bucket -> QueueStats.Plot
plotOf params buckets =
    QueueStats.plot params (QueueStats.answer (statisticsFor params buckets))


withRefresh : AutoRefresh -> Route.QueueStatsParams -> Route.QueueStatsParams
withRefresh refresh params =
    let
        controls =
            params.controls
    in
    { params | controls = { controls | refresh = refresh } }


{-| An answer about both of bitmagnet's queues, drawn: something of every status in each.
-}
shown : QueueStats.State
shown =
    QueueStats.loaded emptyParams
        (statistics
            [ job "process_torrent" Status.Pending -120 Nothing 2
            , job "process_torrent" Status.Processed -180 (Just -120) 3
            , job "process_torrent" Status.Failed -180 (Just -60) 1
            , job "process_torrent_batch" Status.Retry -180 (Just -60) 1
            , job "process_torrent_batch" Status.Processed -120 (Just -60) 4
            ]
        )
        StatsLook.empty


{-| What a view's event asks for: the page it navigates to, or `Nothing` for a plain refresh,
which asks for no other page.
-}
messages : QueueStats.Messages (Maybe Route.QueueStatsParams)
messages =
    { navigate = Just, refreshRequested = Nothing }


viewed : Route.QueueStatsParams -> QueueStats.State -> Query.Single (Maybe Route.QueueStatsParams)
viewed params state =
    QueueStats.view Time.utc messages params state
        |> Query.fromHtml


{-| What each line of a plot reads in a slot, in the order the lines are drawn.
-}
valuesOf : QueueStats.Plot -> { a | counts : Dict.Dict ( Int, Int ) Int } -> List Int
valuesOf plotted slot =
    List.map (\line -> Dict.get line.key slot.counts |> Maybe.withDefault 0) plotted.lines


{-| Shaped as bitmagnet answers: a row per queue, status, creation bucket and run bucket.
-}
answer : String
answer =
    """
    {"data": {"queue": {"metrics": {"buckets": [
      {"queue": "process_torrent", "status": "pending", "createdAtBucket": "2026-10-10T08:00:00Z", "ranAtBucket": null, "count": 2},
      {"queue": "process_torrent", "status": "processed", "createdAtBucket": "2026-10-10T08:00:00Z", "ranAtBucket": "2026-10-10T09:00:00Z", "count": 3}
    ]}}}}
    """
