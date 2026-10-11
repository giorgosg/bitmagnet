module PasswordEntropy exposing (Entropy(..), Strength, answered, forPassword, landed, measure, meter)

{-| What bitmagnet says about a password being typed, and the meter that draws it.

Registration and the password change on `/account` both use it: the measurement is
bitmagnet's own (`self.passwordEntropy`), against the instance's own minimum, so the meter
reads the same wherever a password is chosen. Nothing here gates submission: bitmagnet
decides, and this is advice on the way.

The debounce lives in `Main`, which owns the timer and the counter that drops an answer a
newer keystroke has replaced.

-}

import Bitmagnet
import Graphql.Http
import Graphql.Operation exposing (RootQuery)
import Graphql.SelectionSet as SelectionSet exposing (SelectionSet)
import Html exposing (Html, div, p, text)
import Html.Attributes exposing (attribute, class, value)
import Magnes.Api.Object.PasswordEntropyResult as PasswordEntropyResult
import Magnes.Api.Object.SelfQuery as SelfQuery
import Magnes.Api.Query as Query


{-| What the server last said about the password being typed.

`Unmeasurable` is not "weak". A meter drawn from a query that never answered would read as
zero against the minimum, which is a statement about the password rather than about the
request — so a failed measurement draws no meter at all.

-}
type Entropy
    = Unmeasured
    | Measuring
    | Measured Strength
    | Unmeasurable


{-| `minEntropy` comes from the server rather than being a constant here, because it is a
configurable instance setting. The server also returns its own `valid` verdict on the
pair, which is deliberately not selected: nothing may act on it — submission is not gated
on the measurement — so carrying it would be storing an answer to a question no form here
asks.
-}
type alias Strength =
    { entropy : Float
    , minEntropy : Float
    }


{-| What a newly typed password is owed. A new password abandons the old measurement rather
than leaving it on screen, where it would describe a password that is no longer typed. An
empty field has nothing to measure and nothing to say.
-}
forPassword : String -> Entropy
forPassword password =
    if String.isEmpty password then
        Unmeasured

    else
        Measuring


{-| The password is sent to bitmagnet to be scored. That is what the query is for and what
bitmagnet's own UI does; the caller's job is to ask rarely, since the Angular UI asks once
per keystroke. It answers anonymously, so the meter works before anyone has signed in.
-}
measure : String -> String -> (Result (Graphql.Http.Error Strength) Strength -> msg) -> Cmd msg
measure apiUrl password toMsg =
    strength password
        |> Bitmagnet.queryRequest apiUrl
        |> Graphql.Http.send toMsg


strength : String -> SelectionSet Strength RootQuery
strength password =
    Query.self
        (SelfQuery.passwordEntropy { password = password }
            (SelectionSet.map2 Strength
                PasswordEntropyResult.entropy
                PasswordEntropyResult.minEntropy
            )
        )


{-| The password is not the thing that failed, so nothing is claimed about it when the
query does. The form stays submittable, and bitmagnet is the one that decides anyway.
-}
answered : Result error Strength -> Entropy
answered result =
    case result of
        Ok measurement ->
            Measured measurement

        Err _ ->
            Unmeasurable


{-| A measurement, landing on what a form holds for its password. It lands only on a password
still waiting for one: a field emptied since the measurement was asked for, by the person or by
the form being dropped, has no password for it to describe. Both forms that measure go through
here, so they keep the same rule.
-}
landed : Entropy -> Entropy -> Entropy
landed measurement current =
    if current == Measuring then
        measurement

    else
        current


{-| A real `progress` element, so the platform draws it and assistive technology reads it
without any of it being reimplemented here. The numbers are shown as well as the bar: "42
of 70" is the actionable form, and the bar alone cannot say how far short it falls.
-}
meter : Entropy -> Html msg
meter measurement =
    case measurement of
        Measured strengthOf ->
            div [ class "entropy" ]
                [ Html.progress
                    [ attribute "max" (String.fromFloat strengthOf.minEntropy)
                    , value (String.fromFloat (min strengthOf.entropy strengthOf.minEntropy))
                    , attribute "aria-label" "Password strength"
                    ]
                    []
                , p [ class "entropy-reading" ]
                    [ text
                        (bits strengthOf.entropy
                            ++ " of "
                            ++ bits strengthOf.minEntropy
                            ++ " bits needed"
                        )
                    ]
                ]

        -- A `progress` with no value is the platform's indeterminate bar, which is
        -- exactly what is true here. The alternatives are both worse: an empty bar reads
        -- as a weak password, and the previous score describes a password that is no
        -- longer typed. Keeping the element also stops the meter appearing and vanishing
        -- on every pause in typing.
        Measuring ->
            div [ class "entropy" ]
                [ Html.progress [ attribute "aria-label" "Password strength" ] []
                , p [ class "entropy-reading" ] [ text "Measuring…" ]
                ]

        Unmeasured ->
            text ""

        Unmeasurable ->
            text ""


{-| bitmagnet returns these as floats. They are bit counts, so the decimals are noise.
-}
bits : Float -> String
bits value =
    String.fromInt (round value)
