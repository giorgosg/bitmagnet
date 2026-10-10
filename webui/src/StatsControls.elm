module StatsControls exposing
    ( AutoRefresh(..)
    , Controls
    , Request
    , Resolution
    , Timeframe(..)
    , allRefreshes
    , allTimeframes
    , boundedTimeframes
    , capNote
    , default
    , fromParams
    , queueDefault
    , refreshMillis
    , request
    , rows
    , toParams
    , window
    , withoutRefresh
    )

{-| The controls the statistics pages share: how far back to look, how coarsely to cut it
into buckets, and whether to look again by itself. The torrent timeline and the queue's
statistics will differ in their query and their series, not in how time is cut up, so this
is one module for both (tickets 06 and 05 in `.scratch/dashboard`).

Every choice is in the URL (`docs/adr/0002-put-shareable-search-state-in-real-urls.md`),
and each page's own filters ride beside these in its own query string.

-}

import Buckets
import Chip
import Html exposing (Html, button, input, p, text)
import Html.Attributes exposing (attribute, class, placeholder, type_, value)
import Html.Events as Events
import Json.Decode as Decode
import Magnes.Api.Enum.MetricsBucketDuration as BucketDuration exposing (MetricsBucketDuration(..))
import Time
import Url.Builder as Builder


{-| How far back the chart looks. The Angular UI's list; `AllTime` is the queue's alone,
because its query is bounded by what the queue holds and the torrent query is not.
-}
type Timeframe
    = Minutes15
    | Minutes30
    | Hours1
    | Hours6
    | Hours12
    | Days1
    | Weeks1
    | AllTime


