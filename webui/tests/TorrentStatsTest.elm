module TorrentStatsTest exposing (suite)

import ApiError
import Charts
import Dict
import Expect
import Graphql.Document
import Html.Attributes
import Json.Decode as Decode
import Json.Encode as Encode
import Magnes.Api.Enum.MetricsBucketDuration exposing (MetricsBucketDuration(..))
import Route
import StatsControls exposing (AutoRefresh(..), Timeframe(..))
import Test exposing (Test, describe, test)
import Test.Html.Event as Event
import Test.Html.Query as Query
import Test.Html.Selector as Selector
import Time
import TorrentStats


suite : Test
suite =
    describe "TorrentStats"
        [ describe "query"
            [ test "asks for buckets of the resolution's unit, from the start of the timeframe, and for the sources by name" <|
                \_ ->
                    serialized asked emptyParams
                        |> Expect.all
                            [ String.contains "bucketDuration: minute" >> Expect.equal True
                            , String.contains "startTime: \"2026-10-10T09:00:00.000Z\"" >> Expect.equal True
                            , String.contains "buckets { source bucket updated count }" >> Expect.equal True
                            , String.contains "listSources { sources { key name } }" >> Expect.equal True
                            ]
            , test "never sends an end time, so the newest rows are not cut off by a slow clock" <|
                \_ ->
                    serialized asked emptyParams
                        |> String.contains "endTime"
                        |> Expect.equal False
            , test "sends no sources for all of them, and the chosen ones otherwise" <|
                \_ ->
                    ( serialized asked emptyParams
                        |> String.contains "sources: ["
                    , serialized asked { emptyParams | sources = [ "dht", "rarbg" ] }
                        |> String.contains "sources: [\"dht\", \"rarbg\"]"
                    )
                        |> Expect.equal ( False, True )
            , describe "begins on a whole column"
                [ test "so the first is not a dip: from the start of the minute the timeframe reaches back to" <|
                    \_ ->
                        serialized (afterAsked 27500) emptyParams
                            |> String.contains "startTime: \"2026-10-10T09:00:00.000Z\""
                            |> Expect.equal True
                , test "from the start of the merged bucket, where a multiplier made one" <|
                    \_ ->
                        -- Six hours back from 10:07:30 is 04:07:30, in the 15 minutes that began at 04:00.
                        serialized (afterAsked 450000) (withTimeframe Hours6 emptyParams)
                            |> Expect.all
                                [ String.contains "bucketDuration: minute" >> Expect.equal True
                                , String.contains "startTime: \"2026-10-10T04:00:00.000Z\"" >> Expect.equal True
                                ]
                , test "and of the hour or the day, where bitmagnet is asked for those" <|
                    \_ ->
                        ( serialized (afterAsked 450000) (withTimeframe Weeks1 emptyParams)
                        , serialized (afterAsked 450000) (withResolution { unit = Day, every = Nothing } (withTimeframe Weeks1 emptyParams))
                        )
                            |> Expect.all
                                [ Tuple.first
                                    >> Expect.all
                                        [ String.contains "bucketDuration: hour" >> Expect.equal True
                                        , String.contains "startTime: \"2026-10-03T10:00:00.000Z\"" >> Expect.equal True
                                        ]
                                , Tuple.second
                                    >> Expect.all
                                        [ String.contains "bucketDuration: day" >> Expect.equal True
                                        , String.contains "startTime: \"2026-10-03T00:00:00.000Z\"" >> Expect.equal True
                                        ]
                                ]
                ]
            , test "asks for hours, not minutes, where the minutes would be merged into hours anyway" <|
                \_ ->
                    ( serialized asked (withTimeframe Days1 emptyParams)
                    , serialized asked (withTimeframe Weeks1 emptyParams)
                    )
                        |> Expect.all
                            [ Tuple.first >> String.contains "bucketDuration: hour" >> Expect.equal True
                            , Tuple.second >> String.contains "bucketDuration: hour" >> Expect.equal True
                            ]
            , test "buckets by the unit the multiplier comes to, and starts the timeframe on the merged bucket" <|
                \_ ->
                    serialized asked
                        (withResolution { unit = Hour, every = Just 3 } (withTimeframe Days1 emptyParams))
                        |> Expect.all
                            [ String.contains "bucketDuration: hour" >> Expect.equal True
                            , -- A day back is 10:00 on the 9th, in the three hours, counted from the epoch, that began at 09:00.
                              String.contains "startTime: \"2026-10-09T09:00:00.000Z\"" >> Expect.equal True
                            ]
            , test "reads bitmagnet's answer, and when it was asked" <|
                \_ ->
                    Decode.decodeString (Graphql.Document.decoder (TorrentStats.query asked emptyParams)) answer
                        |> Expect.equal
                            (Ok
                                { statistics
                                    | buckets =
                                        [ bucket "dht" -2 False 3
                                        , bucket "dht" -2 True 5
                                        , bucket "rarbg" -1 False 1
                                        ]
                                }
                            )
            ]
        , describe "plot"
            [ test "has nothing to draw when bitmagnet counted nothing" <|
                \_ ->
                    TorrentStats.plot emptyParams { statistics | buckets = [] }
                        |> .slots
                        |> Expect.equal []
            , test "draws a line for new torrents and one for updated, per source, named as bitmagnet names it" <|
                \_ ->
                    TorrentStats.plot emptyParams { statistics | buckets = [ bucket "dht" 0 False 3 ] }
                        |> .lines
                        |> List.map (\line -> ( line.label, line.ink ))
                        |> Expect.equal [ ( "DHT: new", Charts.Accent ), ( "DHT: updated", Charts.AccentSoft ) ]
            , test "names a source bitmagnet did not list by its key, after those it did" <|
                \_ ->
                    TorrentStats.plot emptyParams
                        { statistics | sources = [ { key = "dht", name = "DHT" } ], buckets = [ bucket "tpb" 0 True 1 ] }
                        |> .lines
                        |> List.map (\line -> ( line.label, line.ink ))
                        |> Expect.equal [ ( "tpb: new", Charts.Strong ), ( "tpb: updated", Charts.StrongSoft ) ]
            , test "counts new and updated apart, per bucket, with the empty ones between" <|
                \_ ->
                    TorrentStats.plot emptyParams
                        { statistics
                            | buckets =
                                [ bucket "dht" -58 False 3
                                , bucket "dht" -58 True 5
                                , bucket "dht" -55 False 1
                                ]
                        }
                        |> .slots
                        |> List.map (\slot -> ( minutesBefore slot.start, Dict.toList slot.counts ))
                        |> List.filter (\( _, counts ) -> not (List.isEmpty counts))
                        |> Expect.equal
                            [ ( 58, [ ( 0, 3 ), ( 1, 5 ) ] )
                            , ( 55, [ ( 0, 1 ) ] )
                            ]
            , test "covers the whole timeframe, not only the stretch with data in it" <|
                \_ ->
                    TorrentStats.plot emptyParams { statistics | buckets = [ bucket "dht" -30 False 1 ] }
                        |> .slots
                        |> List.map (\slot -> minutesBefore slot.start)
                        |> Expect.equal (List.reverse (List.range 0 60))
            , test "merges buckets into the multiplier chosen, and says what the grid came to" <|
                \_ ->
                    let
                        chosen =
                            TorrentStats.plot
                                (withResolution { unit = Minute, every = Just 15 } emptyParams)
                                { statistics | buckets = [ bucket "dht" -2 False 4, bucket "dht" -1 False 6 ] }
                    in
                    Expect.all
                        [ .grid >> Expect.equal { unit = Minute, every = 15, offset = 0 }
                        , .slots
                            >> List.filter (\slot -> not (Dict.isEmpty slot.counts))
                            >> List.map (\slot -> Dict.toList slot.counts)
                            >> Expect.equal [ [ ( 0, 10 ) ] ]
                        ]
                        chosen
            , test "picks the multiplier itself when none was chosen" <|
                \_ ->
                    TorrentStats.plot (withTimeframe Hours6 emptyParams)
                        { statistics | buckets = [ bucket "dht" -2 False 4 ] }
                        |> .grid
                        |> Expect.equal { unit = Minute, every = 15, offset = 0 }
            , test "says what would have been drawn, where the chart was cut down to fit" <|
                \_ ->
                    let
                        aWeekOfMinutes =
                            withResolution { unit = Minute, every = Just 1 } (withTimeframe Weeks1 emptyParams)

                        plotted =
                            TorrentStats.plot aWeekOfMinutes { statistics | buckets = [ bucket "dht" -2 False 4 ] }
                    in
                    ( plotted.grid.every, plotted.wanted.every )
                        |> Expect.equal ( 6, 1 )
            , test "puts days where bitmagnet put them when its zone is not UTC" <|
                \_ ->
                    -- Athens: a day begins at 21:00 UTC. Nothing counted is lost to a day that began elsewhere.
                    let
                        plotted =
                            TorrentStats.plot (withResolution { unit = Day, every = Just 1 } (withTimeframe Weeks1 emptyParams))
                                { statistics
                                    | buckets =
                                        [ bucket "dht" -2220 False 5
                                        , bucket "dht" -780 False 7
                                        , bucket "dht" -780 True 11
                                        ]
                                }
                    in
                    ( plotted.grid.offset
                    , plotted.slots |> List.concatMap (.counts >> Dict.values) |> List.sum
                    , plotted.slots
                        |> List.filter (\slot -> not (Dict.isEmpty slot.counts))
                        |> List.map (\slot -> minutesBefore slot.start)
                    )
                        |> Expect.equal ( 21 * 3600 * 1000, 23, [ 2220, 780 ] )
            , describe "with several sources"
                [ test "gives each its own pair of lines, by its place in the order bitmagnet lists them, while there are three or fewer" <|
                    \_ ->
                        TorrentStats.plot emptyParams
                            { statistics
                                | buckets =
                                    [ bucket "rarbg" 0 False 1
                                    , bucket "dht" 0 False 1
                                    , bucket "magnetico" 0 True 1
                                    ]
                            }
                            |> .lines
                            |> List.map (\line -> ( line.label, line.ink ))
                            |> Expect.equal
                                [ ( "DHT: new", Charts.Accent )
                                , ( "DHT: updated", Charts.AccentSoft )
                                , ( "magnetico: new", Charts.Strong )
                                , ( "magnetico: updated", Charts.StrongSoft )
                                , ( "RARBG: new", Charts.Muted )
                                , ( "RARBG: updated", Charts.Faint )
                                ]
                , test "leaves out a source that nothing was counted for, without moving the inks of the rest" <|
                    \_ ->
                        TorrentStats.plot emptyParams { statistics | buckets = [ bucket "rarbg" 0 False 1 ] }
                            |> .lines
                            |> List.map (\line -> ( line.label, line.ink ))
                            |> Expect.equal [ ( "RARBG: new", Charts.Muted ), ( "RARBG: updated", Charts.Faint ) ]
                , test "keeps a source's ink as the others gain and lose their counts" <|
                    \_ ->
                        let
                            inkOf label statisticsNow =
                                TorrentStats.plot emptyParams statisticsNow
                                    |> .lines
                                    |> List.filter (\line -> line.label == label)
                                    |> List.map .ink
                        in
                        [ inkOf "RARBG: new" { statistics | buckets = [ bucket "rarbg" 0 False 1 ] }
                        , inkOf "RARBG: new" { statistics | buckets = [ bucket "rarbg" 0 False 1, bucket "dht" 0 False 1 ] }
                        , inkOf "RARBG: new" { statistics | buckets = [ bucket "rarbg" 0 False 1, bucket "dht" 0 False 1, bucket "magnetico" 0 False 1 ] }
                        ]
                            |> Expect.equal (List.repeat 3 [ Charts.Muted ])
                , test "takes a chosen order over the one bitmagnet lists them in, where sources were chosen" <|
                    \_ ->
                        TorrentStats.plot { emptyParams | sources = [ "rarbg", "dht" ] }
                            { statistics | buckets = [ bucket "dht" 0 False 1, bucket "rarbg" 0 False 1 ] }
                            |> .lines
                            |> List.map (\line -> ( line.label, line.ink ))
                            |> Expect.equal
                                [ ( "RARBG: new", Charts.Accent )
                                , ( "RARBG: updated", Charts.AccentSoft )
                                , ( "DHT: new", Charts.Strong )
                                , ( "DHT: updated", Charts.StrongSoft )
                                ]
                , describe "of more than three"
                    [ test "draws the first two apart, in the order bitmagnet lists them, and adds the rest together, so the chart adds up" <|
                        \_ ->
                            let
                                plotted =
                                    TorrentStats.plot emptyParams (manySources [ bucket "a" 0 False 10, bucket "c" 0 False 2, bucket "c" -1 True 5, bucket "e" 0 False 3, bucket "e" 0 True 4 ])
                            in
                            Expect.all
                                [ .lines
                                    >> List.map (\line -> ( line.label, line.ink ))
                                    >> Expect.equal
                                        [ ( "Alpha: new", Charts.Accent )
                                        , ( "Alpha: updated", Charts.AccentSoft )
                                        , ( "Other sources: new", Charts.Muted )
                                        , ( "Other sources: updated", Charts.Faint )
                                        ]
                                , .slots
                                    >> List.filter (\slot -> not (Dict.isEmpty slot.counts))
                                    >> List.map (\slot -> ( minutesBefore slot.start, Dict.toList slot.counts ))
                                    >> Expect.equal
                                        [ ( 1, [ ( 5, 5 ) ] )
                                        , ( 0, [ ( 0, 10 ), ( 4, 5 ), ( 5, 4 ) ] )
                                        ]
                                ]
                                plotted
                    , test "names the sources that were added together, by name" <|
                        \_ ->
                            TorrentStats.plot emptyParams (manySources [ bucket "a" 0 False 10, bucket "e" 0 False 3, bucket "c" 0 False 2 ])
                                |> .others
                                |> Expect.equal [ "Charlie", "Echo" ]
                    , test "has none to name when each is drawn apart" <|
                        \_ ->
                            TorrentStats.plot emptyParams { statistics | buckets = [ bucket "dht" 0 False 1, bucket "rarbg" 0 False 1 ] }
                                |> .others
                                |> Expect.equal []
                    ]
                ]
            ]
        , describe "view"
            [ test "says it is loading, with the controls already there to change" <|
                \_ ->
                    viewed emptyParams TorrentStats.empty
                        |> Expect.all
                            [ Query.has [ Selector.text "Loading statistics…" ]
                            , Query.findAll [ Selector.class "chip" ] >> Query.count (Expect.atLeast 5)
                            ]
            , describe "a look that fails"
                [ test "says why, alone, when there was nothing to show" <|
                    \_ ->
                        viewed emptyParams (TorrentStats.failed emptyParams ApiError.ServiceUnavailable TorrentStats.empty)
                            |> Query.find [ Selector.attribute (Html.Attributes.attribute "role" "alert") ]
                            |> Query.has [ Selector.text (ApiError.toMessage ApiError.ServiceUnavailable) ]
                , test "keeps a chart that was drawn for the same look, and says why and as of when" <|
                    \_ ->
                        viewed emptyParams (TorrentStats.failed emptyParams ApiError.Unreachable shown)
                            |> Expect.all
                                [ Query.find [ Selector.attribute (Html.Attributes.attribute "role" "alert") ]
                                    >> Query.has [ Selector.text (ApiError.toMessage ApiError.Unreachable), Selector.text "2026-10-10 10:00" ]
                                , Query.findAll [ Selector.tag "figure" ] >> Query.count (Expect.equal 1)
                                ]
                , test "keeps a chart that was drawn for the same look but for how often to look again" <|
                    \_ ->
                        let
                            quicker =
                                withControls emptyParams (\c -> { c | refresh = Every10Seconds })
                        in
                        viewed quicker (TorrentStats.failed quicker ApiError.Unreachable shown)
                            |> Query.findAll [ Selector.tag "figure" ]
                            |> Query.count (Expect.equal 1)
                , test "shows why alone when the chart was drawn for other choices, so it is not left under chips that are not its own" <|
                    \_ ->
                        let
                            aWeek =
                                withTimeframe Weeks1 emptyParams
                        in
                        viewed aWeek (TorrentStats.failed aWeek ApiError.Unreachable shown)
                            |> Expect.all
                                [ Query.find [ Selector.attribute (Html.Attributes.attribute "role" "alert") ]
                                    >> Query.has [ Selector.text (ApiError.toMessage ApiError.Unreachable) ]
                                , Query.findAll [ Selector.tag "figure" ] >> Query.count (Expect.equal 0)
                                , Query.findAll [ Selector.class "chip", Selector.containing [ Selector.text "1 week" ] ] >> Query.count (Expect.equal 1)
                                ]
                , test "shows why alone when a source was chosen the chart was not drawn for" <|
                    \_ ->
                        let
                            justDht =
                                { emptyParams | sources = [ "dht" ] }
                        in
                        viewed justDht (TorrentStats.failed justDht ApiError.Unreachable shown)
                            |> Query.findAll [ Selector.tag "figure" ]
                            |> Query.count (Expect.equal 0)
                , test "goes on naming the sources as bitmagnet named them" <|
                    \_ ->
                        let
                            justDht =
                                { emptyParams | sources = [ "dht" ] }
                        in
                        viewed justDht (TorrentStats.failed justDht ApiError.Unreachable shown)
                            |> Query.find [ Selector.class "chip", Selector.attribute (Html.Attributes.attribute "aria-pressed" "true"), Selector.containing [ Selector.text "DHT" ] ]
                            |> Query.has [ Selector.text "DHT" ]
                ]
            , test "draws the timeline as a figure headed by what a bucket is, with a legend of the lines" <|
                \_ ->
                    viewed emptyParams shown
                        |> Expect.all
                            [ Query.find [ Selector.tag "figcaption" ] >> Query.has [ Selector.text "Torrents per minute" ]
                            , Query.findAll [ Selector.class "chart-legend", Selector.tag "ul" ] >> Query.count (Expect.equal 1)
                            , Query.find [ Selector.class "chart-legend" ]
                                >> Query.findAll [ Selector.tag "li" ]
                                >> Expect.all
                                    [ Query.count (Expect.equal 4)
                                    , Query.index 0 >> Query.has [ Selector.text "DHT: new" ]
                                    , Query.index 3 >> Query.has [ Selector.text "RARBG: updated" ]
                                    ]
                            ]
            , test "names the bucket the resolution came to, in the largest whole unit" <|
                \_ ->
                    let
                        threeHours =
                            withResolution { unit = Hour, every = Just 3 } emptyParams

                        aWeek =
                            withTimeframe Weeks1 emptyParams
                    in
                    ( viewed threeHours (loadedWith threeHours)
                        |> Query.find [ Selector.tag "figcaption" ]
                        |> Query.has [ Selector.text "Torrents per 3 hours" ]
                    , viewed aWeek (loadedWith aWeek)
                        |> Query.find [ Selector.tag "figcaption" ]
                        |> Query.has [ Selector.text "Torrents per hour" ]
                    )
                        |> (\( three, hourly ) -> Expect.all [ always three, always hourly ] ())
            , describe "the cap on columns"
                [ test "says so when the multiplier asked for would draw more than a chart can" <|
                    \_ ->
                        let
                            aWeekOfMinutes =
                                withResolution { unit = Minute, every = Just 1 } (withTimeframe Weeks1 emptyParams)
                        in
                        ( viewed aWeekOfMinutes (loadedWith aWeekOfMinutes)
                            |> Query.has [ Selector.text "Drawn per 6 minutes, not per minute: that many buckets are more than the chart can draw." ]
                        , viewed emptyParams shown
                            |> Query.hasNot [ Selector.text "that many buckets" ]
                        )
                            |> (\( raised, left ) -> Expect.all [ always raised, always left ] ())
                , test "is said of the chart that was drawn, not of the choices now in the address bar" <|
                    \_ ->
                        let
                            aWeekOfMinutes =
                                withResolution { unit = Minute, every = Just 1 } (withTimeframe Weeks1 emptyParams)
                        in
                        -- Asked again for the last hour, the old chart is still on screen, dimmed.
                        viewed emptyParams (TorrentStats.refreshing (loadedWith aWeekOfMinutes))
                            |> Query.has [ Selector.text "Drawn per 6 minutes, not per minute" ]
                ]
            , test "says when it was asked, and how to read the counts" <|
                \_ ->
                    viewed emptyParams shown
                        |> Expect.all
                            [ Query.has [ Selector.text "As of 2026-10-10 10:00" ]
                            , Query.has [ Selector.text "within an hour of the source first reporting it" ]
                            ]
            , test "names the sources that were added together" <|
                \_ ->
                    viewed emptyParams (TorrentStats.loaded emptyParams (manySources [ bucket "a" 0 False 10, bucket "c" 0 False 2, bucket "e" 0 False 3 ]) TorrentStats.empty)
                        |> Query.has [ Selector.text "Other sources are Charlie, Echo." ]
            , test "has no such sentence where each source is drawn apart" <|
                \_ ->
                    viewed emptyParams shown
                        |> Query.hasNot [ Selector.text "Other sources are" ]
            , test "with nothing counted, says so rather than drawing a chart" <|
                \_ ->
                    viewed emptyParams (TorrentStats.loaded emptyParams { statistics | buckets = [] } TorrentStats.empty)
                        |> Expect.all
                            [ Query.has [ Selector.text "Nothing to show." ]
                            , Query.findAll [ Selector.tag "svg", Selector.attribute (Html.Attributes.attribute "role" "img") ] >> Query.count (Expect.equal 0)
                            ]
            , test "keeps the old chart, dimmed and busy, while another look is on its way" <|
                \_ ->
                    viewed emptyParams (TorrentStats.refreshing shown)
                        |> Query.find [ Selector.class "stats-refreshing" ]
                        |> Query.has [ Selector.attribute (Html.Attributes.attribute "aria-busy" "true"), Selector.tag "figure" ]
            , describe "controls"
                [ test "show the choice in force as pressed" <|
                    \_ ->
                        viewed chosenParams shown
                            |> Query.findAll [ Selector.attribute (Html.Attributes.attribute "aria-pressed" "true") ]
                            |> Expect.all
                                [ Query.count (Expect.equal 4)
                                , Query.index 0 >> Query.has [ Selector.text "6 hours" ]
                                , Query.index 1 >> Query.has [ Selector.text "hours" ]
                                , Query.index 2 >> Query.has [ Selector.text "30 seconds" ]
                                , Query.index 3 >> Query.has [ Selector.text "DHT" ]
                                ]
                , test "choosing a timeframe asks for it and keeps the rest" <|
                    \_ ->
                        viewed chosenParams shown
                            |> Query.find [ Selector.class "chip", Selector.containing [ Selector.text "1 day" ] ]
                            |> Event.simulate Event.click
                            |> Event.expect (Just (withControls chosenParams (\c -> { c | timeframe = Days1 })))
                , test "does not offer a timeframe of everything" <|
                    \_ ->
                        viewed emptyParams shown
                            |> Query.findAll [ Selector.class "chip", Selector.containing [ Selector.text "all time" ] ]
                            |> Query.count (Expect.equal 0)
                , test "choosing a unit starts the multiplier over, to be picked again" <|
                    \_ ->
                        viewed chosenParams shown
                            |> Query.find [ Selector.class "chip", Selector.containing [ Selector.text "days" ] ]
                            |> Event.simulate Event.click
                            |> Event.expect (Just (withControls chosenParams (\c -> { c | resolution = { unit = Day, every = Nothing } })))
                , test "typing a multiplier asks for it, and clearing it hands the choice back" <|
                    \_ ->
                        ( viewed chosenParams shown
                            |> Query.find [ Selector.tag "input" ]
                            |> Event.simulate (changeTo "15")
                            |> Event.expect (Just (withControls chosenParams (\c -> { c | resolution = { unit = Hour, every = Just 15 } })))
                        , viewed chosenParams shown
                            |> Query.find [ Selector.tag "input" ]
                            |> Event.simulate (changeTo "")
                            |> Event.expect (Just (withControls chosenParams (\c -> { c | resolution = { unit = Hour, every = Nothing } })))
                        )
                            |> (\( typed, cleared ) -> Expect.all [ always typed, always cleared ] ())
                , test "shows the multiplier in force, and where none was chosen the one the chart came to, in the unit chosen" <|
                    \_ ->
                        let
                            aWeek =
                                withTimeframe Weeks1 emptyParams
                        in
                        Expect.all
                            [ \_ ->
                                viewed chosenParams shown
                                    |> Query.find [ Selector.tag "input" ]
                                    |> Query.has [ Selector.attribute (Html.Attributes.value "2") ]
                            , \_ ->
                                viewed emptyParams shown
                                    |> Query.find [ Selector.tag "input" ]
                                    |> Query.has [ Selector.attribute (Html.Attributes.value ""), Selector.attribute (Html.Attributes.placeholder "1") ]
                            , -- A week of minutes is drawn in hours, which is 60 of the minutes chosen.
                              \_ ->
                                viewed aWeek (loadedWith aWeek)
                                    |> Query.find [ Selector.tag "input" ]
                                    |> Query.has [ Selector.attribute (Html.Attributes.placeholder "60") ]
                            , -- A chart drawn for another unit says nothing of this one.
                              \_ ->
                                viewed (withResolution { unit = Day, every = Nothing } emptyParams) shown
                                    |> Query.find [ Selector.tag "input" ]
                                    |> Query.has [ Selector.attribute (Html.Attributes.placeholder "auto") ]
                            ]
                            ()
                , test "choosing an auto-refresh interval asks for it" <|
                    \_ ->
                        viewed emptyParams shown
                            |> Query.find [ Selector.class "chip", Selector.containing [ Selector.text "1 minute" ] ]
                            |> Event.simulate Event.click
                            |> Event.expect (Just (withControls emptyParams (\c -> { c | refresh = EveryMinute })))
                , test "offers a look right now" <|
                    \_ ->
                        viewed emptyParams shown
                            |> Query.find [ Selector.tag "button", Selector.containing [ Selector.text "Refresh now" ] ]
                            |> Event.simulate Event.click
                            |> Event.expect Nothing
                , test "choosing a source adds it, and choosing it again takes it away" <|
                    \_ ->
                        ( viewed emptyParams shown
                            |> Query.find [ Selector.class "chip", Selector.containing [ Selector.text "RARBG" ] ]
                            |> Event.simulate Event.click
                            |> Event.expect (Just { emptyParams | sources = [ "rarbg" ] })
                        , viewed { emptyParams | sources = [ "dht", "rarbg" ] } shown
                            |> Query.find [ Selector.class "chip", Selector.containing [ Selector.text "DHT" ] ]
                            |> Event.simulate Event.click
                            |> Event.expect (Just { emptyParams | sources = [ "rarbg" ] })
                        )
                            |> (\( added, removed ) -> Expect.all [ always added, always removed ] ())
                , test "names the sources as bitmagnet does, not by key" <|
                    \_ ->
                        viewed emptyParams shown
                            |> Expect.all
                                [ Query.findAll [ Selector.class "chip", Selector.containing [ Selector.text "DHT" ] ] >> Query.count (Expect.equal 1)
                                , Query.findAll [ Selector.class "chip", Selector.containing [ Selector.text "RARBG" ] ] >> Query.count (Expect.equal 1)
                                , Query.findAll [ Selector.class "chip", Selector.containing [ Selector.text "rarbg" ] ] >> Query.count (Expect.equal 0)
                                ]
                , test "a chosen source bitmagnet does not list is still offered, so it can be unchosen" <|
                    \_ ->
                        viewed { emptyParams | sources = [ "tpb" ] } shown
                            |> Query.find [ Selector.class "chip", Selector.attribute (Html.Attributes.attribute "aria-pressed" "true"), Selector.containing [ Selector.text "tpb" ] ]
                            |> Event.simulate Event.click
                            |> Event.expect (Just emptyParams)
                ]
            ]
        ]


