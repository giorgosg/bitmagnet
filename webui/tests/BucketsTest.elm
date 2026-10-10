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


{-| The window of a timeframe that ends at 10:00: the last `length` minutes.
-}
lastMinutes : Int -> Buckets.Window
lastMinutes length =
    { from = Just (minutes -length), to = minutes 0 }


sample : String -> Time.Posix -> Int -> Buckets.Sample String
sample series at count =
    { series = series, at = at, count = count }


{-| A slot as a person would write it down: when it starts, and what is in it.
-}
slotsOf : Buckets.Grid -> Buckets.Window -> List (Buckets.Sample String) -> List ( Int, List ( String, Int ) )
slotsOf resolved window samples =
    Buckets.slots resolved window samples
        |> List.map (\slot -> ( (Time.posixToMillis slot.start - tenOClock) // 60000, Dict.toList slot.counts ))


suite : Test
suite =
    describe "Buckets"
        [ describe "grid"
            [ test "keeps a multiplier that was asked for" <|
                \_ ->
                    Buckets.grid { unit = Hour, every = Just 3 } (lastMinutes 360) []
                        |> Expect.equal { unit = Hour, every = 3 }
            , test "chooses one bucket a minute over an hour, as the Angular UI does" <|
                \_ ->
                    -- 60 minutes of span is under the 100 that earns a step of five.
                    Buckets.grid { unit = Minute, every = Nothing } (lastMinutes 60) []
                        |> Expect.equal { unit = Minute, every = 1 }
            , test "chooses fifteen minutes over six hours" <|
                \_ ->
                    -- 360 minutes: floor (360 / 100) = 3 steps of five.
                    Buckets.grid { unit = Minute, every = Nothing } (lastMinutes 360) []
                        |> Expect.equal { unit = Minute, every = 15 }
            , test "never chooses more than an hour of minutes" <|
                \_ ->
                    Buckets.grid { unit = Minute, every = Nothing } (lastMinutes (7 * 24 * 60)) []
                        |> Expect.equal { unit = Minute, every = 60 }
            , test "chooses five hours over a week of hours, and a day over a week of days" <|
                \_ ->
                    ( Buckets.grid { unit = Hour, every = Nothing } (lastMinutes (7 * 24 * 60)) []
                    , Buckets.grid { unit = Day, every = Nothing } (lastMinutes (7 * 24 * 60)) []
                    )
                        |> Expect.equal ( { unit = Hour, every = 5 }, { unit = Day, every = 1 } )
            , test "raises a multiplier that was asked for when it would make more columns than a chart can draw" <|
                \_ ->
                    -- A week of minutes is 10,081 columns; 6 of them to a bucket is under 2,000.
                    Buckets.grid { unit = Minute, every = Just 1 } (lastMinutes (7 * 24 * 60)) []
                        |> Expect.equal { unit = Minute, every = 6 }
            , test "keeps what a chart can draw: a day of minutes, and 1,998 of them, are left as asked" <|
                \_ ->
                    ( Buckets.grid { unit = Minute, every = Just 1 } (lastMinutes (24 * 60)) []
                    , Buckets.grid { unit = Minute, every = Just 1 } (lastMinutes 1998) []
                    )
                        |> Expect.equal ( { unit = Minute, every = 1 }, { unit = Minute, every = 1 } )
            , test "never makes more than 2,000 columns, however long the window" <|
                \_ ->
                    let
                        window =
                            { from = Nothing, to = minutes 0 }

                        years =
                            [ sample "a" (minutes (-5 * 365 * 24 * 60)) 1 ]

                        chosen =
                            Buckets.grid { unit = Minute, every = Nothing } window years
                    in
                    Buckets.slots chosen window years
                        |> List.length
                        |> Expect.atMost 2000
            , test "without a start, spans from the earliest sample to the end of the window" <|
                \_ ->
                    -- Six hours of data and no start to the window: as six hours would be.
                    Buckets.grid { unit = Minute, every = Nothing }
                        { from = Nothing, to = minutes 0 }
                        [ sample "a" (minutes -360) 1 ]
                        |> Expect.equal { unit = Minute, every = 15 }
            , test "counts a sample later than the end of the window, as the Angular UI does" <|
                \_ ->
                    -- The browser's clock can run behind the server's.
                    Buckets.grid { unit = Minute, every = Nothing }
                        { from = Just (minutes -60), to = minutes -60 }
                        [ sample "a" (minutes 300) 1 ]
                        |> Expect.equal { unit = Minute, every = 15 }
            ]
        , test "names a bucket by its length" <|
            \_ ->
                List.map Buckets.label
                    [ { unit = Minute, every = 1 }
                    , { unit = Minute, every = 15 }
                    , { unit = Hour, every = 1 }
                    , { unit = Hour, every = 3 }
                    , { unit = Day, every = 1 }
                    ]
                    |> Expect.equal [ "minute", "15 minutes", "hour", "3 hours", "day" ]
        , describe "slots"
            [ test "adds up the samples of a merged bucket, and starts the next one on the multiple" <|
                \_ ->
                    slotsOf { unit = Minute, every = 5 }
                        { from = Just (minutes 0), to = minutes 5 }
                        [ sample "a" (minutes 1) 2
                        , sample "a" (minutes 3) 3
                        , sample "a" (minutes 5) 4
                        ]
                        |> Expect.equal [ ( 0, [ ( "a", 5 ) ] ), ( 5, [ ( "a", 4 ) ] ) ]
            , test "keeps series apart within a bucket" <|
                \_ ->
                    slotsOf { unit = Minute, every = 5 }
                        { from = Just (minutes 0), to = minutes 4 }
                        [ sample "b" (minutes 1) 7
                        , sample "a" (minutes 2) 1
                        , sample "b" (minutes 3) 1
                        ]
                        |> Expect.equal [ ( 0, [ ( "a", 1 ), ( "b", 8 ) ] ) ]
            , test "starts a merged bucket on a multiple of its length since the epoch, not on the window" <|
                \_ ->
                    -- 10:00 UTC is the fifth hour of its block of five, so the block begins at 06:00.
                    slotsOf { unit = Hour, every = 5 }
                        { from = Just (minutes 0), to = minutes 30 }
                        [ sample "a" (minutes 30) 7 ]
                        |> Expect.equal [ ( -240, [ ( "a", 7 ) ] ) ]
            , test "starts a day on UTC midnight" <|
                \_ ->
                    slotsOf { unit = Day, every = 1 }
                        { from = Just (minutes 0), to = minutes 30 }
                        [ sample "a" (minutes 30) 7 ]
                        |> Expect.equal [ ( -600, [ ( "a", 7 ) ] ) ]
            , test "keeps an empty bucket between two that are not, so a gap shows as one" <|
                \_ ->
                    slotsOf { unit = Minute, every = 5 }
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
                    slotsOf { unit = Minute, every = 1 }
                        { from = Just (minutes 0), to = minutes 3 }
                        [ sample "a" (minutes 0) 1 ]
                        |> Expect.equal [ ( 0, [ ( "a", 1 ) ] ), ( 1, [] ), ( 2, [] ), ( 3, [] ) ]
            , test "draws the window with nothing counted in it when there are no samples" <|
                \_ ->
                    slotsOf { unit = Minute, every = 1 }
                        { from = Just (minutes 0), to = minutes 2 }
                        []
                        |> Expect.equal [ ( 0, [] ), ( 1, [] ), ( 2, [] ) ]
            , test "drops a sample from before the window's first bucket" <|
                \_ ->
                    slotsOf { unit = Minute, every = 5 }
                        { from = Just (minutes 0), to = minutes 5 }
                        [ sample "a" (minutes -2) 9 ]
                        |> Expect.equal [ ( 0, [] ), ( 5, [] ) ]
            , test "keeps a sample from before the window that shares its first bucket" <|
                \_ ->
                    -- The window opens at 10:03; the merged bucket it opens in began at 10:00.
                    slotsOf { unit = Minute, every = 5 }
                        { from = Just (minutes 3), to = minutes 4 }
                        [ sample "a" (minutes 1) 9 ]
                        |> Expect.equal [ ( 0, [ ( "a", 9 ) ] ) ]
            , test "reaches a sample that is later than the end of the window" <|
                \_ ->
                    -- The browser's clock can run behind the server's; the data is not thrown away.
                    slotsOf { unit = Minute, every = 5 }
                        { from = Just (minutes 0), to = minutes 5 }
                        [ sample "a" (minutes 12) 3 ]
                        |> Expect.equal [ ( 0, [] ), ( 5, [] ), ( 10, [ ( "a", 3 ) ] ) ]
            , test "without a start, begins at the earliest sample" <|
                \_ ->
                    slotsOf { unit = Minute, every = 5 }
                        { from = Nothing, to = minutes 10 }
                        [ sample "a" (minutes 7) 1, sample "a" (minutes 2) 1 ]
                        |> Expect.equal [ ( 0, [ ( "a", 1 ) ] ), ( 5, [ ( "a", 1 ) ] ), ( 10, [] ) ]
            , test "without a start or any samples, has nothing to draw" <|
                \_ ->
                    slotsOf { unit = Minute, every = 5 }
                        { from = Nothing, to = minutes 10 }
                        []
                        |> Expect.equal []
            , test "is empty for a window that ends before it begins" <|
                \_ ->
                    slotsOf { unit = Minute, every = 1 }
                        { from = Just (minutes 5), to = minutes 0 }
                        []
                        |> Expect.equal []
            ]
        ]
