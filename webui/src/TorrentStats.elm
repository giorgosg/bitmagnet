module TorrentStats exposing
    ( Answer
    , Bucket
    , Line
    , Messages
    , Plot
    , Source
    , State
    , Statistics
    , failed
    , fetch
    , loaded
    , plot
    , query
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
refresh are `StatsControls`', shared with the queue's statistics, the buckets are cut up by
`Buckets`, and how a look goes is `StatsLook`'s. This module is what is particular to torrents:
the query, the sources, and the lines they make.

-}

import ApiError
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
import Html.Attributes exposing (class)
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
import StatsLook
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


fetch : String -> Route.TorrentStatsParams -> (Result (Graphql.Http.Error Statistics) Statistics -> msg) -> Cmd msg
fetch apiUrl params =
    StatsLook.fetch apiUrl (\now -> query now params)


{-| bitmagnet is asked what `StatsControls.request` says: the unit to bucket by, and where the
first column begins. `startTime` is a bound on a row's `updated_at`, inclusive.

No `endTime` is sent: it would cut off rows newer than a browser clock that runs behind the
server's. No `sources` is all of them, and an empty list would be a filter that matches
nothing, so it is left out.

-}
query : Time.Posix -> Route.TorrentStatsParams -> SelectionSet Statistics RootQuery
query now params =
    let
        asking =
            StatsControls.request now params.controls

        input =
            InputObject.buildTorrentMetricsQueryInput
                { bucketDuration = asking.bucketDuration }
                (\optionals ->
                    { optionals
                        | sources =
                            if List.isEmpty params.sources then
                                Absent

                            else
                                Present params.sources
                        , startTime = Graphql.OptionalArgument.fromMaybe asking.startTime
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
were added together into lines of their own.
-}
type alias Plot =
    { grid : Buckets.Grid
    , wanted : Buckets.Grid
    , lines : List Line
    , others : List String
    , slots : List (Buckets.Slot Int)
    }


{-| How each source is inked: a new line and an updated one, by the source's place in the
order its sources go in.
-}
inkPairs : List ( Charts.Ink, Charts.Ink )
inkPairs =
    [ ( Charts.Accent, Charts.AccentSoft )
    , ( Charts.Strong, Charts.StrongSoft )
    , ( Charts.Muted, Charts.Faint )
    ]


{-| The number of sources the palette can draw apart: three pairs, or six single lines.
With more sources, the last place adds the remainder together.
-}
groupLimit : Route.TorrentEvent -> Int
groupLimit event =
    case event of
        Route.AllTorrentEvents ->
            List.length inkPairs

        Route.NewTorrents ->
            List.length inkPairs * 2

        Route.UpdatedTorrents ->
            List.length inkPairs * 2


{-| The name of the lines that add up the sources there are no more inks for.
-}
otherSources : String
otherSources =
    "Other sources"


{-| One source group, and the sources counted in it: one, or the rest.
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

Each source with anything counted for the chosen events gets one or two lines,
inked by its place in the order the sources go in: the order bitmagnet lists them in, or the
order they were chosen in, where some were. A source's ink is its place's, not a matter of
which others have anything counted, so it does not change as they gain and lose counts or
from one look to the next. With both events, three sources can be drawn apart; with one,
six can. Where there are more, one fewer are drawn apart and the rest added together as
"Other sources", so the chart still adds up to everything bitmagnet counted for the chosen
events. Choosing sources narrows the query, and so brings them back apart.

-}
plot : Route.TorrentStatsParams -> Statistics -> Plot
plot params statistics =
    let
        order =
            orderOf params statistics

        limit =
            groupLimit params.event

        groups =
            groupsOf limit (nameOf statistics) order

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
                selectedBuckets

        selectedBuckets =
            List.filter (\bucket -> includesEvent params.event bucket.updated) statistics.buckets

        counted =
            Dict.fromList (List.map (\bucket -> ( bucket.source, () )) selectedBuckets)

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
            |> List.concatMap (\( index, group ) -> linesOf params.event index group)
    , others =
        if List.length order > limit then
            List.drop (limit - 1) order
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


{-| The words the chart uses for the events chosen.
-}
eventLabel : Route.TorrentEvent -> String
eventLabel event =
    case event of
        Route.AllTorrentEvents ->
            "new and updated"

        Route.NewTorrents ->
            "new"

        Route.UpdatedTorrents ->
            "updated"


{-| Whether a bucket or line belongs to the events now chosen.
-}
includesEvent : Route.TorrentEvent -> Bool -> Bool
includesEvent event updated =
    case event of
        Route.AllTorrentEvents ->
            True

        Route.NewTorrents ->
            not updated

        Route.UpdatedTorrents ->
            updated


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


{-| The groups the sources in `order` are drawn as: each its own, or the first ones
each its own and the rest, however many, one group that adds them up.
-}
groupsOf : Int -> (String -> String) -> List String -> List Group
groupsOf limit nameFor order =
    let
        alone key =
            { name = nameFor key, members = [ key ] }
    in
    if List.length order <= limit then
        List.map alone order

    else
        List.map alone (List.take (limit - 1) order)
            ++ [ addedTogether (List.drop (limit - 1) order) ]


{-| The sources there are no more inks for, drawn as one.
-}
addedTogether : List String -> Group
addedTogether members =
    { name = otherSources, members = members }


{-| The first three sources keep their existing event ink. With one event selected,
the other event's three inks draw the next three sources apart.
-}
linesOf : Route.TorrentEvent -> Int -> Group -> List Line
linesOf event index group =
    let
        newInks =
            List.map Tuple.first inkPairs

        updatedInks =
            List.map Tuple.second inkPairs

        line updated inks =
            { key = seriesKey index updated
            , label =
                group.name
                    ++ (if updated then
                            ": updated"

                        else
                            ": new"
                       )
            , ink = List.drop index inks |> List.head |> Maybe.withDefault Charts.Faint
            }
    in
    case event of
        Route.AllTorrentEvents ->
            [ line False newInks, line True updatedInks ]

        Route.NewTorrents ->
            [ line False (newInks ++ updatedInks) ]

        Route.UpdatedTorrents ->
            [ line True (updatedInks ++ newInks) ]



-- STATE


{-| Keep both events so the selected event can be drawn without another request. The
sources also stay named across looks that fail (`StatsLook.State`'s `latest`).
-}
type alias Answer =
    Statistics


type alias State =
    StatsLook.State Route.TorrentStatsParams Answer


loaded : Route.TorrentStatsParams -> Statistics -> State -> State
loaded params statistics =
    StatsLook.loaded params statistics


failed : Route.TorrentStatsParams -> ApiError.Failure -> State -> State
failed =
    StatsLook.failed Route.TorrentStats



-- VIEW


type alias Messages msg =
    { navigate : Route.TorrentStatsParams -> msg
    , refreshRequested : msg
    }


view : Time.Zone -> Messages msg -> Route.TorrentStatsParams -> State -> Html msg
view zone messages params state =
    let
        -- Keep the question the answer was asked for while a new one is on its way,
        -- but draw whichever event is now chosen from that answer's two events.
        drawn =
            StatsLook.shownOf state
                |> Maybe.map
                    (\shown ->
                        let
                            answeredParams =
                                shown.params
                        in
                        ( shown, plot { answeredParams | event = params.event } shown.answer )
                    )
    in
    div [ class "page torrent-stats" ]
        [ h1 [] [ text "Torrent statistics" ]
        , choices messages params state drawn
        , StatsLook.view zone
            .asked
            (\_ -> drawn |> Maybe.map (\( shown, plotted ) -> viewChart zone params.event shown.answer plotted) |> Maybe.withDefault [])
            state
        ]


choices : Messages msg -> Route.TorrentStatsParams -> State -> Maybe ( StatsLook.Shown Route.TorrentStatsParams Answer, Plot ) -> Html msg
choices messages params state drawn =
    let
        navigate controlsChosen =
            messages.navigate { params | controls = controlsChosen }

        -- The multiplier the chart on screen came to, in the unit now chosen: a chart that
        -- was drawn for another unit says nothing of this one.
        picked =
            drawn
                |> Maybe.andThen
                    (\( shown, plotted ) ->
                        if shown.params.controls.resolution.unit == params.controls.resolution.unit then
                            Just (Buckets.widthIn params.controls.resolution.unit plotted.grid)

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
                                , onToggle = messages.navigate { params | sources = Chip.toggle source.key params.sources }
                                }
                        )
                        (withChosen params.sources (state.latest |> Maybe.map .sources |> Maybe.withDefault []))
                    )
               , Chip.facet "event"
                    (List.map
                        (\( label, event ) ->
                            Chip.view
                                { label = label
                                , count = Nothing
                                , selected = params.event == event
                                , onToggle = messages.navigate { params | event = event }
                                }
                        )
                        [ ( "both", Route.AllTorrentEvents ), ( "new", Route.NewTorrents ), ( "updated", Route.UpdatedTorrents ) ]
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


{-| The chart, and how to read what it counts.
-}
viewChart : Time.Zone -> Route.TorrentEvent -> Answer -> Plot -> List (Html msg)
viewChart zone event drawn plotted =
    [ Charts.timeline
        { title = "Torrents per " ++ Buckets.label plotted.grid
        , description = "Line chart: torrents " ++ eventLabel event ++ " per " ++ Buckets.label plotted.grid ++ ", by source."
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
                plotted.lines
        }
        plotted.slots
    , StatsControls.capNote plotted
    , viewOthers plotted
    , p [ class "stats-note" ] [ text ("As of " ++ Format.dateTime zone drawn.asked) ]
    , p [ class "stats-note" ]
        [ text "Counted by when a source last updated a torrent: as new if that was within an hour of the source first reporting it, as updated if later." ]
    ]


{-| Who "Other sources" are, since a line that adds up several cannot say.
-}
viewOthers : Plot -> Html msg
viewOthers drawn =
    if List.isEmpty drawn.others then
        text ""

    else
        p [ class "stats-note" ] [ text (otherSources ++ " are " ++ String.join ", " drawn.others ++ ".") ]
