module ChartsTest exposing (suite)

import Charts
import Expect exposing (Expectation)
import Html.Attributes
import Svg.Attributes
import Test exposing (Test, describe, test)
import Test.Html.Query as Query
import Test.Html.Selector as Selector
import Time


type alias Bucket =
    { at : Time.Posix
    , created : Int
    , processed : Int
    , failed : Int
    }


{-| 2026-10-04T00:00Z.
-}
midnight : Int
midnight =
    1791072000000


minute : Int
minute =
    60000


hour : Int
hour =
    60 * minute


day : Int
day =
    24 * hour


{-| Hourly buckets from midnight on 4 October, UTC.
-}
hourly : Int -> List Bucket
hourly count =
    every { step = hour, from = midnight, count = count }


{-| `count` buckets `step` apart from `from`, with made-up counts.
-}
every : { step : Int, from : Int, count : Int } -> List Bucket
every { step, from, count } =
    List.map
        (\i ->
            { at = Time.millisToPosix (from + i * step)
            , created = 40 + modBy 7 i
            , processed = 30 + modBy 5 i
            , failed = modBy 3 i
            }
        )
        (List.range 0 (count - 1))


timeline : List Bucket -> Query.Single msg
timeline buckets =
    Charts.timeline
        { title = "Jobs per hour"
        , description = "Line chart: jobs created, processed and failed per hour."
        , zone = Time.utc
        , time = .at
        , series =
            [ { label = "Created", value = .created, ink = Charts.Muted }
            , { label = "Processed", value = .processed, ink = Charts.Strong }
            , { label = "Failed", value = .failed, ink = Charts.Accent }
            ]
        }
        buckets
        |> Query.fromHtml


type alias Total =
    { queue : String
    , pending : Int
    , retry : Int
    , failed : Int
    , processed : Int
    }


totals : List Total
totals =
    [ { queue = "process_torrent", pending = 240, retry = 60, failed = 35, processed = 1860 }
    , { queue = "process_torrent_batch", pending = 30, retry = 8, failed = 12, processed = 140 }
    ]


stackedBars : List Total -> Query.Single msg
stackedBars data =
    Charts.stackedBars
        { title = "Jobs by queue and status"
        , description = "Stacked bar chart: jobs in each queue by status."
        , categoryHeading = "Queue"
        , category = .queue
        , segments =
            [ { label = "Failed", value = .failed, ink = Charts.Accent }
            , { label = "Retry", value = .retry, ink = Charts.AccentSoft }
            , { label = "Pending", value = .pending, ink = Charts.Faint }
            , { label = "Processed", value = .processed, ink = Charts.Muted }
            ]
        }
        data
        |> Query.fromHtml


{-| The chart itself, not the legend's swatches.
-}
image : Query.Single msg -> Query.Single msg
image =
    Query.find [ Selector.tag "svg", Selector.attribute (Html.Attributes.attribute "role" "img") ]


{-| Every colour `terezka/elm-charts` 5.0.0 writes when it is not given one: the label,
grid, axis and tick greys, the white label halo, the tooltip's white and its border, and
the palette it cycles through for series. All of them are light-theme colours, so none
may reach the page (ADR 0007). Taken from the package source, not from its docs.
-}
elmChartsColours : List String
elmChartsColours =
    [ "#808BAB"
    , "#EFF2FA"
    , "rgb(200 200 200)"
    , "rgb(210, 210, 210)"
    , "rgba(210, 210, 210, 0.5)"
    , "rgba(210, 210, 210, 1)"
    , "#D8D8D8"
    , "white"
    , "#ea60df"
    , "#7b4dff"
    , "#12A5ED"
    , "#92b42c"
    , "#71c614"
    , "#FF8400"
    , "#22d2ba"
    , "#F5325B"
    , "#eabd39"
    , "#7345f6"
    , "#ea7369"
    , "#db4cb2"
    , "#871c1c"
    , "#6df0d2"
    , "#FFCA00"
    ]


