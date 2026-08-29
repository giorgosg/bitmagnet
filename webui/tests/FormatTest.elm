module FormatTest exposing (suite)

import Expect
import Format
import Test exposing (Test, describe, test)
import Time


{-| 2026-08-25T14:03:07Z.
-}
moment : Time.Posix
moment =
    Time.millisToPosix 1787666587000


suite : Test
suite =
    describe "Format"
        [ describe "date"
            [ test "is the sortable, locale-free form" <|
                \_ ->
                    Format.date Time.utc moment
                        |> Expect.equal "2026-08-25"
            ]
        , describe "dateTime"
            [ test "adds the clock time to the date" <|
                \_ ->
                    Format.dateTime Time.utc moment
                        |> Expect.equal "2026-08-25 14:03"
            , test "pads a single-digit hour and minute" <|
                \_ ->
                    -- 2026-08-25T04:07:00Z
                    Format.dateTime Time.utc (Time.millisToPosix 1787630820000)
                        |> Expect.equal "2026-08-25 04:07"
            , test "is read in the given zone, not UTC" <|
                \_ ->
                    Format.dateTime (Time.customZone 90 []) moment
                        |> Expect.equal "2026-08-25 15:33"
            ]
        ]
