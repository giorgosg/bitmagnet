module Charts exposing (Ink(..), Series, StackedBars, Timeline, stackedBars, timeline)

{-| The statistics charts, drawn with `terezka/elm-charts` and already themed, as
`docs/adr/0007-draw-charts-with-elm-charts.md` decides: a time series of lines, and bars
stacked by category.

Every mark, label and gridline takes its colour here, as a `var(--token)` string through
elm-charts' documented colour attributes. elm-charts' own defaults are light-theme greys,
whites and a palette of bright hues, so nothing it draws is left on one. A caller picks an
`Ink`, never a colour, and so cannot bring one back.

Each chart is a `figure`: its title as the heading, the drawing as an image named by its
description, a legend, and the same numbers in a visually hidden table, so they can be
read without a pointer or without sight.

-}

import Chart as C
import Chart.Attributes as CA
import Chart.Svg as CS
import Format
import Html exposing (Html, div, figcaption, figure, h2, li, p, table, tbody, td, text, th, thead, tr, ul)
import Html.Attributes exposing (attribute, class, style)
import Intervals
import Svg
import Svg.Attributes as SA
import Time


{-| How a series is drawn, from Magnes's colour tokens. Each works for both a line and a
bar segment, so a status keeps its look across the two charts.

  - `Accent`: the one colour, for what wants attention. A heavier line; a solid bar.
  - `AccentSoft`: the accent held back. A dashed accent line; a pale bar edged in accent.
  - `Strong`: the text colour. A solid line; a solid bar.
  - `StrongSoft`: the text colour held back, as `AccentSoft` holds the accent back. A long-dashed
    line; a pale bar edged in the text colour. It is `Strong`'s partner, for a second series of
    the same thing.
  - `AccentHollow`: the accent as an outline only. A dotted accent line; a bar of the
    background edged in accent. For what is the accent's for now, as a job waiting to be tried
    again is a failure for now.
  - `Muted`: the secondary grey. A dashed line; a solid grey bar.
  - `Faint`: barely there. A dotted grey line; a pale bar with an edge.

-}
type Ink
    = Accent
    | AccentSoft
    | Strong
    | StrongSoft
    | AccentHollow
    | Muted
    | Faint


{-| One line, or one segment of every bar: its name in the legend and the table, how to
read its count from a datum, and how it is drawn.
-}
type alias Series data =
    { label : String
    , value : data -> Int
    , ink : Ink
    }


{-| Counts over time, one line per series. `description` names the drawing for a screen
reader. Times are read and labelled in `zone`.
-}
type alias Timeline data =
    { title : String
    , description : String
    , zone : Time.Zone
    , time : data -> Time.Posix
    , series : List (Series data)
    }


{-| A bar per category, stacked from its segments. `categoryHeading` heads the table's
first column, which `category` names.
-}
type alias StackedBars data =
    { title : String
    , description : String
    , categoryHeading : String
    , category : data -> String
    , segments : List (Series data)
    }


{-| One datum per time bucket, in time order. The time axis is labelled at the moments
`terezka/intervals` picks for the span shown: clock times within a day, the date where a
day turns, month names across months, and the year where one turns. A span of a few
minutes labels each bucket instead, and a single moment is labelled once, in full.
-}
timeline : Timeline data -> List data -> Html msg
timeline config data =
    let
        axis =
            timeAxis config.zone (List.map config.time data)
    in
    chartFigure config.title data <|
        [ plot
            { description = config.description
            , width = 920
            , classes = [ "chart-timeline" ]
            , top =
                data
                    |> List.concatMap (\datum -> List.map (\series -> series.value datum) config.series)
                    |> List.maximum
                    |> Maybe.withDefault 0
            , range = axis.range
            }
            (C.series (config.time >> Time.posixToMillis >> toFloat)
                (List.map (line axis.showPointMarkers) config.series)
                data
                :: axis.labels
            )
        , ul [ class "chart-legend" ]
            (List.map (\series -> legendEntry (lineSwatch (paint series.ink).line) series.label) config.series)
        , numbers
            { rowHeading = "Time"
            , rowLabel = config.time >> Format.dateTime config.zone
            , columns = config.series
            }
            data
        ]


