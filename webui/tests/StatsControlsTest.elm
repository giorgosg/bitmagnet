module StatsControlsTest exposing (suite)

import Expect
import Magnes.Api.Enum.MetricsBucketDuration exposing (MetricsBucketDuration(..))
import StatsControls exposing (AutoRefresh(..), Controls, Timeframe(..))
import Test exposing (Test, describe, test)
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
