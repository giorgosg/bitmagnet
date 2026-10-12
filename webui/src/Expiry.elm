module Expiry exposing (options)

{-| The expiry spans Magnes offers, shared by everything that sets one: an Invitation and an
API key both take bitmagnet's `Duration`, and both want the same handful of durations.

The values are **ISO 8601**, because that is what the `Duration` scalar parses. It is
gqlgen's built-in, whose `UnmarshalDuration` reads `PT24H`/`P7D` and answers
`INTERNAL_SERVER_ERROR` — not a clean rejection — for a Go duration string like `24h0m0s`.
Verified against a running bitmagnet on 2026-09-03 for both `auth.invite` and
`self.createAPIKey`. See `Magnes.Api.ScalarCodecs` for the scalar itself.

A fixed set rather than a free-text field: every value here is one the scalar accepts,
which a typed one need not be, and these are the spans anyone actually wants.

-}


{-| Label and ISO 8601 duration. The empty string is "never": bitmagnet reads a missing
expiry as no expiry, so an empty option encodes as an absent argument rather than a
duration.
-}
options : List ( String, String )
options =
    [ ( "Never", "" )
    , ( "24 hours", "PT24H" )
    , ( "7 days", "P7D" )
    , ( "30 days", "P30D" )
    ]