{-| The query as one line, so a test does not depend on how it is indented.
-}
serialized : Time.Posix -> Route.TorrentStatsParams -> String
serialized now params =
    Graphql.Document.serializeQuery (TorrentStats.query now params)
        |> String.words
        |> String.join " "


{-| 2026-10-10T10:00:00Z, when the data was asked for.
-}
asked : Time.Posix
asked =
    Time.millisToPosix 1791626400000


minutesBefore : Time.Posix -> Int
minutesBefore at =
    (Time.posixToMillis asked - Time.posixToMillis at) // 60000


{-| The moment the data was asked for, `milliseconds` on.
-}
afterAsked : Int -> Time.Posix
afterAsked milliseconds =
    Time.millisToPosix (1791626400000 + milliseconds)


emptyParams : Route.TorrentStatsParams
emptyParams =
    Route.emptyTorrentStats


withTimeframe : StatsControls.Timeframe -> Route.TorrentStatsParams -> Route.TorrentStatsParams
withTimeframe timeframe params =
    withControls params (\c -> { c | timeframe = timeframe })


withResolution : StatsControls.Resolution -> Route.TorrentStatsParams -> Route.TorrentStatsParams
withResolution resolution params =
    withControls params (\c -> { c | resolution = resolution })


{-| A bucket `minutes` before the data was asked for.
-}
bucket : String -> Int -> Bool -> Int -> TorrentStats.Bucket
bucket source minutes updated count =
    { source = source
    , bucket = Time.millisToPosix (1791626400000 + minutes * 60000)
    , updated = updated
    , count = count
    }


