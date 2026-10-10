module Buckets exposing (Grid, Sample, Slot, Window, grid, label, slots)

{-| How a statistics page cuts time up: the arithmetic only, with no view in it, so that
the torrent timeline and the queue's can share it.

bitmagnet counts in buckets of a minute, an hour or a day (`MetricsBucketDuration`). A
coarser resolution such as "5 minutes" is made here by merging those buckets, which is what
the Angular UI does. Merged buckets are whole multiples of their length since the Unix
epoch, so a bucket starts at the same moment whatever window it is asked for in, and a page
that refreshes does not shuffle its columns.

-}

import Dict exposing (Dict)
import Magnes.Api.Enum.MetricsBucketDuration exposing (MetricsBucketDuration(..))
import Time


{-| The length of one merged bucket: `every` of bitmagnet's `unit`.
-}
type alias Grid =
    { unit : MetricsBucketDuration
    , every : Int
    }


{-| The stretch of time a chart covers. `from` is `Nothing` for "everything", which starts
at the earliest sample instead. `to` is the moment the data was asked for.
-}
type alias Window =
    { from : Maybe Time.Posix
    , to : Time.Posix
    }


{-| Something that happened `count` times in the bitmagnet bucket that began at `at`, for
one `series`: a line on the chart.
-}
type alias Sample series =
    { series : series
    , at : Time.Posix
    , count : Int
    }


{-| One column of the chart: the moment it begins and what was counted in it. A series
that is missing from `counts` was counted at nothing.
-}
type alias Slot series =
    { start : Time.Posix
    , counts : Dict series Int
    }


unitMillis : MetricsBucketDuration -> Int
unitMillis unit =
    case unit of
        Minute ->
            60 * 1000

        Hour ->
            60 * 60 * 1000

        Day ->
            24 * 60 * 60 * 1000


{-| Which bucket of the grid a moment falls in, counted from the epoch.
-}
indexOf : Grid -> Time.Posix -> Int
indexOf resolved at =
    Time.posixToMillis at // (unitMillis resolved.unit * resolved.every)


{-| The Angular UI's rule for a multiplier nobody chose: about twenty columns, in steps of
five, and never more than 60. `span` is how many of the unit the window is long.
-}
autoEvery : Int -> Int
autoEvery span =
    min 60 (max 1 (span // 100 * 5))


{-| What one bucket is, for a heading to say "per" in front of: "minute", "15 minutes".
-}
label : Grid -> String
label resolved =
    let
        unit =
            case resolved.unit of
                Minute ->
                    "minute"

                Hour ->
                    "hour"

                Day ->
                    "day"
    in
    if resolved.every == 1 then
        unit

    else
        String.fromInt resolved.every ++ " " ++ unit ++ "s"


{-| The most columns a chart is given. Drawn, each is a point on every line and a row in
the table that stands in for the chart, and a week of buckets a minute (10,081 of them) took
four seconds to draw.
-}
largestChart : Int
largestChart =
    2000


{-| The grid a resolution comes to. A multiplier that was chosen is kept; one that was left
to Magnes is picked from how long the window is. Either is raised when it would make more
than `largestChart` columns, as far as it takes and no further, so a caller that wants to say
so can compare the grid it gets with the one it asked for.
-}
grid : { unit : MetricsBucketDuration, every : Maybe Int } -> Window -> List (Sample series) -> Grid
grid resolution window samples =
    let
        ( first, last ) =
            extent { unit = resolution.unit, every = 1 } window samples

        span =
            last - first

        -- One column more than the span's share, when the window does not begin on a
        -- boundary, hence the two in hand.
        fewestMultiplier =
            ceiling (toFloat span / toFloat (largestChart - 2))
    in
    { unit = resolution.unit
    , every =
        max fewestMultiplier
            (case resolution.every of
                Just every ->
                    every

                Nothing ->
                    autoEvery span
            )
    }


{-| The first and last bucket a chart covers: from the start of the window (or the
earliest sample), to the end of the window or the latest sample if that is later.
-}
extent : Grid -> Window -> List (Sample series) -> ( Int, Int )
extent resolved window samples =
    let
        sampled =
            List.map (.at >> indexOf resolved) samples

        last =
            List.maximum (indexOf resolved window.to :: sampled) |> Maybe.withDefault 0

        first =
            case window.from of
                Just from ->
                    indexOf resolved from

                Nothing ->
                    List.minimum (indexOf resolved window.to :: sampled) |> Maybe.withDefault 0
    in
    ( first, last )


{-| Every bucket of the window, in time order, with the samples that fall in it added up
by series. A bucket nothing fell in is still there, with nothing counted, so that a gap in
the data shows as a gap.
-}
slots : Grid -> Window -> List (Sample comparable) -> List (Slot comparable)
slots resolved window samples =
    case ( window.from, samples ) of
        ( Nothing, [] ) ->
            -- "Everything" with nothing in it begins nowhere.
            []

        _ ->
            let
                ( first, last ) =
                    extent resolved window samples

                -- A sample from before the window still counts if it shares the bucket the
                -- window opens in: the bucket is not split.
                totals =
                    List.foldl (addTo resolved first) Dict.empty samples
            in
            List.range first last
                |> List.map
                    (\index ->
                        { start = startOf resolved index
                        , counts = Dict.get index totals |> Maybe.withDefault Dict.empty
                        }
                    )


addTo : Grid -> Int -> Sample comparable -> Dict Int (Dict comparable Int) -> Dict Int (Dict comparable Int)
addTo resolved first sample totals =
    let
        index =
            indexOf resolved sample.at
    in
    if index < first then
        totals

    else
        Dict.update index
            (\bucket ->
                bucket
                    |> Maybe.withDefault Dict.empty
                    |> Dict.update sample.series (\count -> Just (Maybe.withDefault 0 count + sample.count))
                    |> Just
            )
            totals


{-| The moment a bucket of the grid begins.
-}
startOf : Grid -> Int -> Time.Posix
startOf resolved index =
    Time.millisToPosix (index * unitMillis resolved.unit * resolved.every)