{-| What a person chose to cut time into: a unit, and a multiplier of it or none, which
leaves it to Magnes to pick one from how long the timeframe is (`Buckets.grid`, which is the
Angular UI's rule). Bitmagnet is asked for the largest whole unit the result makes, so the
unit that was chosen is not always the one it buckets by.
-}
type alias Resolution =
    Buckets.Resolution


{-| How often the page asks again by itself. Off is the default: a page that polls is
something to turn on, not something to find running.
-}
type AutoRefresh
    = Off
    | Every10Seconds
    | Every30Seconds
    | EveryMinute
    | Every5Minutes


type alias Controls =
    { timeframe : Timeframe
    , resolution : Resolution
    , refresh : AutoRefresh
    }


{-| The Angular UI's starting point for the torrent page: the last hour, minutes, and, unlike
there, no refreshing until it is asked for. A page that starts elsewhere, as the queue's
statistics do, says so in `toParams` and `fromParams`.
-}
default : Controls
default =
    { timeframe = Hours1
    , resolution = { unit = Minute, every = Nothing }
    , refresh = Off
    }


{-| The Angular UI's starting point for the queue's statistics: everything the queue holds, by
the hour, and, unlike there, no refreshing until it is asked for.
-}
queueDefault : Controls
queueDefault =
    { timeframe = AllTime
    , resolution = { unit = Hour, every = Nothing }
    , refresh = Off
    }


allTimeframes : List Timeframe
allTimeframes =
    [ Minutes15, Minutes30, Hours1, Hours6, Hours12, Days1, Weeks1, AllTime ]


{-| The timeframes of a page that is bounded by what it asks for: all of them but the one of
everything, which is for a page whose query is bounded by what it holds.
-}
boundedTimeframes : List Timeframe
boundedTimeframes =
    List.filter ((/=) AllTime) allTimeframes


allRefreshes : List AutoRefresh
allRefreshes =
    [ Off, Every10Seconds, Every30Seconds, EveryMinute, Every5Minutes ]


{-| The most a multiplier can be. Far more than any timeframe needs, and few enough
that a bucket's length in milliseconds stays well inside a double's exact integers.
-}
largestMultiplier : Int
largestMultiplier =
    10000


{-| A multiplier worth keeping: at least 1 and at most `largestMultiplier`. Anything else
is as good as none, and is left to Magnes to pick.
-}
multiplier : Int -> Maybe Int
multiplier every =
    if every >= 1 && every <= largestMultiplier then
        Just every

    else
        Nothing


{-| The same look, not asked again by itself: what two looks are compared by, since how
often to look again is not what to look at.
-}
withoutRefresh : Controls -> Controls
withoutRefresh controls =
    { controls | refresh = Off }



-- WINDOW


{-| The stretch of time the controls ask about, as of `now`.
-}
window : Time.Posix -> Controls -> Buckets.Window
window now controls =
    { from =
        timeframeMillis controls.timeframe
            |> Maybe.map (\length -> Time.millisToPosix (Time.posixToMillis now - length))
    , to = now
    }


{-| What a page asks bitmagnet for: the unit to bucket by, and where the timeframe's first column
begins, or nothing for everything. An answer is read by the request it answers.
-}
type alias Request =
    { bucketDuration : MetricsBucketDuration
    , startTime : Maybe Time.Posix
    }


{-| What to ask bitmagnet for, as of `now`. It buckets by the unit the resolution comes to,
which is the largest whole one it makes (`Buckets.grid`): a week of minutes merged into hours is
asked for as hours. `startTime` is where the timeframe's first column begins, not the moment the
timeframe reaches back to: counted from the middle of a column, bitmagnet would give the first
one only its share of it. Everything has no start, and is asked for in the unit chosen, since
nothing is known yet of how much there is.

That is as far as Magnes can know where bitmagnet's columns begin. It cuts its days, and its
hours in a zone that is not a whole number of hours from UTC, in its database's time zone, so
against a database that is not on UTC the first of those can still be short.

-}
request : Time.Posix -> Controls -> Request
request now controls =
    let
        span =
            window now controls

        planned =
            Buckets.grid controls.resolution span []
    in
    { bucketDuration = planned.bucketedBy
    , startTime = Maybe.map (Buckets.columnStart planned) span.from
    }


timeframeMillis : Timeframe -> Maybe Int
timeframeMillis timeframe =
    case timeframe of
        Minutes15 ->
            Just (15 * minute)

        Minutes30 ->
            Just (30 * minute)

        Hours1 ->
            Just (60 * minute)

        Hours6 ->
            Just (6 * 60 * minute)

        Hours12 ->
            Just (12 * 60 * minute)

        Days1 ->
            Just (24 * 60 * minute)

        Weeks1 ->
            Just (7 * 24 * 60 * minute)

        AllTime ->
            Nothing


minute : Int
minute =
    60 * 1000


{-| Milliseconds between one look and the next, or `Nothing` when it does not look again.
-}
refreshMillis : AutoRefresh -> Maybe Float
refreshMillis refresh =
    case refresh of
        Off ->
            Nothing

        Every10Seconds ->
            Just 10000

        Every30Seconds ->
            Just 30000

        EveryMinute ->
            Just 60000

        Every5Minutes ->
            Just 300000



-- URL


timeframeParam : Timeframe -> String
timeframeParam timeframe =
    case timeframe of
        Minutes15 ->
            "15m"

        Minutes30 ->
            "30m"

        Hours1 ->
            "1h"

        Hours6 ->
            "6h"

        Hours12 ->
            "12h"

        Days1 ->
            "1d"

        Weeks1 ->
            "1w"

        AllTime ->
            "all"


refreshParam : AutoRefresh -> String
refreshParam refresh =
    case refresh of
        Off ->
            "off"

        Every10Seconds ->
            "10s"

        Every30Seconds ->
            "30s"

        EveryMinute ->
            "1m"

        Every5Minutes ->
            "5m"


{-| What the controls add to an address, given the controls the page starts with. A choice
that is that default is left out, so an ordinary look is a bare path.
-}
toParams : Controls -> Controls -> List Builder.QueryParameter
toParams start controls =
    List.concat
        [ if controls.timeframe == start.timeframe then
            []

          else
            [ Builder.string "timeframe" (timeframeParam controls.timeframe) ]
        , if controls.resolution.unit == start.resolution.unit then
            []

          else
            [ Builder.string "resolution" (BucketDuration.toString controls.resolution.unit) ]
        , case controls.resolution.every of
            Just every ->
                [ Builder.int "every" every ]

            Nothing ->
                []
        , if controls.refresh == start.refresh then
            []

          else
            [ Builder.string "refresh" (refreshParam controls.refresh) ]
        ]


{-| The controls an address says. What it does not say, or says in a form not recognised,
is the page's `defaults`, so an old or hand-written link still opens the page. `timeframes`
are the ones the page offers: a link to one it does not is read as the default too.
-}
fromParams :
    { defaults : Controls
    , timeframes : List Timeframe
    , timeframe : Maybe String
    , resolution : Maybe String
    , every : Maybe Int
    , refresh : Maybe String
    }
    -> Controls
fromParams params =
    { timeframe =
        params.timeframe
            |> Maybe.andThen (\raw -> List.filter (\timeframe -> timeframeParam timeframe == raw) params.timeframes |> List.head)
            |> Maybe.withDefault params.defaults.timeframe
    , resolution =
        { unit =
            params.resolution
                |> Maybe.andThen BucketDuration.fromString
                |> Maybe.withDefault params.defaults.resolution.unit
        , every = params.every |> Maybe.andThen multiplier
        }
    , refresh =
        params.refresh
            |> Maybe.andThen (\raw -> List.filter (\refresh -> refreshParam refresh == raw) allRefreshes |> List.head)
            |> Maybe.withDefault params.defaults.refresh
    }



-- VIEW


{-| The rows of chips every statistics page opens with, to be joined by the page's own
filters in the same `facets` box: the timeframe, the resolution, and how often to look again.

`picked` is the multiplier the page's chart came to, in the unit that was chosen, which is
shown in the multiplier's field where none was typed. `change` is told the controls as they
would be, and the page decides where that goes (the address bar).

-}
rows :
    { timeframes : List Timeframe
    , picked : Maybe Int
    , change : Controls -> msg
    , refreshRequested : msg
    }
    -> Controls
    -> List (Html msg)
rows config controls =
    let
        choice label selected onChoose =
            Chip.view { label = label, count = Nothing, selected = selected, onToggle = onChoose }
    in
    [ Chip.facet "timeframe"
        (List.map
            (\timeframe ->
                choice (timeframeLabel timeframe)
                    (controls.timeframe == timeframe)
                    (config.change { controls | timeframe = timeframe })
            )
            config.timeframes
        )
    , Chip.facet "resolution"
        (multiplierField config controls
            :: List.map
                (\unit ->
                    choice (unitLabel unit)
                        (controls.resolution.unit == unit)
                        (if controls.resolution.unit == unit then
                            config.change controls

                         else
                            -- A multiplier belongs to the unit it was typed for: 15 minutes
                            -- says nothing about 15 days.
                            config.change { controls | resolution = { unit = unit, every = Nothing } }
                        )
                )
                BucketDuration.list
        )
    , Chip.facet "refresh"
        (List.map
            (\refresh ->
                choice (refreshLabel refresh)
                    (controls.refresh == refresh)
                    (config.change { controls | refresh = refresh })
            )
            allRefreshes
            ++ [ button [ class "chip", type_ "button", Events.onClick config.refreshRequested ] [ text "Refresh now" ] ]
        )
    ]


{-| Applied when it is left, as the Angular UI's is: a number typed a digit at a time
would otherwise ask the server for each prefix. A number is brought to the nearest whole one a
multiplier can be, so that what was meant is kept, and anything that is not a number hands the
choice back to Magnes.

It is a text field with a numeric keypad rather than a number field. A number field reads as
empty for what is not a number, "e" or a lone "-", and empty is what the page already shows,
so nothing was drawn again and what was typed stayed in it. Elm puts a field's value back on
every draw, but only if it differs from what the DOM holds.

-}
multiplierField : { a | picked : Maybe Int, change : Controls -> msg } -> Controls -> Html msg
multiplierField config controls =
    input
        [ type_ "text"
        , attribute "inputmode" "numeric"
        , class "stats-every"
        , attribute "aria-label" "Buckets of how many"
        , value (controls.resolution.every |> Maybe.map String.fromInt |> Maybe.withDefault "")
        , placeholder (config.picked |> Maybe.map String.fromInt |> Maybe.withDefault "auto")
        , Events.on "change"
            (Events.targetValue
                |> Decode.map
                    (\raw ->
                        let
                            resolution =
                                controls.resolution
                        in
                        config.change
                            { controls
                                | resolution =
                                    { resolution
                                        | every = String.toFloat raw |> Maybe.map (round >> clamp 1 largestMultiplier)
                                    }
                            }
                    )
            )
        ]
        []


{-| Said whenever a chart was cut down to fit, however its multiplier came about: `grid` is what
was drawn and `wanted` what would have been, had a chart been able to draw any number of
columns (`Buckets.grid` and `Buckets.unlimited`). It is said of the chart drawn, not of the
choices since made.
-}
capNote : { a | grid : Buckets.Grid, wanted : Buckets.Grid } -> Html msg
capNote drawn =
    if drawn.grid.unit == drawn.wanted.unit && drawn.grid.every == drawn.wanted.every then
        text ""

    else
        p [ class "stats-note" ]
            [ text
                ("Drawn per "
                    ++ Buckets.label drawn.grid
                    ++ ", not per "
                    ++ Buckets.label drawn.wanted
                    ++ ": that many buckets are more than the chart can draw."
                )
            ]


timeframeLabel : Timeframe -> String
timeframeLabel timeframe =
    case timeframe of
        Minutes15 ->
            "15 minutes"

        Minutes30 ->
            "30 minutes"

        Hours1 ->
            "1 hour"

        Hours6 ->
            "6 hours"

        Hours12 ->
            "12 hours"

        Days1 ->
            "1 day"

        Weeks1 ->
            "1 week"

        AllTime ->
            "all time"


unitLabel : MetricsBucketDuration -> String
unitLabel unit =
    case unit of
        Minute ->
            "minutes"

        Hour ->
            "hours"

        Day ->
            "days"


refreshLabel : AutoRefresh -> String
refreshLabel refresh =
    case refresh of
        Off ->
            "off"

        Every10Seconds ->
            "10 seconds"

        Every30Seconds ->
            "30 seconds"

        EveryMinute ->
            "1 minute"

        Every5Minutes ->
            "5 minutes"
