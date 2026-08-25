module ApiError exposing (Failure(..), ObjectAction, Refusal, fromError, isUnauthorized, toMessage)

{-| The single boundary where a bitmagnet failure becomes a UI state.

bitmagnet's error presenter emits a stable `extensions.code` on every identity and
authorization failure, so the mapping switches on that code and never on the wrapped
English message. Messages stay presentation: they are read only as the fallback for a
code Magnes does not know, which is what keeps a newer server's outcome legible here.

Everything that inspects a failure goes through `Failure`. Nothing outside this module
matches on error text, so extending the contract — a new code, or the `namespace`,
`object` and `action` on an authorization refusal growing richer — is a local change.

A GraphQL response may carry partial data alongside its errors. Magnes sends every
request with `Graphql.Http.send`, which surfaces any error as `Err` regardless, and
identity and administration call sites treat that as total failure: a half-applied
Permission change is not something to render optimistically.

-}

import Graphql.Http
import Graphql.Http.GraphqlError as GraphqlError
import Json.Decode as Decode
import Json.Encode as Encode


{-| The refused operation on an authorization failure. Structurally identical to
`Identity.ObjectAction`; kept here so the mapping stays free of Identity's dependencies.
-}
type alias ObjectAction =
    { namespace : String
    , object : String
    , action : String
    }


{-| What an authorization refusal refused.

`objectAction` is the precise answer and comes from the error's extensions. `field` is the
first segment of the GraphQL error path — `auth`, `self` — which says which top-level
field of a multi-field response was refused. Both are optional: a server that names
neither has still refused.

-}
type alias Refusal =
    { field : Maybe String
    , objectAction : Maybe ObjectAction
    }


{-| What a failed request means. Server outcomes come from `extensions.code`; the last
group is transport, which never reached bitmagnet's resolvers at all.
-}
type Failure
    = InvalidCredentials
    | UserDisabled
    | LoginThrottled
    | UserAlreadyExists
    | UsernameInvalid
    | InvitationRequired
    | InvitationInvalid
    | InvitationExpired
    | InvitationClaimed
    | PasswordInsufficientEntropy
    | Unauthorized Refusal
    | UserAuthenticationRequired
    | ApiKeyManagementForbidden
    | ServiceUnavailable
    | ServerFault
    | ServerRejected String
    | Unreachable
    | Timeout
    | BadStatus Int
    | BadUrl String
    | SchemaMismatch


fromError : Graphql.Http.Error a -> Failure
fromError error =
    case error of
        Graphql.Http.GraphqlError _ errors ->
            fromGraphqlErrors errors

        Graphql.Http.HttpError httpError ->
            case httpError of
                Graphql.Http.NetworkError ->
                    Unreachable

                Graphql.Http.Timeout ->
                    Timeout

                Graphql.Http.BadStatus metadata _ ->
                    BadStatus metadata.statusCode

                Graphql.Http.BadUrl url ->
                    BadUrl url

                Graphql.Http.BadPayload _ ->
                    SchemaMismatch


{-| One response can carry several errors — an authorization refusal on one field beside
an unrelated failure on another. The first recognized code wins, because a code is the
only part of a response that says what actually happened; an error Magnes cannot classify
only contributes its message as a fallback.
-}
fromGraphqlErrors : List GraphqlError.GraphqlError -> Failure
fromGraphqlErrors errors =
    case List.filterMap classify errors of
        recognized :: _ ->
            recognized

        [] ->
            case errors of
                first :: _ ->
                    ServerRejected first.message

                [] ->
                    ServerRejected "The server rejected the query."


classify : GraphqlError.GraphqlError -> Maybe Failure
classify error =
    decode codeDecoder error
        |> Maybe.andThen (\code -> fromCode code (refusal error))


refusal : GraphqlError.GraphqlError -> Refusal
refusal error =
    { field = decode fieldDecoder error
    , objectAction = decode objectActionDecoder error
    }


decode : Decode.Decoder a -> GraphqlError.GraphqlError -> Maybe a
decode decoder error =
    Encode.dict identity identity error.details
        |> Decode.decodeValue decoder
        |> Result.toMaybe


codeDecoder : Decode.Decoder String
codeDecoder =
    Decode.at [ "extensions", "code" ] Decode.string


