module ExpiryTest exposing (suite)

import Expect
import Expiry
import Test exposing (Test, describe, test)


suite : Test
suite =
    describe "Expiry.options"
        [ test "offers ISO 8601 durations, the form the Duration scalar parses" <|
            \_ ->
                -- Not Go duration strings: bitmagnet's Duration is gqlgen's built-in, and the
                -- Go form answers INTERNAL_SERVER_ERROR. Verified against a running instance
                -- on 2026-09-03 for both auth.invite and self.createAPIKey.
                Expiry.options
                    |> List.map Tuple.second
                    |> Expect.equal [ "", "PT24H", "P7D", "P30D" ]
        , test "leads with a never-expires option, encoded as the empty string" <|
            \_ ->
                List.head Expiry.options
                    |> Expect.equal (Just ( "Never", "" ))
        ]
