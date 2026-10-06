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
import Html.Attributes exposing (attribute, class)
import Intervals
import Svg
import Svg.Attributes as SA
import Time


{-| How a series is drawn, from Magnes's colour tokens. Each works for both a line and a
bar segment, so a status keeps its look across the two charts.

  - `Accent`: the one colour, for what wants attention. A heavier line; a solid bar.
  - `AccentSoft`: the accent held back. A dashed accent line; a pale bar edged in accent.
  - `Strong`: the text colour. A solid line; a solid bar.
  - `Muted`: the secondary grey. A dashed line; a solid grey bar.
  - `Faint`: barely there. A dotted grey line; a pale bar with an edge.

-}
type Ink
    = Accent
    | AccentSoft
    | Strong
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
day turns, month names across months, and the year where one turns.
-}
timeline : Timeline data -> List data -> Html msg
timeline config data =
    let
        sole =
            soleMoment (List.map config.time data)
    in
    chartFigure config.title data <|
        [ plot
            { description = config.description
            , top =
                data
                    |> List.concatMap (\datum -> List.map (\series -> series.value datum) config.series)
                    |> List.maximum
                    |> Maybe.withDefault 0
            , range = Maybe.map centredOn sole |> Maybe.withDefault []
            }
            [ timeAxis config.zone sole
            , C.series (config.time >> Time.posixToMillis >> toFloat)
                (List.map (line (sole /= Nothing)) config.series)
                data
            ]
        , ul [ class "chart-legend" ]
            (List.map (\series -> legendEntry (lineSwatch (linePaint series.ink)) series.label) config.series)
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
            , top =
                data
                    |> List.map (\datum -> List.sum (List.map (\series -> series.value datum) config.segments))
                    |> List.maximum
                    |> Maybe.withDefault 0
            , range = []
            }
            [ C.binLabels config.category (CA.moveDown 20 :: labelInk)
            , C.bars [ CA.margin 0.3 ] [ C.stacked (List.map segment config.segments) ] data
            ]
        , ul [ class "chart-legend" ]
            (List.map (\series -> legendEntry (barSwatch (barPaint series.ink)) series.label) config.segments)
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

-}
plot :
    { description : String, top : Int, range : List (CA.Attribute CS.Axis) }
    -> List (C.Element data msg)
    -> Html msg
