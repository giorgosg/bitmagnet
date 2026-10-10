module StatsControlsTest exposing (suite)

import Expect
import Html
import Html.Attributes
import Json.Decode as Decode
import Json.Encode as Encode
import Magnes.Api.Enum.MetricsBucketDuration exposing (MetricsBucketDuration(..))
import StatsControls exposing (AutoRefresh(..), Controls, Timeframe(..))
import Test exposing (Test, describe, test)
import Test.Html.Event as Event
import Test.Html.Query as Query
import Test.Html.Selector as Selector
import Time
import Url.Builder as Builder


suite : Test
suite =
    describe "StatsControls"
        [ describe "in the URL"
            [ test "the defaults leave the address bare" <|
                \_ ->
                    href StatsControls.default
                        |> Expect.equal "/stats"
            , test "every choice is written as a parameter of its own" <|
                \_ ->
                    href
                        { timeframe = Hours6
                        , resolution = { unit = Hour, every = Just 2 }
                        , refresh = Every30Seconds
                        }
                        |> Expect.equal "/stats?timeframe=6h&resolution=hour&every=2&refresh=30s"
            , test "a multiplier left to Magnes is not written, and one that is chosen is, even when it is 1" <|
                \_ ->
                    ( href { defaults | resolution = { unit = Minute, every = Nothing } }
                    , href { defaults | resolution = { unit = Minute, every = Just 1 } }
                    )
                        |> Expect.equal ( "/stats", "/stats?every=1" )
            , test "reads back what it wrote, for every timeframe and refresh interval" <|
                \_ ->
                    let
                        everyChoice =
                            List.concatMap
                                (\timeframe ->
                                    List.map
                                        (\refresh ->
                                            { timeframe = timeframe
                                            , resolution = { unit = Day, every = Just 3 }
                                            , refresh = refresh
                                            }
                                        )
                                        StatsControls.allRefreshes
                                )
                                StatsControls.allTimeframes
                    in
                    everyChoice
                        |> List.map (\controls -> parsed (queryOf controls))
                        |> Expect.equalLists everyChoice
            , test "falls back to the default for what it does not recognise, rather than failing the page" <|
                \_ ->
                    StatsControls.fromParams
                        { defaults = StatsControls.default
                        , timeframes = StatsControls.allTimeframes
                        , timeframe = Just "fortnight"
                        , resolution = Just "week"
                        , every = Just 0
                        , refresh = Just "2s"
                        }
                        |> Expect.equal StatsControls.default
            , test "treats a multiplier below 1 or above 10,000 as none" <|
                \_ ->
                    ( multiplierFrom (Just -5), multiplierFrom (Just 10001), multiplierFrom (Just 10000) )
                        |> Expect.equal ( Nothing, Nothing, Just 10000 )
            , test "ignores a timeframe the page does not offer" <|
                \_ ->
                    StatsControls.fromParams
                        { defaults = StatsControls.default
                        , timeframes = List.filter ((/=) AllTime) StatsControls.allTimeframes
                        , timeframe = Just "all"
                        , resolution = Nothing
                        , every = Nothing
                        , refresh = Nothing
                        }
                        |> Expect.equal StatsControls.default
            , test "reads the timeframe 'all' where the page offers it" <|
                \_ ->
                    StatsControls.fromParams
                        { defaults = StatsControls.default
                        , timeframes = StatsControls.allTimeframes
                        , timeframe = Just "all"
                        , resolution = Nothing
                        , every = Nothing
                        , refresh = Nothing
                        }
                        |> .timeframe
                        |> Expect.equal AllTime
            ]
        , describe "for a page that starts somewhere else"
            [ test "leaves out what is that page's default, and writes what the torrent page's default is not" <|
                \_ ->
                    ( hrefFrom queueStart queueStart
                    , hrefFrom queueStart { queueStart | timeframe = Hours1, resolution = { unit = Minute, every = Nothing } }
                    )
                        |> Expect.equal ( "/stats", "/stats?timeframe=1h&resolution=minute" )
            , test "reads an address that says nothing as that page's default" <|
                \_ ->
                    StatsControls.fromParams
                        { defaults = queueStart
                        , timeframes = StatsControls.allTimeframes
                        , timeframe = Nothing
                        , resolution = Nothing
                        , every = Nothing
                        , refresh = Nothing
                        }
                        |> Expect.equal queueStart
            , test "reads back what it wrote, whichever the default" <|
                \_ ->
                    let
                        shifted =
                            { queueStart | timeframe = Days1, resolution = { unit = Minute, every = Just 5 }, refresh = EveryMinute }
                    in
                    StatsControls.fromParams
                        { defaults = queueStart
                        , timeframes = StatsControls.allTimeframes
                        , timeframe = Just "1d"
                        , resolution = Just "minute"
                        , every = Just 5
                        , refresh = Just "1m"
                        }
                        |> Expect.equal shifted
            ]
        , describe "boundedTimeframes"
            [ test "are every timeframe but the one of everything, which only a page bounded by what it holds offers" <|
                \_ ->
                    StatsControls.boundedTimeframes
                        |> Expect.equal (List.filter ((/=) AllTime) StatsControls.allTimeframes)
            ]
        , describe "withoutRefresh"
            [ test "is the same look, not asked again by itself" <|
                \_ ->
                    StatsControls.withoutRefresh { timeframe = Days1, resolution = { unit = Hour, every = Just 3 }, refresh = Every30Seconds }
                        |> Expect.equal { timeframe = Days1, resolution = { unit = Hour, every = Just 3 }, refresh = Off }
            ]
        , describe "the multiplier's field"
            [ test "keeps a number typed, and hands the choice back for none" <|
                \_ ->
                    [ "15", "", "abc" ]
                        |> List.map typedMultiplier
                        |> Expect.equal [ Just (Just 15), Just Nothing, Just Nothing ]
            , test "brings a number outside what a multiplier can be to the nearest it can, instead of dropping it" <|
                \_ ->
                    [ "0", "-3", "20000", "10000" ]
                        |> List.map typedMultiplier
                        |> Expect.equal [ Just (Just 1), Just (Just 1), Just (Just 10000), Just (Just 10000) ]
            , test "rounds a number that is not whole" <|
                \_ ->
                    [ "2.4", "2.5", "2.6" ]
                        |> List.map typedMultiplier
                        |> Expect.equal [ Just (Just 2), Just (Just 3), Just (Just 3) ]
            , test "shows the multiplier in force, and where none was chosen the one the chart came to" <|
                \_ ->
                    ( fieldOf { defaults | resolution = { unit = Hour, every = Just 4 } } (Just 60)
                        |> Query.has [ Selector.attribute (Html.Attributes.value "4") ]
                    , fieldOf defaults (Just 60)
                        |> Query.has [ Selector.attribute (Html.Attributes.value ""), Selector.attribute (Html.Attributes.placeholder "60") ]
                    , fieldOf defaults Nothing
                        |> Query.has [ Selector.attribute (Html.Attributes.placeholder "auto") ]
                    )
                        |> (\( chosen, picked, unknown ) -> Expect.all [ always chosen, always picked, always unknown ] ())
            ]
        , describe "window"
            [ test "reaches back from now by the length of the timeframe" <|
                \_ ->
                    ( StatsControls.window now { defaults | timeframe = Minutes15 }
                    , StatsControls.window now { defaults | timeframe = Weeks1 }
                    )
                        |> Expect.equal
                            ( { from = Just (Time.millisToPosix (nowMillis - 15 * 60 * 1000)), to = now }
                            , { from = Just (Time.millisToPosix (nowMillis - 7 * 24 * 60 * 60 * 1000)), to = now }
                            )
            , test "has no start for everything" <|
                \_ ->
                    StatsControls.window now { defaults | timeframe = AllTime }
                        |> Expect.equal { from = Nothing, to = now }
            ]
        , describe "auto-refresh"
            [ test "waits the interval it names, and not at all when off" <|
                \_ ->
                    List.map StatsControls.refreshMillis StatsControls.allRefreshes
                        |> Expect.equal [ Nothing, Just 10000, Just 30000, Just 60000, Just 300000 ]
            ]
        ]


