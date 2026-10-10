module TorrentStats exposing
    ( Bucket
    , Line
    , Messages
    , Plot
    , Shown
    , Source
    , State
    , Statistics
    , empty
    , failed
    , fetch
    , loaded
    , plot
    , query
    , refreshing
    , timerDue
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
import Dict
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


{-| How long a look is waited for before it is given up on, so that a request that is never
answered does not hold the page's refresh back for good. On a real instance the default look
took about five seconds, and nine days of minutes more than thirty, so this is a good deal
longer than either.
-}
requestTimeout : Float
requestTimeout =
    120 * 1000


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
                    |> Graphql.Http.withTimeout requestTimeout
                    |> Graphql.Http.toTask
            )
        |> Task.attempt toMsg


{-| bitmagnet buckets by the unit the grid comes to, which is the largest whole one the
resolution makes (`Buckets.grid`): a week of minutes merged into hours is asked for as hours.
`startTime` is a bound on a row's `updated_at`, inclusive, and is where the timeframe's first
column begins, not the moment the timeframe reaches back to: counted from the middle of a
column, bitmagnet would give the first one only its share of the column.

That is as far as Magnes can know where bitmagnet's columns begin. It cuts its days, and its
hours in a zone that is not a whole number of hours from UTC, in its database's time zone, so
against a database that is not on UTC the first of those can still be short.

No `endTime` is sent: it would cut off rows newer than a browser clock that runs behind the
server's. No `sources` is all of them, and an empty list would be a filter that matches
nothing, so it is left out.

-}
query : Time.Posix -> Route.TorrentStatsParams -> SelectionSet Statistics RootQuery
query now params =
    let
        window =
            StatsControls.window now params.controls

        planned =
            Buckets.grid params.controls.resolution window []

        input =
            InputObject.buildTorrentMetricsQueryInput
                { bucketDuration = planned.bucketedBy }
                (\optionals ->
                    { optionals
                        | sources =
                            if List.isEmpty params.sources then
                                Absent

                            else
                                Present params.sources
                        , startTime =
                            case window.from of
                                Just from ->
                                    Present (Buckets.columnStart planned from)

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
the resolution came to, so a page can say what a bucket is, and `wanted` what it would have
come to had a chart been able to draw any number of columns. `others` names the sources that
were added together into one pair of lines.
-}
type alias Plot =
    { grid : Buckets.Grid
    , wanted : Buckets.Grid
    , lines : List Line
    , others : List String
    , slots : List (Buckets.Slot Int)
    }


{-| How each source is inked: a new line and an updated one, by the source's place in the
order its sources go in. A source is drawn as a pair of lines, so this is also how many are
drawn apart.
-}
inkPairs : List ( Charts.Ink, Charts.Ink )
inkPairs =
    [ ( Charts.Accent, Charts.AccentSoft )
    , ( Charts.Strong, Charts.StrongSoft )
    , ( Charts.Muted, Charts.Faint )
    ]


largestGroups : Int
largestGroups =
    List.length inkPairs


{-| The name of the pair of lines that adds up the sources there are no more inks for.
-}
otherSources : String
otherSources =
    "Other sources"


{-| One pair of lines, and the sources counted in it: one, or the rest.
-}
type alias Group =
    { name : String
    , members : List String
    }


{-| Which of a pair's two lines a count belongs to, for the key a slot counts it under.
-}
seriesKey : Int -> Bool -> Int
seriesKey group updated =
    group
        * 2
        + (if updated then
            1

           else
            0
          )


{-| The chart of an answer, under the controls it was asked with.

Each source with anything counted gets a line for its new torrents and one for its updated,
inked by its place in the order the sources go in: the order bitmagnet lists them in, or the
order they were chosen in, where some were. A source's ink is its place's, not a matter of
which others have anything counted, so it does not change as they gain and lose counts or
from one look to the next. Where there are more sources than inks, the first two are drawn
apart and the rest added together as "Other sources", so the chart still adds up to everything
bitmagnet counted. Choosing sources narrows the query, and so brings them back apart.

-}
plot : Route.TorrentStatsParams -> Statistics -> Plot
plot params statistics =
    let
        order =
            orderOf params statistics

        groups =
            groupsOf (nameOf statistics) order

        groupOf source =
            groups
                |> List.indexedMap Tuple.pair
                |> List.filter (\( _, group ) -> List.member source group.members)
                |> List.head
                |> Maybe.map Tuple.first
                |> Maybe.withDefault (List.length groups - 1)

        samples =
            List.map
                (\bucket ->
                    { series = seriesKey (groupOf bucket.source) bucket.updated
                    , at = bucket.bucket
                    , count = bucket.count
                    }
                )
                statistics.buckets

        counted =
            Dict.fromList (List.map (\bucket -> ( bucket.source, () )) statistics.buckets)

        hasCounts group =
            List.any (\member -> Dict.member member counted) group.members

        window =
            StatsControls.window statistics.asked params.controls

        resolved =
            Buckets.grid params.controls.resolution window samples
    in
    { grid = resolved
    , wanted = Buckets.unlimited params.controls.resolution window samples
    , lines =
        groups
            |> List.indexedMap Tuple.pair
            |> List.filter (Tuple.second >> hasCounts)
            |> List.concatMap (\( index, group ) -> linesOf index group)
    , others =
        if List.length order > largestGroups then
            List.drop (largestGroups - 1) order
                |> List.filter (\key -> Dict.member key counted)
                |> List.map (nameOf statistics)

        else
            []
    , slots =
        -- A timeframe in which nothing was counted has no chart: a flat line along zero
        -- says less than saying so.
        if List.isEmpty samples then
            []

        else
            Buckets.slots resolved window samples
    }


{-| The sources in the order their inks go by. Those that were chosen, in the order they
were chosen in, or else those bitmagnet lists, in its order; then any it counted that is in
neither, by key.
-}
orderOf : Route.TorrentStatsParams -> Statistics -> List String
orderOf params statistics =
    let
        base =
            if List.isEmpty params.sources then
                List.map .key statistics.sources

            else
                params.sources

        unlisted =
            Dict.keys (Dict.fromList (List.map (\bucket -> ( bucket.source, () )) statistics.buckets))
                |> List.filter (\key -> not (List.member key base))
    in
    List.foldl
        (\key seen ->
            if List.member key seen then
                seen

            else
                seen ++ [ key ]
        )
        []
        (base ++ unlisted)


{-| A source's name as bitmagnet lists it, or its key where it does not.
-}
nameOf : Statistics -> String -> String
nameOf statistics key =
    statistics.sources
        |> List.filter (\source -> source.key == key)
        |> List.head
        |> Maybe.map .name
        |> Maybe.withDefault key


{-| The pairs of lines the sources in `order` are drawn as: each its own, or the first ones
each its own and the rest, however many, one pair that adds them up.
-}
groupsOf : (String -> String) -> List String -> List Group
groupsOf nameFor order =
    let
        alone key =
            { name = nameFor key, members = [ key ] }
    in
    if List.length order <= largestGroups then
        List.map alone order

    else
        List.map alone (List.take (largestGroups - 1) order)
            ++ [ addedTogether (List.drop (largestGroups - 1) order) ]


{-| The sources there are no more inks for, drawn as one.
-}
addedTogether : List String -> Group
addedTogether members =
    { name = otherSources, members = members }


linesOf : Int -> Group -> List Line
linesOf index group =
    let
        ( new, updated ) =
            List.drop index inkPairs
                |> List.head
                |> Maybe.withDefault ( Charts.Muted, Charts.Faint )
    in
    [ { key = seriesKey index False, label = group.name ++ ": new", ink = new }
    , { key = seriesKey index True, label = group.name ++ ": updated", ink = updated }
    ]



-- STATE


{-| What is on screen: the chart, with the choices it was drawn for and when it was asked.
-}
type alias Shown =
    { params : Route.TorrentStatsParams
    , asked : Time.Posix
    , plot : Plot
    }


type Listing
    = Loading
    | Failed ApiError.Failure
    | Loaded Shown


{-| `refreshing` is set while another look is on its way over one already shown, which
stays on screen, quieter, until it arrives. `lastFailure` is a look that did not arrive over a
chart drawn for the same choices: the chart stays, with the reason, because a refresh that
fails is not a reason to take away what was there.

`sources` are those of the last answer, kept across looks that fail, so that a chosen source
goes on being named as bitmagnet names it.

-}
type alias State =
    { listing : Listing
    , refreshing : Bool
    , lastFailure : Maybe ApiError.Failure
    , sources : List Source
    }


empty : State
empty =
    { listing = Loading, refreshing = False, lastFailure = Nothing, sources = [] }


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


{-| Whether the page's timer firing is to be a look: it was asked to keep itself fresh, and
no look is on its way. A tick can already be on its way when refreshing is turned off, and
fires once more, so the timer's own word that it was due is not enough.
-}
timerDue : Route.TorrentStatsParams -> State -> Bool
timerDue params state =
    Route.refreshInterval (Route.TorrentStats params) /= Nothing && not (inFlight state)


shownOf : State -> Maybe Shown
shownOf state =
    case state.listing of
        Loaded shown ->
            Just shown

        _ ->
            Nothing


loaded : Route.TorrentStatsParams -> Statistics -> State -> State
loaded params statistics state =
    { state
        | listing = Loaded { params = params, asked = statistics.asked, plot = plot params statistics }
        , refreshing = False
        , lastFailure = Nothing
        , sources = statistics.sources
    }


{-| A look that did not come, asked for under `params`. A chart that was drawn for the same
look stays with the reason, so that a poll that fails does not take it away. One drawn for
other choices would be left under chips that are not its own, with a heading that says
something else, so it goes, and the reason is shown alone.
-}
failed : Route.TorrentStatsParams -> ApiError.Failure -> State -> State
failed params failure state =
    case state.listing of
        Loaded shown ->
            if sameLook shown.params params then
                { state | refreshing = False, lastFailure = Just failure }

            else
                { state | listing = Failed failure, refreshing = False, lastFailure = Nothing }

        _ ->
            { state | listing = Failed failure, refreshing = False }


{-| Whether two looks ask bitmagnet the same question: how often to look again is not what
to look at.
-}
sameLook : Route.TorrentStatsParams -> Route.TorrentStatsParams -> Bool
sameLook one other =
    Route.withoutRefresh (Route.TorrentStats one) == Route.withoutRefresh (Route.TorrentStats other)



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

            Loaded shown ->
                viewShown zone state shown
        ]