paintsNothingInElmChartsColours : Query.Single msg -> Expectation
paintsNothingInElmChartsColours chart =
    chart
        |> Expect.all
            (List.concatMap
                (\colour ->
                    [ Query.findAll [ Selector.attribute (Svg.Attributes.fill colour) ]
                        >> Query.count (Expect.equal 0)
                    , Query.findAll [ Selector.attribute (Svg.Attributes.stroke colour) ]
                        >> Query.count (Expect.equal 0)
                    ]
                )
                elmChartsColours
            )


suite : Test
suite =
    describe "Charts"
        [ describe "timeline"
            [ test "is a figure headed by its title" <|
                \_ ->
                    timeline (hourly 48)
                        |> Query.find [ Selector.tag "figcaption" ]
                        |> Query.has [ Selector.tag "h2", Selector.text "Jobs per hour" ]
            , test "draws an image named by its description" <|
                \_ ->
                    timeline (hourly 48)
                        |> image
                        |> Query.has
                            [ Selector.attribute
                                (Html.Attributes.attribute "aria-label" "Line chart: jobs created, processed and failed per hour.")
                            ]
            , test "writes counts on the axis with thousands separators" <|
                \_ ->
                    hourly 48
                        |> List.map (\b -> { b | created = 12000 + b.created })
                        |> timeline
                        |> image
                        |> Expect.all
                            [ Query.has [ Selector.text "10,000" ]
                            , Query.hasNot [ Selector.text "10000" ]
                            ]
            , test "names each series in a legend, with a swatch in its ink" <|
                \_ ->
                    timeline (hourly 48)
                        |> Query.findAll [ Selector.tag "li" ]
                        |> Expect.all
                            [ Query.count (Expect.equal 3)
                            , Query.index 0 >> Query.has [ Selector.text "Created" ]
                            , Query.index 0
                                >> Query.has [ Selector.attribute (Svg.Attributes.stroke "var(--dim)") ]
                            , Query.index 1 >> Query.has [ Selector.text "Processed" ]
                            , Query.index 1
                                >> Query.has [ Selector.attribute (Svg.Attributes.stroke "var(--fg)") ]
                            , Query.index 2 >> Query.has [ Selector.text "Failed" ]
                            , Query.index 2
                                >> Query.has [ Selector.attribute (Svg.Attributes.stroke "var(--accent)") ]
                            ]
            , test "draws a soft strong ink as the strong one dashed, in the legend and the line" <|
                \_ ->
                    Charts.timeline
                        { title = "Jobs per hour"
                        , description = "Line chart."
                        , zone = Time.utc
                        , time = .at
                        , series =
                            [ { label = "Created", value = .created, ink = Charts.Strong }
                            , { label = "Processed", value = .processed, ink = Charts.StrongSoft }
                            ]
                        }
                        (hourly 48)
                        |> Query.fromHtml
                        |> Expect.all
                            [ Query.findAll [ Selector.tag "li" ]
                                >> Expect.all
                                    [ Query.index 0 >> Query.has [ Selector.attribute (Svg.Attributes.strokeDasharray "") ]
                                    , Query.index 1
                                        >> Query.has
                                            [ Selector.attribute (Svg.Attributes.stroke "var(--fg)")
                                            , Selector.attribute (Svg.Attributes.strokeDasharray "8 3")
                                            ]
                                    ]
                            , image
                                >> Query.findAll
                                    [ Selector.tag "path"
                                    , Selector.attribute (Svg.Attributes.stroke "var(--fg)")
                                    , Selector.attribute (Svg.Attributes.strokeDasharray "8 3")
                                    ]
                                >> Query.count (Expect.equal 1)
                            ]
            , test "opens its box at the latest end, which is the end a timeline is read for, and a bar chart's does not" <|
                \_ ->
                    ( timeline (hourly 48)
                        |> Query.findAll [ Selector.class "chart-plot", Selector.class "chart-timeline" ]
                        |> Query.count (Expect.equal 1)
                    , stackedBars totals
                        |> Query.findAll [ Selector.class "chart-timeline" ]
                        |> Query.count (Expect.equal 0)
                    )
                        |> (\( timelineBox, barBox ) -> Expect.all [ always timelineBox, always barBox ] ())
            , test "with a single bucket, marks it with a dot in each series' ink" <|
                \_ ->
                    -- A line through one point draws nothing.
                    timeline (hourly 1)
                        |> image
                        |> Expect.all
                            [ Query.findAll [ Selector.tag "circle", Selector.attribute (Svg.Attributes.fill "var(--accent)") ]
                                >> Query.count (Expect.equal 1)
                            , Query.findAll [ Selector.tag "circle", Selector.attribute (Svg.Attributes.fill "var(--dim)") ]
                                >> Query.count (Expect.equal 1)
                            , Query.findAll [ Selector.tag "circle", Selector.attribute (Svg.Attributes.fill "var(--fg)") ]
                                >> Query.count (Expect.equal 1)
                            ]
            , test "with a single bucket, still paints nothing in elm-charts' own colours" <|
                \_ ->
                    timeline (hourly 1)
                        |> paintsNothingInElmChartsColours
            , test "with no buckets, says so rather than drawing an empty frame" <|
                \_ ->
                    -- elm-charts would draw one anyway, dated 1 January 1970.
                    timeline []
                        |> Expect.all
                            [ Query.has [ Selector.text "Jobs per hour" ]
                            , Query.has [ Selector.text "Nothing to show." ]
                            , Query.findAll [ Selector.tag "svg" ] >> Query.count (Expect.equal 0)
                            , Query.findAll [ Selector.tag "table" ] >> Query.count (Expect.equal 0)
                            ]
            , describe "the numbers, for anyone who cannot see the chart"
                [ test "are in a table, in a visually hidden box" <|
                    \_ ->
                        -- The box, not the table: a table sizes to its content whatever
                        -- width it is given, so hiding it directly still widens the page.
                        timeline (hourly 48)
                            |> Query.find [ Selector.class "visually-hidden" ]
                            |> Query.findAll [ Selector.tag "table" ]
                            |> Query.count (Expect.equal 1)
                , test "head a column per series after the time" <|
                    \_ ->
                        timeline (hourly 48)
                            |> Query.find [ Selector.tag "thead" ]
                            |> Query.findAll [ Selector.tag "th" ]
                            |> Expect.all
                                [ Query.count (Expect.equal 4)
                                , Query.index 0 >> Query.has [ Selector.text "Time" ]
                                , Query.index 1 >> Query.has [ Selector.text "Created" ]
                                , Query.index 3 >> Query.has [ Selector.text "Failed" ]
                                ]
                , test "give a row per bucket, with its time and counts" <|
                    \_ ->
                        [ { at = Time.millisToPosix (midnight + 14 * hour + 5 * minute)
                          , created = 12040
                          , processed = 7
                          , failed = 0
                          }
                        , { at = Time.millisToPosix (midnight + 15 * hour)
                          , created = 1
                          , processed = 2
                          , failed = 3
                          }
                        ]
                            |> timeline
                            |> Query.find [ Selector.tag "tbody" ]
                            |> Query.findAll [ Selector.tag "tr" ]
                            |> Expect.all
                                [ Query.count (Expect.equal 2)
                                , Query.index 0
                                    >> Query.children []
                                    >> Expect.all
                                        [ Query.index 0 >> Query.has [ Selector.tag "th", Selector.text "2026-10-04 14:05" ]
                                        , Query.index 1 >> Query.has [ Selector.text "12,040" ]
                                        , Query.index 2 >> Query.has [ Selector.text "7" ]
                                        , Query.index 3 >> Query.has [ Selector.text "0" ]
                                        ]
                                ]
                ]
            , describe "time axis"
                [ test "over two days, gives clock times and the date at midnight" <|
                    \_ ->
                        timeline (hourly 48)
                            |> image
                            |> Expect.all
                                [ Query.has [ Selector.text "06:00" ]
                                , Query.has [ Selector.text "5 Oct" ]
                                , Query.hasNot [ Selector.text "00:00" ]
                                , Query.hasNot [ Selector.text "10/5" ]
                                ]
                , test "with a single bucket, names its moment once" <|
                    \_ ->
                        -- elm-charts widens a zero-length span by 10 ms, and intervals
                        -- would label that span five times over as the same minute.
                        hourly 1
                            |> timeline
                            |> image
                            |> Expect.all
                                [ Query.has [ Selector.text "2026-10-04 00:00" ]
                                , Query.findAll [ Selector.tag "tspan", Selector.text "00:00" ]
                                    >> Query.count (Expect.equal 1)
                                ]
                , test "over a couple of minutes, names each bucket's minute once" <|
                    \_ ->
                        -- Too short a span for minute ticks: intervals would tick the
                        -- seconds, and every one of them reads as the same minute.
                        every { step = minute, from = midnight + 14 * hour, count = 2 }
                            |> timeline
                            |> image
                            |> Expect.all
                                [ Query.findAll [ Selector.tag "tspan", Selector.text "14:00" ]
                                    >> Query.count (Expect.equal 1)
                                , Query.findAll [ Selector.tag "tspan", Selector.text "14:01" ]
                                    >> Query.count (Expect.equal 1)
                                ]
                , test "across New Year, gives the year where it turns" <|
                    \_ ->
                        -- Three weeks of days from 22 December.
                        every { step = day, from = midnight + 79 * day, count = 21 }
                            |> timeline
                            |> image
                            |> Expect.all
                                [ Query.has [ Selector.text "2027" ]
                                , Query.hasNot [ Selector.text "2026" ]
                                ]
                , test "over an hour and a half, gives clock times to the minute" <|
                    \_ ->
                        -- From 14:00.
                        every { step = minute, from = midnight + 14 * hour, count = 90 }
                            |> timeline
                            |> image
                            |> Expect.all
                                [ Query.has [ Selector.text "14:15" ]
                                , Query.has [ Selector.text "15:00" ]
                                , Query.hasNot [ Selector.text "4 Oct" ]
                                ]
                , test "over a year, names the months and the year where it turns" <|
                    \_ ->
                        every { step = day, from = midnight, count = 365 }
                            |> timeline
                            |> image
                            |> Expect.all
                                [ Query.has [ Selector.text "2027" ]
                                , Query.hasNot [ Selector.text "2026" ]
                                , Query.hasNot [ Selector.text " Nov" ]
                                , Query.hasNot [ Selector.text " Dec" ]
                                ]
                ]
            , test "paints nothing in elm-charts' own colours" <|
                \_ ->
                    timeline (hourly 48)
                        |> paintsNothingInElmChartsColours
            ]
        , describe "stackedBars"
            [ test "is a figure headed by its title" <|
                \_ ->
                    stackedBars totals
                        |> Query.find [ Selector.tag "figcaption" ]
                        |> Query.has [ Selector.tag "h2", Selector.text "Jobs by queue and status" ]
            , test "draws an image named by its description" <|
                \_ ->
                    stackedBars totals
                        |> image
                        |> Query.has
                            [ Selector.attribute
                                (Html.Attributes.attribute "aria-label" "Stacked bar chart: jobs in each queue by status.")
                            ]
            , test "gives many categories enough horizontal room for their labels" <|
                \_ ->
                    List.repeat 12 { queue = "process_torrent_batch", pending = 1, retry = 2, failed = 3, processed = 4 }
                        |> stackedBars
                        |> Query.has [ Selector.style "min-width" "1596px", Selector.style "max-width" "1878px" ]
            , test "lets a chart of few bars shrink to a phone's width, and draws it no wider than its bars need" <|
                \_ ->
                    -- Two bars of 21-letter names, under counts up to "2,195": 379 wide, and down
                    -- to 322, inside a 375-pixel screen's column.
                    stackedBars totals
                        |> Expect.all
                            [ Query.has [ Selector.style "min-width" "322px", Selector.style "max-width" "379px" ]
                            , Query.has [ Selector.class "chart-bars" ]
                            ]
            , test "keeps a timeline from shrinking below where its labels can be read" <|
                \_ ->
                    timeline (hourly 48)
                        |> Expect.all
                            [ Query.has [ Selector.style "min-width" "704px" ]
                            , Query.hasNot [ Selector.class "chart-bars" ]
                            ]
            , test "names each bar by its category" <|
                \_ ->
                    stackedBars totals
                        |> image
                        |> Expect.all
                            [ Query.has [ Selector.text "process_torrent" ]
                            , Query.has [ Selector.text "process_torrent_batch" ]
                            ]
            , test "paints nothing in elm-charts' own colours" <|
                \_ ->
                    stackedBars totals
                        |> paintsNothingInElmChartsColours
            , test "names each status in a legend, top of the stack first, with a swatch in its ink" <|
                \_ ->
                    stackedBars totals
                        |> Query.findAll [ Selector.tag "li" ]
                        |> Expect.all
                            [ Query.count (Expect.equal 4)
                            , Query.index 0 >> Query.has [ Selector.text "Failed" ]
                            , Query.index 0
                                >> Query.has [ Selector.attribute (Svg.Attributes.fill "var(--accent)") ]
                            , Query.index 2 >> Query.has [ Selector.text "Pending" ]
                            , Query.index 2
                                >> Query.has
                                    [ Selector.attribute (Svg.Attributes.fill "var(--faint)")
                                    , Selector.attribute (Svg.Attributes.stroke "var(--edge)")
                                    ]
                            ]
            , test "draws a soft strong ink as a pale bar edged in the strong colour" <|
                \_ ->
                    Charts.stackedBars
                        { title = "Jobs by queue and status"
                        , description = "Stacked bar chart."
                        , categoryHeading = "Queue"
                        , category = .queue
                        , segments = [ { label = "Failed", value = .failed, ink = Charts.StrongSoft } ]
                        }
                        totals
                        |> Query.fromHtml
                        |> Query.find [ Selector.tag "li" ]
                        |> Query.has
                            [ Selector.attribute (Svg.Attributes.fill "var(--faint)")
                            , Selector.attribute (Svg.Attributes.stroke "var(--fg)")
                            ]
            , test "draws a hollow accent ink as a bar of the background edged in the accent, unlike the soft accent's pale fill" <|
                \_ ->
                    Charts.stackedBars
                        { title = "Jobs by queue and status"
                        , description = "Stacked bar chart."
                        , categoryHeading = "Queue"
                        , category = .queue
                        , segments = [ { label = "Retry", value = .retry, ink = Charts.AccentHollow } ]
                        }
                        totals
                        |> Query.fromHtml
                        |> Query.find [ Selector.tag "li" ]
                        |> Query.has
                            [ Selector.attribute (Svg.Attributes.fill "var(--bg)")
                            , Selector.attribute (Svg.Attributes.stroke "var(--accent)")
                            ]
            , test "gives the numbers in a visually hidden table, a row per category" <|
                \_ ->
                    stackedBars totals
                        |> Query.find [ Selector.class "visually-hidden" ]
                        |> Expect.all
                            [ Query.findAll [ Selector.tag "table" ] >> Query.count (Expect.equal 1)
                            , Query.find [ Selector.tag "thead" ]
                                >> Query.findAll [ Selector.tag "th" ]
                                >> Expect.all
                                    [ Query.count (Expect.equal 5)
                                    , Query.index 0 >> Query.has [ Selector.text "Queue" ]
                                    , Query.index 4 >> Query.has [ Selector.text "Processed" ]
                                    ]
                            , Query.find [ Selector.tag "tbody" ]
                                >> Query.findAll [ Selector.tag "tr" ]
                                >> Expect.all
                                    [ Query.count (Expect.equal 2)
                                    , Query.index 0
                                        >> Query.children []
                                        >> Expect.all
                                            [ Query.index 0 >> Query.has [ Selector.tag "th", Selector.text "process_torrent" ]
                                            , Query.index 1 >> Query.has [ Selector.text "35" ]
                                            , Query.index 4 >> Query.has [ Selector.text "1,860" ]
                                            ]
                                    ]
                            ]
            , test "with no categories, says so rather than drawing an empty frame" <|
                \_ ->
                    stackedBars []
                        |> Expect.all
                            [ Query.has [ Selector.text "Nothing to show." ]
                            , Query.findAll [ Selector.tag "svg" ] >> Query.count (Expect.equal 0)
                            ]
            ]
        ]