{-| One datum per category, drawn left to right in list order. The first segment is the
top of each stack, and the legend and table list them in the same order.
-}
stackedBars : StackedBars data -> List data -> Html msg
stackedBars config data =
    chartFigure config.title data <|
        [ plot
            { description = config.description
            , width =
                let
                    labelWidth =
                        data
                            |> List.map (config.category >> String.length >> (\length -> 7 * length + 24))
                            |> List.maximum
                            |> Maybe.withDefault 0
                in
                max 920 (96 + List.length data * labelWidth)
            , classes = []
            , top =
                data
                    |> List.map (\datum -> List.sum (List.map (\series -> series.value datum) config.segments))
                    |> List.maximum
                    |> Maybe.withDefault 0
            , range = []
            }
            [ C.binLabels config.category (CA.moveDown 20 :: labelStyle)
            , C.bars [ CA.margin 0.3 ] [ C.stacked (List.map segment config.segments) ] data
            ]
        , ul [ class "chart-legend" ]
            (List.map (\series -> legendEntry (barSwatch (paint series.ink).bar) series.label) config.segments)
        , numbers
            { rowHeading = config.categoryHeading
            , rowLabel = config.category
            , columns = config.segments
            }
            data
        ]


{-| The drawing area, an image named by the chart's `description`, with the gridlines and
counts every chart has under the `elements` it draws. `range` overrides the x axis's,
which otherwise fits the data.

A grid is always given: without one elm-charts adds its own, in its light-theme grey.

The drawing scales to its container rather than reflowing, so it is drawn at about the
width a panel gives it, where a 12-unit label reads at about 12px. The stylesheet stops it
shrinking much below that, on a narrow screen, by letting `chart-plot` scroll instead.
That box clips whatever falls outside the drawing, so every label has to fit inside its
margins: the left one is as wide as the largest count, `top`, written out.

A timeline's box is also `chart-timeline`, which the stylesheet opens at its right-hand end,
where the latest time is. What a timeline is for is how things are now, and on a phone the
box is the narrower part of a chart that scrolls: opened at the left, it would show the oldest
stretch and the count axis and hide the end that matters. The cost is that the axis is out of
view until the box is scrolled back, and the numbers are in the table either way.

-}
plot :
    { description : String, width : Int, top : Int, range : List (CA.Attribute CS.Axis), classes : List String }
    -> List (C.Element data msg)
    -> Html msg
plot { description, width, top, range, classes } elements =
    div [ class (String.join " " ("chart-plot" :: classes)) ]
        [ C.chart
            [ CA.width (toFloat width)
            , CA.htmlAttrs
                [ style "min-width"
                    (String.fromInt
                        (if width > 920 then
                            width

                         else
                            704
                        )
                        ++ "px"
                    )
                ]
            , CA.height 260
            , CA.margin
                { top = 12
                , bottom = 28
                , left = 16 + 7 * toFloat (String.length (Format.count top))
                , right = 24
                }
            , CA.range range
            , CA.attrs [ attribute "role" "img", attribute "aria-label" description ]
            ]
            (C.grid [ CA.color "var(--faint)" ] :: countAxis :: elements)
        ]


{-| A chart under its heading. With no data there is nothing to draw: elm-charts would
draw an empty frame anyway, placed at the start of 1970.
-}
chartFigure : String -> List data -> List (Html msg) -> Html msg
chartFigure title data body =
    figure [ class "chart" ]
        (figcaption [] [ h2 [] [ text title ] ]
            :: (if List.isEmpty data then
                    [ p [ class "chart-empty" ] [ text "Nothing to show." ] ]

                else
                    body
               )
        )


{-| A series' line. A line through a single moment draws nothing, so `showPointMarkers` adds
a dot at each point.
-}
line : Bool -> Series data -> C.Property data CS.Interpolation CS.Dot
line showPointMarkers series =
    let
        { stroke, width, dashes } =
            (paint series.ink).line
    in
    C.interpolated (series.value >> toFloat)
        [ CA.color stroke, CA.width width, CA.dashed dashes ]
        (if showPointMarkers then
            [ CA.circle, CA.size 24, CA.color stroke, CA.border stroke, CA.borderWidth 0 ]

         else
            []
        )


segment : Series data -> C.Property data inter CS.Bar
segment series =
    let
        { fill, edge, edgeWidth } =
            (paint series.ink).bar
    in
    C.bar (series.value >> toFloat)
        [ CA.color fill, CA.border edge, CA.borderWidth edgeWidth ]


type alias LinePaint =
    { stroke : String
    , width : Float
    , dashes : List Float
    }


type alias BarPaint =
    { fill : String
    , edge : String
    , edgeWidth : Float
    }


