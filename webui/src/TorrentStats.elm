module TorrentStats exposing
    ( Bucket
    , Line
    , Messages
    , Page
    , Plot
    , Source
    , State
    , Statistics
    , asksTheSame
    , empty
    , failed
    , fetch
    , inFlight
    , loaded
    , plot
    , query
    , refreshing
    , view
    )

{-| How fast the index is growing: the torrents bitmagnet has seen, per time bucket and per
source, as new or as updated.

The counts are bitmagnet's `torrent.metrics`, which reads `torrents_torrent_sources`: a row
for each torrent a source has reported. A row is counted once, in the bucket of its latest
`updated_at`, and is `updated` when that is more than an hour after its `created_at`. So a
torrent first reported in one bucket and updated again later moves to the later bucket, and from
new to updated: what an earlier bucket counted as new can shrink.

What is looked at is in the URL (`Route.TorrentStatsParams`); the timeframe, resolution and
refresh are `StatsControls`', shared with the queue's statistics, and the buckets are cut up
by `Buckets`. This module is what is particular to torrents: the query, the sources, and
the lines they make.

-}

import ApiError
import Bitmagnet
import Buckets
import Charts
import Chip
import Dict exposing (Dict)
import Format
import Graphql.Http
import Graphql.Operation exposing (RootQuery)
import Graphql.OptionalArgument exposing (OptionalArgument(..))
import Graphql.SelectionSet as SelectionSet exposing (SelectionSet)
import Html exposing (Html, div, h1, p, text)
import Html.Attributes exposing (attribute, class, classList)
import Magnes.Api.InputObject as InputObject
import Magnes.Api.Object
import Magnes.Api.Object.TorrentListSourcesResult as ListSourcesResult
import Magnes.Api.Object.TorrentMetricsBucket as MetricsBucket
import Magnes.Api.Object.TorrentMetricsQueryResult as MetricsResult
import Magnes.Api.Object.TorrentQuery as TorrentQuery
import Magnes.Api.Object.TorrentSource as TorrentSource
import Magnes.Api.Query as Query
import Route
import StatsControls
import Task
import Time


{-| What bitmagnet counted: `count` torrents that `source` reported, whose latest update
fell in the bucket that begins at `bucket`.
-}
type alias Bucket =
    { source : String
    , bucket : Time.Posix
    , updated : Bool
    , count : Int
    }


type alias Source =
    { key : String
    , name : String
    }


{-| bitmagnet's answer, with when it was asked for. The window is read back from that
moment rather than from the clock at the time of drawing, so a chart redrawn later still
ends where its data does.
-}
type alias Statistics =
    { asked : Time.Posix
    , buckets : List Bucket
    , sources : List Source
    }



-- REQUESTS


{-| The clock is read as the request is made, so the start of the timeframe and the end of
the chart are the same moment, and the answer carries it.
-}
fetch : String -> Route.TorrentStatsParams -> (Result (Graphql.Http.Error Statistics) Statistics -> msg) -> Cmd msg
fetch apiUrl params toMsg =
    Time.now
        |> Task.andThen
            (\now ->
                query now params
                    |> Bitmagnet.queryRequest apiUrl
                    |> Graphql.Http.toTask
            )
        |> Task.attempt toMsg


{-| bitmagnet buckets by the unit of the resolution, and the multiplier is made here
(`Buckets`). `startTime` is a bound on a row's `updated_at`, inclusive. No `endTime` is
sent: it would cut off rows newer than a browser clock that runs behind the server's.

No `sources` is all of them. An empty list would be a filter that matches nothing, so it
is left out.

-}
query : Time.Posix -> Route.TorrentStatsParams -> SelectionSet Statistics RootQuery
query now params =
    let
        input =
            InputObject.buildTorrentMetricsQueryInput
                { bucketDuration = params.controls.resolution.unit }
                (\optionals ->
                    { optionals
                        | sources =
                            if List.isEmpty params.sources then
                                Absent

                            else
                                Present params.sources
                        , startTime =
                            case (StatsControls.window now params.controls).from of
                                Just from ->
                                    Present from

                                Nothing ->
                                    Absent
                    }
                )
    in
    Query.torrent
        (SelectionSet.map2 (\buckets sources -> { asked = now, buckets = buckets, sources = sources })
            (TorrentQuery.metrics { input = input } (MetricsResult.buckets bucketSelection))
            (TorrentQuery.listSources (ListSourcesResult.sources sourceSelection))
        )


bucketSelection : SelectionSet Bucket Magnes.Api.Object.TorrentMetricsBucket
bucketSelection =
    SelectionSet.map4 Bucket
        MetricsBucket.source
        MetricsBucket.bucket
        MetricsBucket.updated
        MetricsBucket.count


sourceSelection : SelectionSet Source Magnes.Api.Object.TorrentSource
sourceSelection =
    SelectionSet.map2 Source TorrentSource.key TorrentSource.name



-- PLOT


{-| One line of the chart: which count it follows in each column, what it is called, and
how it is drawn.
-}
type alias Line =
    { key : Int
    , label : String
    , ink : Charts.Ink
    }


