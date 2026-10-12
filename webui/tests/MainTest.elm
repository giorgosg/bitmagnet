module MainTest exposing (suite)

{-| Main's failure wording is tested here; the credentialed browser suite exercises its
retry wiring, which needs a real Navigation.Key. Importing main also makes elm-test
compile the application wiring.
-}

import Expect
import Main
import Test exposing (Test, describe, test)


suite : Test
suite =
    describe "Identity refresh"
        [ test "Main compiles, so the wiring is checked by the test run as well as the build" <|
            \_ ->
                Main.main
                    |> Expect.all [ \_ -> Expect.pass ]
        , test "a failed Identity fetch explains that sign-in succeeded and retains the cause" <|
            \_ ->
                Main.refreshFailure Main.AfterSignIn "Could not reach bitmagnet."
                    |> Expect.equal "You are signed in. Magnes could not load your Identity: Could not reach bitmagnet."
        , test "startup failure keeps its existing wording" <|
            \_ ->
                Main.refreshFailure Main.Resolving "Could not reach bitmagnet."
                    |> Expect.equal "Could not reach bitmagnet."
        , test "sign-out failure keeps its existing wording" <|
            \_ ->
                Main.refreshFailure Main.AfterSignOut "Could not reach bitmagnet."
                    |> Expect.equal "You are signed out. Magnes could not load what this instance allows without a User: Could not reach bitmagnet."
        , test "password-change failure keeps its existing wording" <|
            \_ ->
                Main.refreshFailure Main.AfterPasswordChange "Could not reach bitmagnet."
                    |> Expect.equal "Your password was changed, and that signed you out everywhere. Magnes could not load what this instance allows without a User: Could not reach bitmagnet."
        ]