defaults : Controls
defaults =
    StatsControls.default


{-| Where the queue's statistics would start: everything, by the hour.
-}
queueStart : Controls
queueStart =
    { timeframe = AllTime
    , resolution = { unit = Hour, every = Nothing }
    , refresh = Off
    }


nowMillis : Int
nowMillis =
    1791626400000


now : Time.Posix
now =
    Time.millisToPosix nowMillis


href : Controls -> String
href =
    hrefFrom StatsControls.default


hrefFrom : Controls -> Controls -> String
hrefFrom start controls =
    Builder.absolute [ "stats" ] (StatsControls.toParams start controls)


{-| The controls a page reads from a query string made of `controls`' parameters, which is
what an address is read as.
-}
parsed : List ( String, String ) -> Controls
parsed pairs =
    let
        get key =
            pairs |> List.filter (Tuple.first >> (==) key) |> List.head |> Maybe.map Tuple.second
    in
    StatsControls.fromParams
        { defaults = StatsControls.default
        , timeframes = StatsControls.allTimeframes
        , timeframe = get "timeframe"
        , resolution = get "resolution"
        , every = get "every" |> Maybe.andThen String.toInt
        , refresh = get "refresh"
        }


{-| `toParams` as name and value pairs, read back out of the address it builds.
-}
queryOf : Controls -> List ( String, String )
queryOf controls =
    href controls
        |> String.split "?"
        |> List.drop 1
        |> List.head
        |> Maybe.withDefault ""
        |> String.split "&"
        |> List.filter (not << String.isEmpty)
        |> List.filterMap
            (\pair ->
                case String.split "=" pair of
                    [ key, value ] ->
                        Just ( key, value )

                    _ ->
                        Nothing
            )


multiplierFrom : Maybe Int -> Maybe Int
multiplierFrom every =
    StatsControls.fromParams
        { defaults = StatsControls.default
        , timeframes = StatsControls.allTimeframes
        , timeframe = Nothing
        , resolution = Nothing
        , every = every
        , refresh = Nothing
        }
        |> .resolution
        |> .every


{-| The rows a page draws, around a message that says what the controls would be.
-}
rowsFor : Controls -> Maybe Int -> Query.Single Controls
rowsFor controls picked =
    Html.div []
        (StatsControls.rows
            { timeframes = StatsControls.boundedTimeframes
            , picked = picked
            , change = identity
            , refreshRequested = controls
            }
            controls
        )
        |> Query.fromHtml


fieldOf : Controls -> Maybe Int -> Query.Single Controls
fieldOf controls picked =
    rowsFor controls picked
        |> Query.find [ Selector.tag "input" ]


{-| What the controls become when `raw` is typed into the field and committed, or `Nothing`
when nothing is asked for.
-}
typedMultiplier : String -> Maybe (Maybe Int)
typedMultiplier raw =
    fieldOf defaults Nothing
        |> Event.simulate (changeTo raw)
        |> Event.toResult
        |> Result.toMaybe
        |> Maybe.map (\controls -> controls.resolution.every)


changeTo : String -> ( String, Decode.Value )
changeTo value =
    Event.custom "change" (Encode.object [ ( "target", Encode.object [ ( "value", Encode.string value ) ] ) ])