choices : Messages msg -> Route.TorrentStatsParams -> State -> Html msg
choices messages params state =
    let
        navigate controlsChosen =
            messages.navigate { params | controls = controlsChosen }

        -- The multiplier the chart on screen came to, in the unit now chosen: a chart that
        -- was drawn for another unit says nothing of this one.
        picked =
            shownOf state
                |> Maybe.andThen
                    (\shown ->
                        if shown.params.controls.resolution.unit == params.controls.resolution.unit then
                            Just (Buckets.widthIn params.controls.resolution.unit shown.plot.grid)

                        else
                            Nothing
                    )
    in
    div [ class "facets stats-controls" ]
        (StatsControls.rows
            { timeframes = StatsControls.boundedTimeframes
            , picked = picked
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
                        (withChosen params.sources state.sources)
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
viewShown : Time.Zone -> State -> Shown -> Html msg
viewShown zone state shown =
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
                            ++ Format.dateTime zone shown.asked
                            ++ "."
                        )
                    ]

            Nothing ->
                text ""
        , Charts.timeline
            { title = "Torrents per " ++ Buckets.label shown.plot.grid
            , description = "Line chart: torrents new and updated per " ++ Buckets.label shown.plot.grid ++ ", by source."
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
                    shown.plot.lines
            }
            shown.plot.slots
        , viewCapNote shown.plot
        , viewOthers shown.plot
        , p [ class "stats-note" ] [ text ("As of " ++ Format.dateTime zone shown.asked) ]
        , p [ class "stats-note" ]
            [ text "Counted by when a source last updated a torrent: as new if that was within an hour of the source first reporting it, as updated if later." ]
        ]


{-| Said whenever the chart was cut down to fit, however the multiplier came about, of the
chart that was drawn and not of the choices since made.
-}
viewCapNote : Plot -> Html msg
viewCapNote drawn =
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


{-| Who "Other sources" are, since a pair of lines that adds up several cannot say.
-}
viewOthers : Plot -> Html msg
viewOthers drawn =
    if List.isEmpty drawn.others then
        text ""

    else
        p [ class "stats-note" ] [ text (otherSources ++ " are " ++ String.join ", " drawn.others ++ ".") ]