{-| The chart's columns, in time order, and the lines that run through them. `grid` is what
the resolution came to, so a page can say what a bucket is.
-}
type alias Plot =
    { grid : Buckets.Grid
    , lines : List Line
    , slots : List (Buckets.Slot Int)
    }


{-| Most sources the chart draws apart. A source is drawn as a pair of lines, and the
inks tell three pairs apart (`Charts.Ink`); more than that would be lines nobody can name.
-}
largestPair : Int
largestPair =
    3


{-| The chart of an answer, under the controls it was asked with.

Each source with anything counted gets a line for its new torrents and one for its updated,
in the order of the sources' keys, inked by position. Past three sources the two with the
most counted are drawn apart and the rest are added together as "Other sources", so the
chart still adds up to everything bitmagnet counted. Choosing sources narrows the query, and
so brings them back apart.

-}
plot : Route.TorrentStatsParams -> Statistics -> Plot
plot params statistics =
    let
        pairs =
            pairsOf statistics

        pairOf source =
            pairs
                |> List.indexedMap Tuple.pair
                |> List.filter (\( _, pair ) -> List.member source pair.sources)
                |> List.head
                |> Maybe.map Tuple.first
                |> Maybe.withDefault (List.length pairs - 1)

        samples =
            List.map
                (\bucket ->
                    { series = pairOf bucket.source * 2 + boolToInt bucket.updated
                    , at = bucket.bucket
                    , count = bucket.count
                    }
                )
                statistics.buckets

        window =
            StatsControls.window statistics.asked params.controls

        resolved =
            Buckets.grid params.controls.resolution window samples
    in
    { grid = resolved
    , lines = List.indexedMap linesOf pairs |> List.concat
    , slots =
        -- A timeframe in which nothing was counted has no chart: a flat line along zero
        -- says less than saying so.
        if List.isEmpty samples then
            []

        else
            Buckets.slots resolved window samples
    }


type alias Pair =
    { name : String
    , sources : List String
    }


{-| Who is drawn together, in order: each source with anything counted, by key, or the two
busiest by key and the rest as one.
-}
pairsOf : Statistics -> List Pair
pairsOf statistics =
    let
        totals : Dict String Int
        totals =
            List.foldl
                (\bucket -> Dict.update bucket.source (\total -> Just (Maybe.withDefault 0 total + bucket.count)))
                Dict.empty
                statistics.buckets

        counted =
            Dict.keys totals

        nameOf key =
            statistics.sources
                |> List.filter (\source -> source.key == key)
                |> List.head
                |> Maybe.map .name
                |> Maybe.withDefault key
    in
    if List.length counted <= largestPair then
        List.map (\key -> { name = nameOf key, sources = [ key ] }) counted

    else
        let
            busiest =
                Dict.toList totals
                    |> List.sortBy (\( key, total ) -> ( negate total, key ))
                    |> List.take (largestPair - 1)
                    |> List.map Tuple.first
        in
        List.filter (\key -> List.member key busiest) counted
            |> List.map (\key -> { name = nameOf key, sources = [ key ] })
            |> (\apart -> apart ++ [ { name = "Other sources", sources = List.filter (\key -> not (List.member key busiest)) counted } ])


linesOf : Int -> Pair -> List Line
linesOf index pair =
    let
        ( new, updated ) =
            case index of
                0 ->
                    ( Charts.Accent, Charts.AccentSoft )

                1 ->
                    ( Charts.Strong, Charts.StrongSoft )

                _ ->
                    ( Charts.Muted, Charts.Faint )
    in
    [ { key = index * 2, label = pair.name ++ ": new", ink = new }
    , { key = index * 2 + 1, label = pair.name ++ ": updated", ink = updated }
    ]


boolToInt : Bool -> Int
boolToInt flag =
    if flag then
        1

    else
        0



-- STATE


type alias Page =
    { asked : Time.Posix
    , sources : List Source
    , plot : Plot
    }


type Listing
    = Loading
    | Failed ApiError.Failure
    | Loaded Page


{-| `refreshing` is set while another look is on its way over one already shown, which
stays on screen, quieter, until it arrives. `lastFailure` is a look that did not arrive:
the old chart stays, with the reason, because a refresh that fails is not a reason to take
away what was there.
-}
type alias State =
    { listing : Listing
    , refreshing : Bool
    , lastFailure : Maybe ApiError.Failure
    }


empty : State
empty =
    { listing = Loading, refreshing = False, lastFailure = Nothing }


{-| Another look has been asked for.
-}
refreshing : State -> State
refreshing state =
    { state | refreshing = True }


{-| Whether an answer is still to come, so a timer does not ask again over the top of it.
-}
inFlight : State -> Bool
inFlight state =
    case state.listing of
        Loading ->
            True

        _ ->
            state.refreshing


loaded : Route.TorrentStatsParams -> Statistics -> State -> State
loaded params statistics state =
    { state
        | listing = Loaded { asked = statistics.asked, sources = statistics.sources, plot = plot params statistics }
        , refreshing = False
        , lastFailure = Nothing
    }