{-| Five sources, so that more than three can be drawn.
-}
manySources : List TorrentStats.Bucket -> TorrentStats.Statistics
manySources buckets =
    { asked = asked
    , buckets = buckets
    , sources =
        [ { key = "a", name = "Alpha" }
        , { key = "b", name = "Bravo" }
        , { key = "c", name = "Charlie" }
        , { key = "d", name = "Delta" }
        , { key = "e", name = "Echo" }
        ]
    }


statistics : TorrentStats.Statistics
statistics =
    { asked = asked
    , buckets = []
    , sources = [ { key = "dht", name = "DHT" }, { key = "magnetico", name = "magnetico" }, { key = "rarbg", name = "RARBG" } ]
    }


{-| Shaped as bitmagnet answers: a bucket per source, hour and whether it was an update, and
the sources by key.
-}
answer : String
answer =
    """
    {"data": {"torrent": {
      "metrics": {"buckets": [
        {"source": "dht", "bucket": "2026-10-10T09:58:00Z", "updated": false, "count": 3},
        {"source": "dht", "bucket": "2026-10-10T09:58:00Z", "updated": true, "count": 5},
        {"source": "rarbg", "bucket": "2026-10-10T09:59:00Z", "updated": false, "count": 1}
      ]},
      "listSources": {"sources": [
        {"key": "dht", "name": "DHT"},
        {"key": "magnetico", "name": "magnetico"},
        {"key": "rarbg", "name": "RARBG"}
      ]}
    }}}
    """


