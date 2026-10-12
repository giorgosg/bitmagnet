module PasswordEntropyTest exposing (suite)

import Expect
import PasswordEntropy
import Test exposing (Test, describe, test)


reading : PasswordEntropy.Entropy
reading =
    PasswordEntropy.Measured { entropy = 80, minEntropy = 70 }


suite : Test
suite =
    describe "PasswordEntropy"
        [ describe "landed"
            [ test "a measurement lands on a password waiting for one" <|
                \_ ->
                    PasswordEntropy.landed reading PasswordEntropy.Measuring
                        |> Expect.equal reading
            , test "a failed measurement lands too, as no reading rather than a weak one" <|
                \_ ->
                    PasswordEntropy.landed PasswordEntropy.Unmeasurable PasswordEntropy.Measuring
                        |> Expect.equal PasswordEntropy.Unmeasurable
            , test "a measurement does not land on a field emptied since it was asked for" <|
                \_ ->
                    -- An emptied field, by the person or by the form being dropped, has no
                    -- password for it to describe.
                    PasswordEntropy.landed reading PasswordEntropy.Unmeasured
                        |> Expect.equal PasswordEntropy.Unmeasured
            ]
        ]