failed : ApiError.Failure -> State -> State
failed failure state =
    case state.listing of
        Loaded _ ->
            { state | refreshing = False, lastFailure = Just failure }

        _ ->
            { state | listing = Failed failure, refreshing = False }


{-| Whether two addresses ask bitmagnet the same question. Auto-refresh is how often to
ask again, not what to ask, so changing it does not need an answer from the server.
-}
asksTheSame : Route.TorrentStatsParams -> Route.TorrentStatsParams -> Bool
asksTheSame one other =
    let
        withoutRefresh params =
            let
                controls =
                    params.controls
            in
            { params | controls = { controls | refresh = StatsControls.Off } }
    in
    withoutRefresh one == withoutRefresh other



-- VIEW


type alias Messages msg =
    { navigate : Route.TorrentStatsParams -> msg
    , refreshRequested : msg
    }


view : Time.Zone -> Messages msg -> Route.TorrentStatsParams -> State -> Html msg
view zone messages params state =
    div [ class "page torrent-stats" ]
        [ h1 [] [ text "Torrent statistics" ]
        , choices messages params state
        , case state.listing of
            Loading ->
                p [ class "notice" ] [ text "Loading statistics…" ]

            Failed failure ->
                p [ class "notice error", attribute "role" "alert" ] [ text (ApiError.toMessage failure) ]

            Loaded page ->
                shown zone params state page
        ]


choices : Messages msg -> Route.TorrentStatsParams -> State -> Html msg
choices messages params state =
    let
        navigate controlsChosen =
            messages.navigate { params | controls = controlsChosen }

        known =
            case state.listing of
                Loaded page ->
                    page.sources

                _ ->
                    []
    in
    div [ class "facets stats-controls" ]
        (StatsControls.rows
            { timeframes = List.filter ((/=) StatsControls.AllTime) StatsControls.allTimeframes
            , resolved =
                case state.listing of
                    Loaded page ->
                        Just page.plot.grid.every

                    _ ->
                        Nothing
            , change = navigate
            , refreshRequested = messages.refreshRequested
            }
            params.controls
            ++ [ Chip.facet "source"
                    (List.map
                        (\source ->
                            Chip.view
                                { label = source.name
                                , count = Nothing
                                , selected = List.member source.key params.sources
                                , onToggle = messages.navigate { params | sources = toggleIn source.key params.sources }
                                }
                        )
                        (withChosen params.sources known)
                    )
               ]
        )


{-| A chosen source stays offered when bitmagnet does not list it, named by its key, so a
link naming one can still be undone.
-}
withChosen : List String -> List Source -> List Source
withChosen chosen known =
    known
        ++ List.filterMap
            (\key ->
                if List.any (\source -> source.key == key) known then
                    Nothing

                else
                    Just { key = key, name = key }
            )
            chosen


toggleIn : a -> List a -> List a
toggleIn value values =
    if List.member value values then
        List.filter ((/=) value) values

    else
        values ++ [ value ]


{-| The chart, dimmed while another look is on its way, with the reason when a look did not
come, and how to read what it counts.
-}
shown : Time.Zone -> Route.TorrentStatsParams -> State -> Page -> Html msg
shown zone params state page =
    div
        [ classList [ ( "stats", True ), ( "stats-refreshing", state.refreshing ) ]
        , attribute "aria-busy"
            (if state.refreshing then
                "true"

             else
                "false"
            )
        ]
        [ case state.lastFailure of
            Just failure ->
                p [ class "notice error", attribute "role" "alert" ]
                    [ text
                        (ApiError.toMessage failure
                            ++ " Showing the answer as of "
                            ++ Format.dateTime zone page.asked
                            ++ "."
                        )
                    ]

            Nothing ->
                text ""
        , Charts.timeline
            { title = "Torrents per " ++ Buckets.label page.plot.grid
            , description = "Line chart: torrents new and updated per " ++ Buckets.label page.plot.grid ++ ", by source."
            , zone = zone
            , time = .start
            , series =
                List.map
                    (\line ->
                        { label = line.label
                        , value = \slot -> Dict.get line.key slot.counts |> Maybe.withDefault 0
                        , ink = line.ink
                        }
                    )
                    page.plot.lines
            }
            page.plot.slots
        , case params.controls.resolution.every of
            Just asked ->
                if page.plot.grid.every > asked then
                    p [ class "stats-note" ]
                        [ text
                            ("Drawn per "
                                ++ Buckets.label page.plot.grid
                                ++ ", not per "
                                ++ Buckets.label { unit = page.plot.grid.unit, every = asked }
                                ++ ": that many buckets are more than the chart can draw."
                            )
                        ]

                else
                    text ""

            Nothing ->
                text ""
        , p [ class "stats-note" ] [ text ("As of " ++ Format.dateTime zone page.asked) ]
        , p [ class "stats-note" ]
            [ text "Counted by when a source last updated a torrent: as new if that was within an hour of the source first reporting it, as updated if later." ]
        ]