{-| What a view's event asks for: the page it navigates to, or `Nothing` for a plain
refresh, which asks for no other page.
-}
messages : TorrentStats.Messages (Maybe Route.TorrentStatsParams)
messages =
    { navigate = Just, refreshRequested = Nothing }


viewed : Route.TorrentStatsParams -> TorrentStats.State -> Query.Single (Maybe Route.TorrentStatsParams)
viewed params state =
    TorrentStats.view Time.utc messages params state
        |> Query.fromHtml


chosenParams : Route.TorrentStatsParams
chosenParams =
    { controls =
        { timeframe = Hours6
        , resolution = { unit = Hour, every = Just 2 }
        , refresh = Every30Seconds
        }
    , sources = [ "dht" ]
    }


withControls : Route.TorrentStatsParams -> (StatsControls.Controls -> StatsControls.Controls) -> Route.TorrentStatsParams
withControls params change =
    { params | controls = change params.controls }


loadedWith : Route.TorrentStatsParams -> TorrentStats.State
loadedWith params =
    TorrentStats.loaded params counted TorrentStats.empty


{-| Counted for DHT and RARBG, both new and updated.
-}
counted : TorrentStats.Statistics
counted =
    { statistics
        | buckets =
            [ bucket "dht" -2 False 3
            , bucket "dht" -2 True 5
            , bucket "rarbg" -1 False 1
            , bucket "rarbg" -1 True 2
            ]
    }


shown : TorrentStats.State
shown =
    loadedWith emptyParams


changeTo : String -> ( String, Decode.Value )
changeTo value =
    Event.custom "change" (Encode.object [ ( "target", Encode.object [ ( "value", Encode.string value ) ] ) ])
