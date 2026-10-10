module StatsControls exposing
    ( AutoRefresh(..)
    , Controls
    , Resolution
    , Timeframe(..)
    , allRefreshes
    , allTimeframes
    , default
    , fromParams
    , refreshMillis
    , rows
    , toParams
    , window
    )

{-| The controls the statistics pages share: how far back to look, how coarsely to cut it
into buckets, and whether to look again by itself. The torrent timeline and the queue's
differ in their query and their series, not in how time is cut up, so this is one module
for both (tickets 05 and 06 in `.scratch/dashboard`).

Every choice is in the URL (`docs/adr/0002-put-shareable-search-state-in-real-urls.md`),
and each page's own filters ride beside these in its own query string.

-}

import Buckets
import Chip
import Html exposing (Html, button, input, text)
import Html.Attributes as Attributes exposing (attribute, class, placeholder, step, type_, value)
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


{-| The unit is what bitmagnet buckets by. A multiplier left to Magnes (`Nothing`) is picked
to give about twenty buckets (`Buckets.grid`), which is what the Angular UI does for a
resolution nobody typed a number for.
-}
type alias Resolution =
    { unit : MetricsBucketDuration
    , every : Maybe Int
    }


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


{-| The Angular UI's starting point for the torrent page: the last hour, a bucket a minute,
and, unlike there, no refreshing until it is asked for. A page that starts elsewhere, as the
queue's does, says so in `toParams` and `fromParams`.
-}
default : Controls
default =
    { timeframe = Hours1
    , resolution = { unit = Minute, every = Nothing }
    , refresh = Off
    }


allTimeframes : List Timeframe
allTimeframes =
    [ Minutes15, Minutes30, Hours1, Hours6, Hours12, Days1, Weeks1, AllTime ]


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

`resolved` is the multiplier the page's chart came to, which is shown in the multiplier's
field where none was chosen. `change` is told the controls as they would be, and the page
decides where that goes (the address bar).

-}
rows :
    { timeframes : List Timeframe
    , resolved : Maybe Int
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
would otherwise ask the server for each prefix. Empty, or not a number, hands the choice
back to Magnes.
-}
multiplierField : { a | resolved : Maybe Int, change : Controls -> msg } -> Controls -> Html msg
multiplierField config controls =
    input
        [ type_ "number"
        , class "stats-every"
        , Attributes.min "1"
        , Attributes.max (String.fromInt largestMultiplier)
        , step "1"
        , attribute "aria-label" "Buckets of how many"
        , value (controls.resolution.every |> Maybe.map String.fromInt |> Maybe.withDefault "")
        , placeholder (config.resolved |> Maybe.map String.fromInt |> Maybe.withDefault "auto")
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
                                        | every = String.toInt raw |> Maybe.andThen multiplier
                                    }
                            }
                    )
            )
        ]
        []


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
