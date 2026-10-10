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
            , test "starts the timeframe back from when it was asked, and buckets by the unit chosen" <|
                \_ ->
                    serialized asked
                        { emptyParams
                            | controls =
                                { timeframe = Days1
                                , resolution = { unit = Hour, every = Just 3 }
                                , refresh = Off
                                }
                        }
                        |> Expect.all
                            [ String.contains "bucketDuration: hour" >> Expect.equal True
                            , String.contains "startTime: \"2026-10-09T10:00:00.000Z\"" >> Expect.equal True
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
            , test "names a source bitmagnet did not list by its key" <|
                \_ ->
                    TorrentStats.plot emptyParams { statistics | buckets = [ bucket "tpb" 0 True 1 ] }
                        |> .lines
                        |> List.map .label
                        |> Expect.equal [ "tpb: new", "tpb: updated" ]
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
                                { emptyParams | controls = { timeframe = Hours1, resolution = { unit = Minute, every = Just 15 }, refresh = Off } }
                                { statistics | buckets = [ bucket "dht" -2 False 4, bucket "dht" -1 False 6 ] }
                    in
                    Expect.all
                        [ .grid >> Expect.equal { unit = Minute, every = 15 }
                        , .slots
                            >> List.filter (\slot -> not (Dict.isEmpty slot.counts))
                            >> List.map (\slot -> Dict.toList slot.counts)
                            >> Expect.equal [ [ ( 0, 10 ) ] ]
                        ]
                        chosen
            , test "picks the multiplier itself when none was chosen" <|
                \_ ->
                    TorrentStats.plot
                        { emptyParams | controls = { timeframe = Hours6, resolution = { unit = Minute, every = Nothing }, refresh = Off } }
                        { statistics | buckets = [ bucket "dht" -2 False 4 ] }
                        |> .grid
                        |> Expect.equal { unit = Minute, every = 15 }
            , describe "with several sources"
                [ test "gives each its own pair of lines, in the order of their keys, while there are three or fewer" <|
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
                , test "leaves out a source that nothing was counted for" <|
                    \_ ->
                        TorrentStats.plot emptyParams { statistics | buckets = [ bucket "rarbg" 0 False 1 ] }
                            |> .lines
                            |> List.map .label
                            |> Expect.equal [ "RARBG: new", "RARBG: updated" ]
                , test "draws the two busiest of more than three, and adds the rest together" <|
                    \_ ->
                        let
                            plotted =
                                TorrentStats.plot emptyParams
                                    { statistics
                                        | buckets =
                                            [ bucket "dht" 0 False 10
                                            , bucket "magnetico" 0 False 50
                                            , bucket "rarbg" 0 False 2
                                            , bucket "tpb" 0 False 3
                                            , bucket "tpb" 0 True 4
                                            , bucket "rarbg" -1 True 5
                                            ]
                                    }
                        in
                        Expect.all
                            [ .lines
                                >> List.map (\line -> ( line.label, line.ink ))
                                >> Expect.equal
                                    [ ( "DHT: new", Charts.Accent )
                                    , ( "DHT: updated", Charts.AccentSoft )
                                    , ( "magnetico: new", Charts.Strong )
                                    , ( "magnetico: updated", Charts.StrongSoft )
                                    , ( "Other sources: new", Charts.Muted )
                                    , ( "Other sources: updated", Charts.Faint )
                                    ]
                            , -- RARBG and tpb, 2 + 3 new and 4 + 5 updated, are one pair of lines
                              .slots
                                >> List.filter (\slot -> not (Dict.isEmpty slot.counts))
                                >> List.map (\slot -> ( minutesBefore slot.start, Dict.toList slot.counts ))
                                >> Expect.equal
                                    [ ( 1, [ ( 5, 5 ) ] )
                                    , ( 0, [ ( 0, 10 ), ( 2, 50 ), ( 4, 5 ), ( 5, 4 ) ] )
                                    ]
                            ]
                            plotted
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
            , test "a failed request says why" <|
                \_ ->
                    viewed emptyParams (TorrentStats.failed ApiError.ServiceUnavailable TorrentStats.empty)
                        |> Query.find [ Selector.attribute (Html.Attributes.attribute "role" "alert") ]
                        |> Query.has [ Selector.text (ApiError.toMessage ApiError.ServiceUnavailable) ]
            , test "a look that fails over a chart already shown keeps the chart and says why" <|
                \_ ->
                    viewed emptyParams (TorrentStats.failed ApiError.Unreachable shown)
                        |> Expect.all
                            [ Query.find [ Selector.attribute (Html.Attributes.attribute "role" "alert") ]
                                >> Query.has [ Selector.text (ApiError.toMessage ApiError.Unreachable), Selector.text "2026-10-10 10:00" ]
                            , Query.findAll [ Selector.tag "figure" ] >> Query.count (Expect.equal 1)
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
            , test "names the bucket the resolution came to" <|
                \_ ->
                    viewed { emptyParams | controls = { timeframe = Hours1, resolution = { unit = Hour, every = Just 3 }, refresh = Off } }
                        (loadedWith { emptyParams | controls = { timeframe = Hours1, resolution = { unit = Hour, every = Just 3 }, refresh = Off } })
                        |> Query.find [ Selector.tag "figcaption" ]
                        |> Query.has [ Selector.text "Torrents per 3 hours" ]
            , test "says so when the multiplier asked for would draw more than a chart can" <|
                \_ ->
                    let
                        aWeekOfMinutes =
                            { emptyParams | controls = { timeframe = Weeks1, resolution = { unit = Minute, every = Just 1 }, refresh = Off } }
                    in
                    ( viewed aWeekOfMinutes (loadedWith aWeekOfMinutes)
                        |> Query.has [ Selector.text "Drawn per 6 minutes, not per minute: that many buckets are more than the chart can draw." ]
                    , viewed emptyParams shown
                        |> Query.hasNot [ Selector.text "that many buckets" ]
                    )
                        |> (\( raised, left ) -> Expect.all [ always raised, always left ] ())
            , test "says when it was asked, and how to read the counts" <|
                \_ ->
                    viewed emptyParams shown
                        |> Expect.all
                            [ Query.has [ Selector.text "As of 2026-10-10 10:00" ]
                            , Query.has [ Selector.text "within an hour of the source first reporting it" ]
                            ]
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
                , test "shows the multiplier in force, and the one Magnes picked where none was chosen" <|
                    \_ ->
                        ( viewed chosenParams shown
                            |> Query.find [ Selector.tag "input" ]
                            |> Query.has [ Selector.attribute (Html.Attributes.value "2") ]
                        , viewed emptyParams shown
                            |> Query.find [ Selector.tag "input" ]
                            |> Query.has [ Selector.attribute (Html.Attributes.value ""), Selector.attribute (Html.Attributes.placeholder "1") ]
                        )
                            |> (\( chosen, picked ) -> Expect.all [ always chosen, always picked ] ())
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


emptyParams : Route.TorrentStatsParams
emptyParams =
    Route.emptyTorrentStats


{-| A bucket `minutes` before the data was asked for.
-}
bucket : String -> Int -> Bool -> Int -> TorrentStats.Bucket
bucket source minutes updated count =
    { source = source
    , bucket = Time.millisToPosix (1791626400000 + minutes * 60000)
    , updated = updated
    , count = count
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
