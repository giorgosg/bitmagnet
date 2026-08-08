module Magnes.Api.ScalarCodecs exposing (Date, DateTime, Duration, Hash20, Id, Void, Year, codecs)

{-| The one generated file elm-graphql intends you to edit; regeneration preserves it.

Mapped away from the default `String`-backed wrappers:

  - `DateTime` and `Date` become `Time.Posix`. bitmagnet sends RFC-3339 with microsecond
    precision (`2026-08-06T04:42:29.862002Z`); `Iso8601` accepts up to nine fractional
    digits, and a bare `2019-09-05` parses as midnight UTC.
  - `Year` becomes `Int` — it arrives as a JSON number, not a string.

`Hash20` stays opaque: it is an info hash, only ever passed through to a URL or back to
the API, and wrapping it keeps it from being confused with any other string. `Void` is
the return type of mutations, which Magnes does not call.

-}

import Iso8601
import Json.Decode as Decode exposing (Decoder)
import Json.Encode as Encode
import Magnes.Api.Scalar exposing (defaultCodecs)
import Time


type alias Date =
    Time.Posix


type alias DateTime =
    Time.Posix


type alias Duration =
    Magnes.Api.Scalar.Duration


type alias Hash20 =
    Magnes.Api.Scalar.Hash20


type alias Id =
    Magnes.Api.Scalar.Id


type alias Void =
    Magnes.Api.Scalar.Void


type alias Year =
    Int


codecs : Magnes.Api.Scalar.Codecs Date DateTime Duration Hash20 Id Void Year
codecs =
    Magnes.Api.Scalar.defineCodecs
        { codecDate = posixCodec
        , codecDateTime = posixCodec
        , codecDuration = defaultCodecs.codecDuration
        , codecHash20 = defaultCodecs.codecHash20
        , codecId = defaultCodecs.codecId
        , codecVoid = defaultCodecs.codecVoid
        , codecYear =
            { encoder = Encode.int
            , decoder = Decode.int
            }
        }


posixCodec : { encoder : Time.Posix -> Encode.Value, decoder : Decoder Time.Posix }
posixCodec =
    { encoder = Iso8601.encode
    , decoder = Iso8601.decoder
    }
