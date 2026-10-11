module MainTest exposing (suite)

{-| `Main` holds no logic worth a unit test: each decision it makes is a call into a module
with tests of its own. What it does hold is wiring, and wiring that does not compile must fail
`npx elm-test` as well as `elm make`. Importing it here is what makes elm-test compile it.
Nothing is run: running it would need a `Browser.Navigation.Key`, and a `Program` can be
neither compared nor inspected, so the expectation passes for any value. That it type-checks
is the test.
-}

import Expect
import Main
import Test exposing (Test, test)


suite : Test
suite =
    test "Main compiles, so the wiring is checked by the test run as well as the build" <|
        \_ ->
            Main.main
                |> Expect.all [ \_ -> Expect.pass ]
