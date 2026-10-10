module BucketsTest exposing (suite)

import Buckets
import Dict
import Expect
import Magnes.Api.Enum.MetricsBucketDuration exposing (MetricsBucketDuration(..))
import Test exposing (Test, describe, test)
import Time


{-| 2026-10-10T10:00:00Z. Every time below is this plus a number of minutes, so the
expected values can be read off a clock.
-}
tenOClock : Int
tenOClock =
    1791626400000


minutes : Int -> Time.Posix
minutes n =
    Time.millisToPosix (tenOClock + n * 60000)


hours : Int -> Int
hours n =
    n * 60


{-| The window of a timeframe that ends at 10:00: the last `length` minutes.
-}
lastMinutes : Int -> Buckets.Window
lastMinutes length =
    { from = Just (minutes -length), to = minutes 0 }


sample : String -> Time.Posix -> Int -> Buckets.Sample String
sample series at count =
    { series = series, at = at, count = count }


{-| A grid that begins on the boundaries of its unit, as one in UTC does.
-}
plain : MetricsBucketDuration -> Int -> Buckets.Grid
plain unit every =
    { unit = unit, every = every, offset = 0, bucketedBy = unit }


{-| A slot as a person would write it down: when it starts, in minutes from 10:00, and what
is in it.
-}
slotsOf : Buckets.Grid -> Buckets.Window -> List (Buckets.Sample String) -> List ( Int, List ( String, Int ) )
slotsOf resolved window samples =
    Buckets.slots resolved window samples
        |> List.map (\slot -> ( (Time.posixToMillis slot.start - tenOClock) // 60000, Dict.toList slot.counts ))


{-| The grid and the slots a resolution comes to for some samples, which is how a page asks.
-}
drawn : { unit : MetricsBucketDuration, every : Maybe Int } -> Buckets.Window -> List (Buckets.Sample String) -> ( Buckets.Grid, List ( Int, List ( String, Int ) ) )
drawn resolution window samples =
    let
        resolved =
            Buckets.grid resolution window samples
    in
    ( resolved, slotsOf resolved window samples )


suite : Test
suite =
    describe "Buckets"
        [ describe "grid"
            [ test "keeps a multiplier that was asked for" <|
                \_ ->
                    Buckets.grid { unit = Hour, every = Just 3 } (lastMinutes 360) []
                        |> Expect.equal (plain Hour 3)
            , test "chooses one bucket a minute over an hour, as the Angular UI does" <|
                \_ ->
                    -- 60 minutes of span is under the 100 that earns a step of five.
                    Buckets.grid { unit = Minute, every = Nothing } (lastMinutes 60) []
                        |> Expect.equal (plain Minute 1)
            , test "chooses fifteen minutes over six hours" <|
                \_ ->
                    -- 360 minutes: floor (360 / 100) = 3 steps of five.
                    Buckets.grid { unit = Minute, every = Nothing } (lastMinutes 360) []
                        |> Expect.equal (plain Minute 15)
            , test "chooses thirty-five minutes over twelve hours" <|
                \_ ->
                    Buckets.grid { unit = Minute, every = Nothing } (lastMinutes 720) []
                        |> Expect.equal (plain Minute 35)
            , test "never chooses more than an hour of minutes" <|
                \_ ->
                    Buckets.grid { unit = Minute, every = Nothing } (lastMinutes (7 * 24 * 60)) []
                        |> .every
                        |> Expect.atMost 60
            , test "chooses five hours over a week of hours, and a day over a week of days" <|
                \_ ->
                    ( Buckets.grid { unit = Hour, every = Nothing } (lastMinutes (7 * 24 * 60)) []
                    , Buckets.grid { unit = Day, every = Nothing } (lastMinutes (7 * 24 * 60)) []
                    )
                        |> Expect.equal ( plain Hour 5, plain Day 1 )
            , test "decides on the window alone when there is a start, whatever came back" <|
                \_ ->
                    -- The request was made for the window, and so was its start rounded down.
                    -- A sample from beyond it (a browser clock behind the server's) is drawn, but
                    -- is not what the multiplier is chosen from.
                    Buckets.grid { unit = Minute, every = Nothing }
                        { from = Just (minutes -60), to = minutes -60 }
                        [ sample "a" (minutes 300) 1 ]
                        |> Expect.equal (plain Minute 1)
            , test "without a start, spans from the earliest sample to the end of the window" <|
                \_ ->
                    -- Six hours of data and no start to the window: as six hours would be.
                    Buckets.grid { unit = Minute, every = Nothing }
                        { from = Nothing, to = minutes 0 }
                        [ sample "a" (minutes -360) 1 ]
                        |> Expect.equal (plain Minute 15)
            , test "without a start, counts a sample later than the end of the window, as the Angular UI does" <|
                \_ ->
                    -- The browser's clock can run behind the server's.
                    Buckets.grid { unit = Minute, every = Nothing }
                        { from = Nothing, to = minutes -60 }
                        [ sample "a" (minutes 300) 1 ]
                        |> Expect.equal (plain Minute 15)
            , describe "in the largest whole unit"
                [ test "is an hour where the multiplier made one, however it was chosen" <|
                    \_ ->
                        ( Buckets.grid { unit = Minute, every = Just 60 } (lastMinutes (hours 6)) []
                        , Buckets.grid { unit = Minute, every = Just 120 } (lastMinutes (hours 6)) []
                        )
                            |> Expect.equal ( plain Hour 1, plain Hour 2 )
                , test "is a day where the multiplier made one" <|
                    \_ ->
                        [ Buckets.grid { unit = Minute, every = Just 1440 } (lastMinutes (hours 72)) []
                        , Buckets.grid { unit = Minute, every = Just 2880 } (lastMinutes (hours 72)) []
                        , Buckets.grid { unit = Hour, every = Just 24 } (lastMinutes (hours 72)) []
                        , Buckets.grid { unit = Hour, every = Just 48 } (lastMinutes (hours 72)) []
                        ]
                            |> Expect.equal [ plain Day 1, plain Day 2, plain Day 1, plain Day 2 ]
                , test "is left alone where it is not a whole number of the larger unit" <|
                    \_ ->
                        ( Buckets.grid { unit = Minute, every = Just 90 } (lastMinutes (hours 72)) []
                        , Buckets.grid { unit = Hour, every = Just 5 } (lastMinutes (hours 72)) []
                        )
                            |> Expect.equal ( plain Minute 90, plain Hour 5 )
                , test "makes a day or a week of the default minutes into hours, so bitmagnet is not asked for them by the minute" <|
                    \_ ->
                        ( Buckets.grid { unit = Minute, every = Nothing } (lastMinutes (hours 24)) []
                        , Buckets.grid { unit = Minute, every = Nothing } (lastMinutes (7 * hours 24)) []
                        )
                            |> Expect.equal ( plain Hour 1, plain Hour 1 )
                ]
            , describe "never more than 2,000 columns"
                [ test "raises a multiplier that was asked for when it would make more, and says what was asked for" <|
                    \_ ->
                        -- A week of minutes is 10,081 columns; 6 of them to a bucket is under 2,000.
                        ( Buckets.grid { unit = Minute, every = Just 1 } (lastMinutes (7 * 24 * 60)) []
                        , Buckets.unlimited { unit = Minute, every = Just 1 } (lastMinutes (7 * 24 * 60)) []
                        )
                            |> Expect.equal ( plain Minute 6, plain Minute 1 )
                , test "raises one that was left to Magnes just the same, over a window with no start" <|
                    \_ ->
                        let
                            window =
                                { from = Nothing, to = minutes 0 }

                            years =
                                [ sample "a" (minutes (-5 * 365 * 24 * 60)) 1 ]

                            resolution =
                                { unit = Minute, every = Nothing }
                        in
                        ( List.length (Buckets.slots (Buckets.grid resolution window years) window years) <= 2000
                        , Buckets.grid resolution window years /= Buckets.unlimited resolution window years
                        )
                            |> Expect.equal ( True, True )
                , test "leaves what a chart can draw as asked: a day of minutes, and 1,998 of them" <|
                    \_ ->
                        ( Buckets.grid { unit = Minute, every = Just 1 } (lastMinutes (24 * 60)) []
                        , Buckets.grid { unit = Minute, every = Just 1 } (lastMinutes 1998) []
                        )
                            |> Expect.equal ( plain Minute 1, plain Minute 1 )
                ]
            , describe "in a time zone of its own"
                [ test "begins a day where bitmagnet began it, three hours ahead of UTC" <|
                    \_ ->
                        -- Athens in October: a day begins at 21:00 UTC. The first column is the one the
                        -- window opens in, 12:00 UTC on 8 October, which began at 21:00 on the 7th.
                        drawn { unit = Day, every = Just 1 }
                            { from = Just (minutes -2760), to = minutes 0 }
                            [ sample "a" (minutes -2220) 5, sample "a" (minutes -780) 7 ]
                            |> Expect.equal
                                ( { unit = Day, every = 1, offset = 21 * 3600 * 1000, bucketedBy = Day }
                                , [ ( -3660, [] ), ( -2220, [ ( "a", 5 ) ] ), ( -780, [ ( "a", 7 ) ] ) ]
                                )
                , test "begins a day where bitmagnet began it, five hours behind UTC" <|
                    \_ ->
                        -- A zone five hours behind UTC, as New York is in winter: a day begins at 05:00 UTC.
                        drawn { unit = Day, every = Just 1 }
                            { from = Just (minutes -2760), to = minutes 0 }
                            [ sample "a" (minutes -1740) 4, sample "a" (minutes -300) 9 ]
                            |> Expect.equal
                                ( { unit = Day, every = 1, offset = 5 * 3600 * 1000, bucketedBy = Day }
                                , [ ( -3180, [] ), ( -1740, [ ( "a", 4 ) ] ), ( -300, [ ( "a", 9 ) ] ) ]
                                )
                , test "begins an hour at the half hour in a zone that is half an hour out, and keeps merged hours to it" <|
                    \_ ->
                        -- India: hours begin at :30. Two hours to a bucket, counted from the epoch and
                        -- shifted by the half hour.
                        drawn { unit = Hour, every = Just 2 }
                            { from = Just (minutes -30), to = minutes 30 }
                            [ sample "a" (minutes -30) 3, sample "a" (minutes 30) 4 ]
                            |> Expect.equal
                                ( { unit = Hour, every = 2, offset = 30 * 60 * 1000, bucketedBy = Hour }
                                , [ ( -90, [ ( "a", 3 ) ] ), ( 30, [ ( "a", 4 ) ] ) ]
                                )
                , test "takes the offset most of the buckets have, so a day an hour out across a clock change does not move the rest" <|
                    \_ ->
                        Buckets.grid { unit = Day, every = Just 1 }
                            (lastMinutes (hours 72))
                            [ sample "a" (minutes -780) 1, sample "a" (minutes -2220) 1, sample "a" (minutes -3600) 1 ]
                            |> .offset
                            |> Expect.equal (21 * 3600 * 1000)
                , test "does not add the partial day before the first column into it: a week by the day, asked at 22:00 UTC in Athens" <|
                    \_ ->
                        -- The request opens at 00:00 UTC on 3 October, for the page cannot know the zone,
                        -- so bitmagnet answers with the Athens day that began at 21:00 UTC on the 2nd, 21
                        -- hours of it counted: 100 rows. The window's own first column is the next day,
                        -- which began at 21:00 UTC on the 3rd and holds 1.
                        drawn { unit = Day, every = Just 1 }
                            { from = Just (minutes (720 - 7 * 1440)), to = minutes 720 }
                            [ sample "a" (minutes (660 - 8 * 1440)) 100, sample "a" (minutes (660 - 7 * 1440)) 1 ]
                            |> Tuple.second
                            |> Expect.all
                                [ List.take 2
                                    >> Expect.equal
                                        [ ( 660 - 8 * 1440, [ ( "a", 100 ) ] )
                                        , ( 660 - 7 * 1440, [ ( "a", 1 ) ] )
                                        ]
                                , List.length >> Expect.equal 9
                                ]
                , test "does not add the partial hour before the first column into it: a day of hours, asked at 10:45 UTC in India" <|
                    \_ ->
                        -- Hours there begin at :30. The request opens on the hour, 10:00 UTC yesterday, so
                        -- bitmagnet answers with the hour that began at 09:30, a half of it counted: 1.
                        -- The window's own first column began at 10:30, and holds 50.
                        drawn { unit = Minute, every = Nothing }
                            { from = Just (minutes (45 - 1440)), to = minutes 45 }
                            [ sample "a" (minutes -1470) 1, sample "a" (minutes -1410) 50, sample "a" (minutes -1350) 2 ]
                            |> Tuple.second
                            |> List.take 3
                            |> Expect.equal
                                [ ( -1470, [ ( "a", 1 ) ] )
                                , ( -1410, [ ( "a", 50 ) ] )
                                , ( -1350, [ ( "a", 2 ) ] )
                                ]
                , test "keeps each day in its own column across a clock change, where the days are an hour apart" <|
                    \_ ->
                        -- Athens in autumn: three days begin at 21:00 UTC (summer time), then five at 22:00,
                        -- each a day's count of 10, 20 ... 80. The commonest offset is the winter one. Each
                        -- summer day is an hour before it, and is the same day, not the one before.
                        let
                            midnight =
                                -600

                            summer day =
                                sample "a" (minutes (midnight + day * 1440 + 21 * 60)) ((day + 1) * 10)

                            winter day =
                                sample "a" (minutes (midnight + day * 1440 + 22 * 60)) ((day + 1) * 10)
                        in
                        drawn { unit = Day, every = Just 1 }
                            { from = Just (minutes (midnight + 22 * 60 + 30)), to = minutes (midnight + 7 * 1440 + 22 * 60 + 30) }
                            (List.map summer [ 0, 1, 2 ] ++ List.map winter [ 3, 4, 5, 6, 7 ])
                            |> Tuple.second
                            |> List.map (Tuple.second >> List.map Tuple.second)
                            |> Expect.equal (List.map (\day -> [ day * 10 ]) (List.range 1 8))
                , test "keeps each merged day with its own pair across a clock change" <|
                    \_ ->
                        -- The same days, two to a bucket, counted from the epoch: 10 and 20, 30 and 40,
                        -- 50 and 60, 70 and 80. (10 October 2026 is an even day since the epoch, so the
                        -- pairs begin on the first of them.)
                        let
                            midnight =
                                -600

                            at day offset =
                                sample "a" (minutes (midnight + day * 1440 + offset * 60)) ((day + 1) * 10)
                        in
                        drawn { unit = Day, every = Just 2 }
                            { from = Just (minutes (midnight + 22 * 60 + 30)), to = minutes (midnight + 7 * 1440 + 22 * 60 + 30) }
                            [ at 0 21, at 1 21, at 2 21, at 3 22, at 4 22, at 5 22, at 6 22, at 7 22 ]
                            |> Tuple.second
                            |> List.map (Tuple.second >> List.map Tuple.second)
                            |> Expect.equal [ [ 30 ], [ 70 ], [ 110 ], [ 150 ] ]
                , test "keeps every count: none is lost to a grid that began elsewhere" <|
                    \_ ->
                        let
                            counts =
                                [ sample "a" (minutes -2220) 5, sample "a" (minutes -780) 7, sample "b" (minutes -780) 11 ]

                            window =
                                { from = Just (minutes -2760), to = minutes 0 }
                        in
                        Buckets.slots (Buckets.grid { unit = Day, every = Just 1 } window counts) window counts
                            |> List.concatMap (.counts >> Dict.values)
                            |> List.sum
                            |> Expect.equal 23
                ]
            ]
        , describe "columnStart"
            [ test "is where the column a moment falls in begins, so a window can open on a whole one" <|
                \_ ->
                    ( Buckets.columnStart (plain Minute 15) (Time.millisToPosix (tenOClock + 7 * 60000 + 30000))
                    , Buckets.columnStart (plain Hour 6) (minutes 7)
                    , Buckets.columnStart { unit = Hour, every = 2, offset = 30 * 60 * 1000, bucketedBy = Hour } (minutes 7)
                    )
                        |> Expect.equal ( minutes 0, minutes -240, minutes -90 )
            ]
        , describe "firstBucketFrom"
            [ test "is where the first bucket bitmagnet counted that begins at or after a moment begins, in the grid's own offset" <|
                \_ ->
                    [ Buckets.firstBucketFrom (plain Day 1) (minutes 0)
                    , Buckets.firstBucketFrom (plain Day 1) (minutes -600)
                    , Buckets.firstBucketFrom { unit = Day, every = 1, offset = 5 * 60 * 60000, bucketedBy = Day } (minutes -600)
                    , Buckets.firstBucketFrom { unit = Hour, every = 2, offset = 30 * 60000, bucketedBy = Hour } (minutes 7)
                    , Buckets.firstBucketFrom (plain Minute 15) (minutes 7)
                    ]
                        |> Expect.equal [ minutes 840, minutes -600, minutes -300, minutes 30, minutes 7 ]
            ]
        , describe "endsAfter"
            [ test "is whether a bucket bitmagnet counted reaches past a moment, so a page can tell one that began before its window from one wholly before it" <|
                \_ ->
                    [ -- The hour from 09:00 reaches past 09:59 and not past 10:00, where the next begins.
                      Buckets.endsAfter Hour (minutes -60) (minutes -1)
                    , Buckets.endsAfter Hour (minutes -60) (minutes 0)

                    -- A day in Athens began at 21:00 UTC and reaches past midnight UTC.
                    , Buckets.endsAfter Day (minutes -(13 * 60)) (minutes -600)
                    , Buckets.endsAfter Minute (minutes 0) (minutes 0)
                    , Buckets.endsAfter Minute (minutes -2) (minutes 0)
                    ]
                        |> Expect.equal [ True, False, True, True, False ]
            ]
        , describe "widthIn"
            [ test "says how many of a smaller unit a bucket is, for a field that names the unit chosen" <|
                \_ ->
                    [ Buckets.widthIn Minute (plain Hour 1)
                    , Buckets.widthIn Hour (plain Day 1)
                    , Buckets.widthIn Minute (plain Minute 15)
                    , Buckets.widthIn Minute (plain Hour 6)
                    ]
                        |> Expect.equal [ 60, 24, 15, 360 ]
            ]
        , test "names a bucket by its length" <|
            \_ ->
                List.map Buckets.label
                    [ plain Minute 1
                    , plain Minute 15
                    , plain Hour 1
                    , plain Hour 3
                    , plain Day 1
                    ]
                    |> Expect.equal [ "minute", "15 minutes", "hour", "3 hours", "day" ]
        , describe "slots"
            [ test "adds up the samples of a merged bucket, and starts the next one on the multiple" <|
                \_ ->
                    slotsOf (plain Minute 5)
                        { from = Just (minutes 0), to = minutes 5 }
                        [ sample "a" (minutes 1) 2
                        , sample "a" (minutes 3) 3
                        , sample "a" (minutes 5) 4
                        ]
                        |> Expect.equal [ ( 0, [ ( "a", 5 ) ] ), ( 5, [ ( "a", 4 ) ] ) ]
            , test "keeps series apart within a bucket" <|
                \_ ->
                    slotsOf (plain Minute 5)
                        { from = Just (minutes 0), to = minutes 4 }
                        [ sample "b" (minutes 1) 7
                        , sample "a" (minutes 2) 1
                        , sample "b" (minutes 3) 1
                        ]
                        |> Expect.equal [ ( 0, [ ( "a", 1 ), ( "b", 8 ) ] ) ]
            , test "starts a merged bucket on a multiple of its length since the epoch, not on the window" <|
                \_ ->
                    -- 10:00 UTC is the fifth hour of its block of five, so the block begins at 06:00.
                    slotsOf (plain Hour 5)
                        { from = Just (minutes 0), to = minutes 30 }
                        [ sample "a" (minutes 0) 7 ]
                        |> Expect.equal [ ( -240, [ ( "a", 7 ) ] ) ]
            , test "starts a day on UTC midnight" <|
                \_ ->
                    slotsOf (plain Day 1)
                        { from = Just (minutes 0), to = minutes 30 }
                        [ sample "a" (minutes -600) 7 ]
                        |> Expect.equal [ ( -600, [ ( "a", 7 ) ] ) ]
            , test "keeps an empty bucket between two that are not, so a gap shows as one" <|
                \_ ->
                    slotsOf (plain Minute 5)
                        { from = Just (minutes -3), to = minutes 22 }
                        [ sample "a" (minutes 0) 1
                        , sample "a" (minutes 20) 2
                        ]
                        |> Expect.equal
                            [ ( -5, [] )
                            , ( 0, [ ( "a", 1 ) ] )
                            , ( 5, [] )
                            , ( 10, [] )
                            , ( 15, [] )
                            , ( 20, [ ( "a", 2 ) ] )
                            ]
            , test "runs to the end of the window even where nothing has happened lately" <|
                \_ ->
                    slotsOf (plain Minute 1)
                        { from = Just (minutes 0), to = minutes 3 }
                        [ sample "a" (minutes 0) 1 ]
                        |> Expect.equal [ ( 0, [ ( "a", 1 ) ] ), ( 1, [] ), ( 2, [] ), ( 3, [] ) ]
            , test "draws the window with nothing counted in it when there are no samples" <|
                \_ ->
                    slotsOf (plain Minute 1)
                        { from = Just (minutes 0), to = minutes 2 }
                        []
                        |> Expect.equal [ ( 0, [] ), ( 1, [] ), ( 2, [] ) ]
            , test "gives a bucket from before the window's first bucket a column of its own, and loses none" <|
                \_ ->
                    slotsOf (plain Minute 5)
                        { from = Just (minutes 0), to = minutes 5 }
                        [ sample "a" (minutes -5) 9, sample "a" (minutes -60) 1 ]
                        |> List.filter (\( _, counts ) -> not (List.isEmpty counts))
                        |> Expect.equal [ ( -60, [ ( "a", 1 ) ] ), ( -5, [ ( "a", 9 ) ] ) ]
            , test "keeps a sample from before the window that shares its first bucket" <|
                \_ ->
                    -- The window opens at 10:03; the merged bucket it opens in began at 10:00.
                    slotsOf (plain Minute 5)
                        { from = Just (minutes 3), to = minutes 4 }
                        [ sample "a" (minutes 1) 9 ]
                        |> Expect.equal [ ( 0, [ ( "a", 9 ) ] ) ]
            , test "reaches a sample that is later than the end of the window" <|
                \_ ->
                    -- The browser's clock can run behind the server's; the data is not thrown away.
                    slotsOf (plain Minute 5)
                        { from = Just (minutes 0), to = minutes 5 }
                        [ sample "a" (minutes 12) 3 ]
                        |> Expect.equal [ ( 0, [] ), ( 5, [] ), ( 10, [ ( "a", 3 ) ] ) ]
            , test "without a start, begins at the earliest sample" <|
                \_ ->
                    slotsOf (plain Minute 5)
                        { from = Nothing, to = minutes 10 }
                        [ sample "a" (minutes 7) 1, sample "a" (minutes 2) 1 ]
                        |> Expect.equal [ ( 0, [ ( "a", 1 ) ] ), ( 5, [ ( "a", 1 ) ] ), ( 10, [] ) ]
            , test "without a start or any samples, has nothing to draw" <|
                \_ ->
                    slotsOf (plain Minute 5)
                        { from = Nothing, to = minutes 10 }
                        []
                        |> Expect.equal []
            , test "counts minutes of a window with no start in the hours they are merged into, whole hours and not a zone's offset" <|
                \_ ->
                    -- The request was planned by the minute, as a window with no start and nothing
                    -- returned is, and the answer is merged into hours. The :07 the samples begin at is
                    -- the minute they are in, not a time zone.
                    let
                        window =
                            { from = Nothing, to = minutes 0 }

                        counted =
                            [ sample "a" (minutes (-5 * 1440 + 7)) 2
                            , sample "a" (minutes (-3 * 1440 + 7)) 3
                            , sample "a" (minutes (-1440 + 37)) 4
                            ]

                        resolved =
                            Buckets.grid { unit = Minute, every = Nothing } window counted
                    in
                    ( resolved
                    , slotsOf resolved window counted
                        |> List.filter (\( _, counts ) -> not (List.isEmpty counts))
                    )
                        |> Expect.equal
                            ( { unit = Hour, every = 1, offset = 0, bucketedBy = Minute }
                            , [ ( -5 * 1440, [ ( "a", 2 ) ] ), ( -3 * 1440, [ ( "a", 3 ) ] ), ( -1440, [ ( "a", 4 ) ] ) ]
                            )
            , test "is empty for a window that ends before it begins" <|
                \_ ->
                    slotsOf (plain Minute 1)
                        { from = Just (minutes 5), to = minutes 0 }
                        []
                        |> Expect.equal []
            ]
        ]