{-| How each ink draws a line and a bar segment, side by side so the two read as the same
series. The chart and its legend both draw from here. A solid segment names its fill as
its edge too, rather than leave the edge to elm-charts.
-}
paint : Ink -> { line : LinePaint, bar : BarPaint }
paint ink =
    case ink of
        Accent ->
            { line = { stroke = "var(--accent)", width = 2, dashes = [] }
            , bar = { fill = "var(--accent)", edge = "var(--accent)", edgeWidth = 0 }
            }

        AccentSoft ->
            { line = { stroke = "var(--accent)", width = 1.5, dashes = [ 4, 3 ] }
            , bar = { fill = "var(--accent-soft)", edge = "var(--accent)", edgeWidth = 1 }
            }

        Strong ->
            { line = { stroke = "var(--fg)", width = 1.5, dashes = [] }
            , bar = { fill = "var(--fg)", edge = "var(--fg)", edgeWidth = 0 }
            }

        StrongSoft ->
            { line = { stroke = "var(--fg)", width = 1.5, dashes = [ 8, 3 ] }
            , bar = { fill = "var(--faint)", edge = "var(--fg)", edgeWidth = 1 }
            }

        AccentHollow ->
            { line = { stroke = "var(--accent)", width = 1, dashes = [ 1, 3 ] }
            , bar = { fill = "var(--bg)", edge = "var(--accent)", edgeWidth = 1.5 }
            }

        Muted ->
            { line = { stroke = "var(--dim)", width = 1.5, dashes = [ 4, 3 ] }
            , bar = { fill = "var(--dim)", edge = "var(--dim)", edgeWidth = 0 }
            }

        Faint ->
            { line = { stroke = "var(--dim)", width = 1, dashes = [ 1, 3 ] }
            , bar = { fill = "var(--faint)", edge = "var(--edge)", edgeWidth = 1 }
            }


legendEntry : Svg.Svg msg -> String -> Html msg
legendEntry swatch label =
    li [] [ swatch, text label ]


lineSwatch : LinePaint -> Svg.Svg msg
lineSwatch { stroke, width, dashes } =
    swatchFrame
        [ Svg.line
            [ SA.x1 "0"
            , SA.y1 "5"
            , SA.x2 "20"
            , SA.y2 "5"
            , SA.stroke stroke
            , SA.strokeWidth (String.fromFloat width)
            , SA.strokeDasharray (String.join " " (List.map String.fromFloat dashes))
            ]
            []
        ]


barSwatch : BarPaint -> Svg.Svg msg
barSwatch { fill, edge, edgeWidth } =
    swatchFrame
        [ Svg.rect
            [ SA.x "1"
            , SA.y "1"
            , SA.width "18"
            , SA.height "8"
            , SA.fill fill
            , SA.stroke edge
            , SA.strokeWidth (String.fromFloat edgeWidth)
            ]
            []
        ]


swatchFrame : List (Svg.Svg msg) -> Svg.Svg msg
swatchFrame =
    Svg.svg [ SA.class "chart-swatch", SA.viewBox "0 0 20 10", attribute "aria-hidden" "true" ]


{-| The chart's numbers as a table, hidden from sight but not from a screen reader, so they
can be read without a pointer. The box around it is what is hidden: a table sizes to its
content whatever width it is given, so hiding the table itself still widens the page.
-}
numbers :
    { rowHeading : String
    , rowLabel : data -> String
    , columns : List (Series data)
    }
    -> List data
    -> Html msg
numbers config data =
    div [ class "visually-hidden" ]
        [ table []
            [ thead []
                [ tr [] (th [] [ text config.rowHeading ] :: List.map (\column -> th [] [ text column.label ]) config.columns) ]
            , tbody []
                (List.map
                    (\datum ->
                        tr []
                            (th [] [ text (config.rowLabel datum) ]
                                :: List.map (\column -> td [] [ text (Format.count (column.value datum)) ]) config.columns
                            )
                    )
                    data
                )
            ]
        ]


{-| How the time axis is drawn for the moments the data falls at: the span it covers,
the labels along it, and whether each point needs a dot to be seen at all.
-}
type alias TimeAxis data msg =
    { range : List (CA.Attribute CS.Axis)
    , labels : List (C.Element data msg)
    , showPointMarkers : Bool
    }