{-| The path names the field that failed, deepest last; its first segment is the top-level
field. An error without a path is not unusual, so this stays optional.
-}
fieldDecoder : Decode.Decoder String
fieldDecoder =
    Decode.field "path" (Decode.index 0 Decode.string)


{-| Present only on an authorization refusal, and optional even there: a refusal whose
Object action the server could not name is still a refusal.
-}
objectActionDecoder : Decode.Decoder ObjectAction
objectActionDecoder =
    Decode.field "extensions"
        (Decode.map3 ObjectAction
            (Decode.field "namespace" Decode.string)
            (Decode.field "object" Decode.string)
            (Decode.field "action" Decode.string)
        )


fromCode : String -> Refusal -> Maybe Failure
fromCode code refused =
    case code of
        "INVALID_CREDENTIALS" ->
            Just InvalidCredentials

        "USER_DISABLED" ->
            Just UserDisabled

        "LOGIN_THROTTLED" ->
            Just LoginThrottled

        "USER_ALREADY_EXISTS" ->
            Just UserAlreadyExists

        "USERNAME_INVALID" ->
            Just UsernameInvalid

        "INVITATION_REQUIRED" ->
            Just InvitationRequired

        "INVITATION_INVALID" ->
            Just InvitationInvalid

        "INVITATION_EXPIRED" ->
            Just InvitationExpired

        "INVITATION_CLAIMED" ->
            Just InvitationClaimed

        "PASSWORD_INSUFFICIENT_ENTROPY" ->
            Just PasswordInsufficientEntropy

        "UNAUTHORIZED" ->
            Just (Unauthorized refused)

        "USER_SESSION_REQUIRED" ->
            Just UserAuthenticationRequired

        "API_KEY_MANAGEMENT_FORBIDDEN" ->
            Just ApiKeyManagementForbidden

        "AUTHENTICATION_INFRASTRUCTURE_FAILURE" ->
            Just ServiceUnavailable

        "INTERNAL_SERVER_ERROR" ->
            Just ServerFault

        _ ->
            Nothing


{-| Whether the failure says the Identity Magnes is holding is no longer the one the
server sees, and so should be refetched. A wrong password does not: it is a statement
about the credential just offered, not about the Identity in hand.
-}
isUnauthorized : Failure -> Bool
isUnauthorized failure =
    case failure of
        Unauthorized _ ->
            True

        UserAuthenticationRequired ->
            True

        _ ->
            False


toMessage : Failure -> String
toMessage failure =
    case failure of
        InvalidCredentials ->
            "That username and password do not match."

        UserDisabled ->
            "That account is disabled."

        LoginThrottled ->
            "Too many sign-in attempts. Wait a moment and try again."

        UserAlreadyExists ->
            "That username is already taken."

        UsernameInvalid ->
            "That username is not usable."

        InvitationRequired ->
            "This instance needs an invitation code to register."

        InvitationInvalid ->
            "That invitation code is not valid."

        InvitationExpired ->
            "That invitation code has expired."

        InvitationClaimed ->
            "That invitation code has already been used."

        PasswordInsufficientEntropy ->
            "That password is too easy to guess. Make it longer or less predictable."

        Unauthorized { objectAction } ->
            case objectAction of
                Just refused ->
                    "You are not allowed to "
                        ++ refused.action
                        ++ " "
                        ++ refused.object
                        ++ "."

                Nothing ->
                    "You are not allowed to do that."

        UserAuthenticationRequired ->
            "That needs you to be signed in."

        ApiKeyManagementForbidden ->
            "API keys cannot manage API keys."

        ServiceUnavailable ->
            "bitmagnet's authentication service is unavailable. Try again shortly."

        ServerFault ->
            "bitmagnet ran into a problem handling that."

        ServerRejected message ->
            message

        Unreachable ->
            "Could not reach bitmagnet."

        Timeout ->
            "bitmagnet took too long to answer."

        BadStatus statusCode ->
            "bitmagnet answered with status " ++ String.fromInt statusCode ++ "."

        BadUrl url ->
            "Not a usable bitmagnet URL: " ++ url

        SchemaMismatch ->
            "bitmagnet's answer did not match the schema Magnes was built against."