plot { description, top, range } elements =
    div [ class "chart-plot" ]
        [ C.chart
            [ CA.width 920
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


{-| A series' line. A line through a single moment draws nothing, so there, and only
there, each point is also marked with a dot.
-}
line : Bool -> Series data -> C.Property data CS.Interpolation CS.Dot
line single series =
    let
        paint =
            linePaint series.ink
    in
    C.interpolated (series.value >> toFloat)
        [ CA.color paint.stroke, CA.width paint.width, CA.dashed paint.dashes ]
        (if single then
            [ CA.circle, CA.size 24, CA.color paint.stroke, CA.border paint.stroke, CA.borderWidth 0 ]

         else
            []
        )


{-| How a series' line is drawn, in one place for both the chart and its legend.
-}
type alias LinePaint =
    { stroke : String
    , width : Float
    , dashes : List Float
    }


linePaint : Ink -> LinePaint
linePaint ink =
    case ink of
        Accent ->
            { stroke = "var(--accent)", width = 2, dashes = [] }

        AccentSoft ->
            { stroke = "var(--accent)", width = 1.5, dashes = [ 4, 3 ] }

        Strong ->
            { stroke = "var(--fg)", width = 1.5, dashes = [] }

        Muted ->
            { stroke = "var(--dim)", width = 1.5, dashes = [ 4, 3 ] }

        Faint ->
            { stroke = "var(--dim)", width = 1, dashes = [ 1, 3 ] }


segment : Series data -> C.Property data inter CS.Bar
segment series =
    let
        paint =
            barPaint series.ink
    in
    C.bar (series.value >> toFloat)
        [ CA.color paint.fill, CA.border paint.edge, CA.borderWidth paint.edgeWidth ]


{-| How a bar segment is drawn, in one place for both the chart and its legend. A solid
segment names its fill as its edge too, rather than leave the edge to elm-charts.
-}
type alias BarPaint =
    { fill : String
    , edge : String
    , edgeWidth : Float
    }


barPaint : Ink -> BarPaint
barPaint ink =
    case ink of
        Accent ->
            { fill = "var(--accent)", edge = "var(--accent)", edgeWidth = 0 }

        AccentSoft ->
            { fill = "var(--accent-soft)", edge = "var(--accent)", edgeWidth = 1 }

        Strong ->
            { fill = "var(--fg)", edge = "var(--fg)", edgeWidth = 0 }

        Muted ->
            { fill = "var(--dim)", edge = "var(--dim)", edgeWidth = 0 }

        Faint ->
            { fill = "var(--faint)", edge = "var(--edge)", edgeWidth = 1 }


legendEntry : Svg.Svg msg -> String -> Html msg
legendEntry swatch label =
    li [] [ swatch, text label ]


lineSwatch : LinePaint -> Svg.Svg msg
lineSwatch paint =
    swatchFrame
        [ Svg.line
            [ SA.x1 "0"
            , SA.y1 "5"
            , SA.x2 "20"
            , SA.y2 "5"
            , SA.stroke paint.stroke
            , SA.strokeWidth (String.fromFloat paint.width)
            , SA.strokeDasharray (String.join " " (List.map String.fromFloat paint.dashes))
            ]
            []
        ]


barSwatch : BarPaint -> Svg.Svg msg
barSwatch paint =
    swatchFrame
        [ Svg.rect
            [ SA.x "1"
            , SA.y "1"
            , SA.width "18"
            , SA.height "8"
            , SA.fill paint.fill
            , SA.stroke paint.edge
            , SA.strokeWidth (String.fromFloat paint.edgeWidth)
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


{-| Times along the bottom, at the "nice" moments `terezka/intervals` picks for the span
shown, written the way Magnes writes them rather than in elm-charts' US-style `10/4`.

Data at a single moment is labelled once, in full. elm-charts widens a span of nothing to
10 ms, and the moments picked across that would all read as the same minute.

-}
timeAxis : Time.Zone -> Maybe Time.Posix -> C.Element data msg
timeAxis zone sole =
    case sole of
        Just at ->
            timeLabelAt at (date zone at ++ " " ++ clock zone at)

        Nothing ->
            C.generate 8 (C.times zone) .x [] <|
                \_ tick -> [ timeLabelAt tick.timestamp (timeLabel tick) ]


{-| A time range with `at` in the middle, for data that all falls at one moment: left
to elm-charts, it would sit against the count axis.
-}
centredOn : Time.Posix -> List (CA.Attribute CS.Axis)
centredOn at =
    let
        millis =
            toFloat (Time.posixToMillis at)
    in
    [ CA.lowest (millis - 1) CA.exactly, CA.highest (millis + 1) CA.exactly ]


{-| The one moment every datum falls at, when they all do.
-}
soleMoment : List Time.Posix -> Maybe Time.Posix
soleMoment moments =
    case moments of
        first :: rest ->
            if List.all ((==) first) rest then
                Just first

            else
                Nothing

        [] ->
            Nothing


timeLabelAt : Time.Posix -> String -> C.Element data msg
timeLabelAt at label =
    C.xLabel (CA.x (toFloat (Time.posixToMillis at)) :: labelInk) [ Svg.text label ]


{-| The clock time within a day, and the date where the day turns, so a reader can always
tell which day a stretch of the line belongs to.
-}
timeLabel : Intervals.Time -> String
timeLabel tick =
    let
        zone =
            tick.zone

        at =
            tick.timestamp

        year =
            String.fromInt (Time.toYear zone at)
    in
    case tick.unit of
        Intervals.Year ->
            year

        Intervals.Month ->
            if tick.change == Just Intervals.Year then
                year

            else
                monthName (Time.toMonth zone at)

        Intervals.Day ->
            date zone at

        _ ->
            if List.member tick.change [ Just Intervals.Day, Just Intervals.Month, Just Intervals.Year ] then
                date zone at

            else
                clock zone at


{-| `5 Oct`: short, and with the month named, not ambiguous between day-first and
month-first readers.
-}
date : Time.Zone -> Time.Posix -> String
date zone at =
    String.fromInt (Time.toDay zone at) ++ " " ++ monthName (Time.toMonth zone at)


clock : Time.Zone -> Time.Posix -> String
clock zone at =
    pad (Time.toHour zone at) ++ ":" ++ pad (Time.toMinute zone at)


pad : Int -> String
pad n =
    String.padLeft 2 '0' (String.fromInt n)


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
            [ C.yLabel (CA.y (toFloat n) :: CA.withGrid :: labelInk) [ Svg.text (Format.count n) ] ]


labelInk : List (CA.Attribute { a | color : String, border : String, borderWidth : Float, fontSize : Maybe Int })
labelInk =
    [ CA.color "var(--dim)", CA.border "var(--bg)", CA.borderWidth 0, CA.fontSize 12 ]
