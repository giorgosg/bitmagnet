module Buckets exposing (Grid, Resolution, Sample, Slot, Window, columnStart, grid, label, slots, unlimited, widthIn)

{-| How a statistics page cuts time up: the arithmetic only, with no view in it, so that
the torrent timeline and the queue's can share it.

bitmagnet counts in buckets of a minute, an hour or a day (`MetricsBucketDuration`). A
coarser resolution such as "5 minutes" is made here by merging those buckets, which is what
the Angular UI does. Merged buckets are whole multiples of their length since the Unix epoch,
so a bucket starts at the same moment whatever window it is asked for in, and a page that
refreshes does not shuffle its columns.

bitmagnet cuts its days, and its hours in a zone that is not a whole number of hours from
UTC, where its database's time zone says (`date_trunc` runs in the session's zone). So a
grid has an `offset`, which it takes from the buckets it is given: none of them is thrown
away for beginning somewhere other than UTC would.

-}

import Dict exposing (Dict)
import Magnes.Api.Enum.MetricsBucketDuration exposing (MetricsBucketDuration(..))
import Time


{-| How a person chose to cut time up: a unit, and how many of it to a bucket. A multiplier
left to Magnes (`Nothing`) is picked from how long the window is.
-}
type alias Resolution =
    { unit : MetricsBucketDuration
    , every : Maybe Int
    }


{-| The length of one merged bucket, `every` of `unit`, and where the buckets begin: `offset`
milliseconds into a unit, counted from the epoch. A `Grid` is in the largest whole unit its
length makes, so a bucket of 60 minutes is an hour and one of 48 hours is two days, and that
is the unit bitmagnet is asked for.
-}
type alias Grid =
    { unit : MetricsBucketDuration
    , every : Int
    , offset : Int
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


widthMillis : Grid -> Int
widthMillis resolved =
    unitMillis resolved.unit * resolved.every


{-| Which bucket of the grid a moment falls in, counted from the epoch.
-}
indexOf : Grid -> Time.Posix -> Int
indexOf resolved at =
    (Time.posixToMillis at - resolved.offset) // widthMillis resolved


{-| The moment a bucket of the grid begins.
-}
startOf : Grid -> Int -> Time.Posix
startOf resolved index =
    Time.millisToPosix (index * widthMillis resolved + resolved.offset)


{-| Where the column a moment falls in begins. A request that opens on this moment, rather
than on the moment itself, has a first column that is whole: from the middle of one, bitmagnet
would count only the rest of it.
-}
columnStart : Grid -> Time.Posix -> Time.Posix
columnStart resolved at =
    startOf resolved (indexOf resolved at)


{-| How many of `unit`, which is no larger than the grid's own, a bucket is long: what a
field for a multiplier in the unit a person chose has to say.
-}
widthIn : MetricsBucketDuration -> Grid -> Int
widthIn unit resolved =
    widthMillis resolved // unitMillis unit


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


{-| The Angular UI's rule for a multiplier nobody chose. A window under 100 units long gets a
bucket to the unit; from 100 on, a bucket of five units for each hundred the window is long,
so between 20 and 40 columns, until the multiplier reaches 60. Past that the multiplier stays
at 60 and a longer window has more columns. `span` is how many of the unit the window is long.
-}
autoEvery : Int -> Int
autoEvery span =
    min 60 (max 1 (span // 100 * 5))


{-| The grid a resolution comes to: the multiplier a person typed, or the one picked for
the window, raised, as far as it takes and no further, to leave no more than `largestChart`
columns, and put in the largest whole unit. A caller that wants to say the chart was cut
down compares the grid with `unlimited`.

When the window has a start, the multiplier is chosen from the window alone, which is what
was asked for, and not from what came back. `samples` give the grid its offset, and, for a
window with no start, where it begins.

-}
grid : Resolution -> Window -> List (Sample series) -> Grid
grid =
    chosen True


{-| As `grid`, without the cap on columns: what would have been drawn if a chart could draw
any number.
-}
unlimited : Resolution -> Window -> List (Sample series) -> Grid
unlimited =
    chosen False


chosen : Bool -> Resolution -> Window -> List (Sample series) -> Grid
chosen capped resolution window samples =
    let
        span =
            spanOf resolution.unit window samples

        wanted =
            case resolution.every of
                Just typed ->
                    typed

                Nothing ->
                    autoEvery span

        -- A window `span` units long, in buckets of `n` units, has at most span / n + 2
        -- columns: one at each end, for a window that begins and ends part way through a
        -- bucket. So `n` is at least span / (largestChart - 2).
        fewest =
            if capped then
                ceiling (toFloat span / toFloat (largestChart - 2))

            else
                1

        ( unit, every ) =
            largestWhole resolution.unit (max wanted fewest)
    in
    { unit = unit, every = every, offset = offsetOf unit samples }


{-| How many of `unit` the window is long. A window with no start begins at the earliest
sample, and runs to the latest if that is later than its end.
-}
spanOf : MetricsBucketDuration -> Window -> List (Sample series) -> Int
spanOf unit window samples =
    let
        single =
            { unit = unit, every = 1, offset = 0 }
    in
    case window.from of
        Just from ->
            max 0 (indexOf single window.to - indexOf single from)

        Nothing ->
            let
                moments =
                    window.to :: List.map .at samples

                indices =
                    List.map (indexOf single) moments
            in
            Maybe.map2 (-) (List.maximum indices) (List.minimum indices) |> Maybe.withDefault 0


{-| `every` of `unit` as the largest unit that has a whole number of them in it.
-}
largestWhole : MetricsBucketDuration -> Int -> ( MetricsBucketDuration, Int )
largestWhole unit every =
    let
        width =
            unitMillis unit * every
    in
    List.filter (\larger -> unitMillis larger >= unitMillis unit && modBy (unitMillis larger) width == 0) [ Day, Hour, Minute ]
        |> List.head
        |> Maybe.withDefault unit
        |> (\larger -> ( larger, width // unitMillis larger ))


{-| Where into a unit most of the samples begin: the offset of the time zone they were
bucketed in. The commonest one, so that a bucket an hour out across a clock change does not
move the rest; the smallest of equals. None is zero, which is UTC.
-}
offsetOf : MetricsBucketDuration -> List (Sample series) -> Int
offsetOf unit samples =
    let
        tally =
            List.foldl
                (\sample ->
                    Dict.update (modBy (unitMillis unit) (Time.posixToMillis sample.at))
                        (\seen -> Just (Maybe.withDefault 0 seen + 1))
                )
                Dict.empty
                samples
    in
    Dict.foldl
        (\offset seen best ->
            case best of
                Just ( _, mostSeen ) ->
                    if seen > mostSeen then
                        Just ( offset, seen )

                    else
                        best

                Nothing ->
                    Just ( offset, seen )
        )
        Nothing
        tally
        |> Maybe.map Tuple.first
        |> Maybe.withDefault 0


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
the data shows as a gap. A sample from before the first bucket is counted in it, and none is
dropped.
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
    Dict.update (max first (indexOf resolved sample.at))
        (\bucket ->
            bucket
                |> Maybe.withDefault Dict.empty
                |> Dict.update sample.series (\count -> Just (Maybe.withDefault 0 count + sample.count))
                |> Just
        )
        totals
