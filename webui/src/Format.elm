module Format exposing (bytes, count, date, dateTime)

{-| Human-readable numbers and dates: sizes and counts for the row, dates and times
wherever a moment is shown to a person.
-}

import Time


{-| `2026-08-06`. Sortable, unambiguous across locales, and the same width every time,
which matters in a column.
-}
date : Time.Zone -> Time.Posix -> String
date zone posix =
    String.fromInt (Time.toYear zone posix)
        ++ "-"
        ++ pad (monthNumber (Time.toMonth zone posix))
        ++ "-"
        ++ pad (Time.toDay zone posix)


{-| The same date with the clock time after it, for the one place a moment matters to the
minute rather than to the day: when a User last signed in. Seconds are left off — they say
nothing a person reads this for — and the zone is the browser's, since this is being
compared against "was that me?".
-}
dateTime : Time.Zone -> Time.Posix -> String
dateTime zone posix =
    date zone posix
        ++ " "
        ++ pad (Time.toHour zone posix)
        ++ ":"
        ++ pad (Time.toMinute zone posix)


pad : Int -> String
pad n =
    if n < 10 then
        "0" ++ String.fromInt n

    else
        String.fromInt n


monthNumber : Time.Month -> Int
monthNumber month =
    case month of
        Time.Jan ->
            1

        Time.Feb ->
            2

        Time.Mar ->
            3

        Time.Apr ->
            4

        Time.May ->
            5

        Time.Jun ->
            6

        Time.Jul ->
            7

        Time.Aug ->
            8

        Time.Sep ->
            9

        Time.Oct ->
            10

        Time.Nov ->
            11

        Time.Dec ->
            12


{-| Binary multiples with the short labels torrent clients use — 1 GB here is 2^30 bytes.

Sizes routinely exceed the 32-bit range GraphQL nominally allows for `Int`; Elm's `Int`
is a JS number and carries them fine.

-}
bytes : Int -> String
bytes n =
    let
        go value units =
            case units of
                unit :: rest ->
                    if value < 1024 || List.isEmpty rest then
                        oneDecimal value ++ " " ++ unit

                    else
                        go (value / 1024) rest

                [] ->
                    oneDecimal value ++ " B"
    in
    if n < 1024 then
        String.fromInt n ++ " B"

    else
        go (toFloat n / 1024) [ "KB", "MB", "GB", "TB", "PB" ]


{-| One decimal below ten, none above — "1.4 GB" but "997 MB".
-}
oneDecimal : Float -> String
oneDecimal value =
    if value < 10 then
        let
            tenths =
                round (value * 10)
        in
        String.fromInt (tenths // 10) ++ "." ++ String.fromInt (modBy 10 tenths)

    else
        String.fromInt (round value)


{-| Thousands separators, so a result count reads at a glance.
-}
count : Int -> String
count n =
    String.fromInt n
        |> String.toList
        |> List.reverse
        |> groupsOfThree
        |> List.reverse
        |> String.join ","


groupsOfThree : List Char -> List String
groupsOfThree chars =
    case chars of
        [] ->
            []

        _ ->
            (List.take 3 chars |> List.reverse |> String.fromList)
                :: groupsOfThree (List.drop 3 chars)