{-| Times along the bottom, written the way Magnes writes them rather than in elm-charts'
US-style `10/4`. Usually they are the "nice" moments `terezka/intervals` picks for the
span: the ticks `C.generate (C.times zone)` would give, taken from intervals directly so
that their unit can be seen before they are used. Two spans need something else, because
buckets are a minute apart at the finest:

  - **One moment.** elm-charts widens it to 10 ms against the count axis, and labels that
    with the same minute over and over. It is centred and labelled once, in full, and its
    points are marked with dots, since a line through one point draws nothing.
  - **A few minutes.** Intervals ticks the seconds, which all read as the same minute. Each
    bucket's own minute is labelled instead.

-}
timeAxis : Time.Zone -> List Time.Posix -> TimeAxis data msg
timeAxis zone moments =
    let
        millis =
            List.map (Time.posixToMillis >> toFloat) moments

        plain labels =
            { range = [], labels = labels, showPointMarkers = False }
    in
    case ( List.minimum millis, List.maximum millis ) of
        ( Just first, Just last ) ->
            if first == last then
                { range = [ CA.lowest (first - 1) CA.exactly, CA.highest (first + 1) CA.exactly ]
                , labels = [ timeLabel first (Format.dateTime zone (Time.millisToPosix (round first))) ]
                , showPointMarkers = True
                }

            else
                let
                    ticks =
                        Intervals.times zone 8 { min = first, max = last }
                in
                if List.any (\tick -> List.member tick.unit [ Intervals.Millisecond, Intervals.Second ]) ticks then
                    plain (List.map (\at -> timeLabel (toFloat (Time.posixToMillis at)) (Format.time zone at)) moments)

                else
                    plain (List.map (\tick -> timeLabel (toFloat (Time.posixToMillis tick.timestamp)) (tickText tick)) ticks)

        _ ->
            plain []


timeLabel : Float -> String -> C.Element data msg
timeLabel x label =
    C.xLabel (CA.x x :: labelStyle) [ Svg.text label ]


{-| A tick's label, by the unit intervals stepped in and what changed since the tick
before: the clock time within a day, the date where a day turns, the month across months,
and the year, alone or after the date, where a year turns. A reader can always tell which
day, and which year, a stretch of the line belongs to.
-}
tickText : Intervals.Time -> String
tickText tick =
    let
        zone =
            tick.zone

        at =
            tick.timestamp

        year =
            String.fromInt (Time.toYear zone at)

        yearTurned =
            tick.change == Just Intervals.Year

        clockTick =
            if List.member tick.change [ Just Intervals.Day, Just Intervals.Month, Just Intervals.Year ] then
                dated

            else
                Format.time zone at

        dated =
            if yearTurned then
                shortDate zone at ++ " " ++ year

            else
                shortDate zone at
    in
    case tick.unit of
        Intervals.Year ->
            year

        Intervals.Month ->
            if yearTurned then
                year

            else
                monthName (Time.toMonth zone at)

        Intervals.Day ->
            dated

        Intervals.Hour ->
            clockTick

        Intervals.Minute ->
            clockTick

        Intervals.Second ->
            clockTick

        Intervals.Millisecond ->
            clockTick


{-| `5 Oct`: short, and with the month named, not ambiguous between day-first and
month-first readers.
-}
shortDate : Time.Zone -> Time.Posix -> String
shortDate zone at =
    String.fromInt (Time.toDay zone at) ++ " " ++ monthName (Time.toMonth zone at)


monthName : Time.Month -> String
monthName month =
    case month of
        Time.Jan ->
            "Jan"

        Time.Feb ->
            "Feb"

        Time.Mar ->
            "Mar"

        Time.Apr ->
            "Apr"

        Time.May ->
            "May"

        Time.Jun ->
            "Jun"

        Time.Jul ->
            "Jul"

        Time.Aug ->
            "Aug"

        Time.Sep ->
            "Sep"

        Time.Oct ->
            "Oct"

        Time.Nov ->
            "Nov"

        Time.Dec ->
            "Dec"


{-| Counts up the left edge, with a gridline at each. Drawn a label at a time rather than
with `C.yLabels`, whose labels take no border colour and so always carry elm-charts'
white one.
-}
countAxis : C.Element data msg
countAxis =
    C.generate 5 C.ints .y [] <|
        \_ n ->
            [ C.yLabel (CA.y (toFloat n) :: CA.withGrid :: labelStyle) [ Svg.text (Format.count n) ] ]


labelStyle : List (CA.Attribute { a | color : String, border : String, borderWidth : Float, fontSize : Maybe Int })
labelStyle =
    [ CA.color "var(--dim)", CA.border "var(--bg)", CA.borderWidth 0, CA.fontSize 12 ]
