module ModerationTest exposing (suite)

import Expect
import Identity
import Magnes.Api.Enum.ModerationStatus as Status
import Moderation
import Route
import Test exposing (Test, describe, test)
import Time
import Url


suite : Test
suite =
    describe "private moderation"
        [ test "search and record links round trip beneath the mount" <|
            \_ ->
                let
                    mount =
                        Route.basePath "/magnes"

                    route =
                        Route.Moderation { status = Status.Allowed, query = "full/path manual.pdf", after = Just "opaque+/cursor=", identifier = Just (String.repeat 64 "a"), uncertainFlag = Just (String.repeat 40 "b") }
                in
                Route.toHref mount route
                    |> (\path -> Url.fromString ("https://example.test" ++ path))
                    |> Maybe.map (Route.fromUrl mount)
                    |> Expect.equal (Just route)
        , test "uncertain flag context changes the warning without asking a different private question" <|
            \_ ->
                let
                    params =
                        Route.emptyModeration
                in
                Route.sameQuestion (Route.Moderation params) (Route.Moderation { params | uncertainFlag = Just (String.repeat 40 "b") })
                    |> Expect.equal True
        , test "catalogue reader and editor Permissions cannot enter private review" <|
            \_ ->
                [ Identity.Anonymous [ Identity.graphql "torrentContent" "query" ]
                , Identity.UserAuthenticated user [ Identity.graphql "torrent" "query", Identity.graphql "torrent" "mutate" ]
                ]
                    |> List.map (\identity -> Route.guard (Route.basePath "") identity (Route.Moderation Route.emptyModeration))
                    |> Expect.equal (List.repeat 2 (Route.Refused "Your Identity does not permit private moderation review."))
        , test "private query Permission admits reading without mutate" <|
            \_ ->
                Route.guard (Route.basePath "") reader (Route.Moderation Route.emptyModeration)
                    |> Expect.equal Route.Allowed
        , test "a read-only reviewer cannot arm a decision" <|
            \_ ->
                loaded
                    |> Moderation.update "/graphql" reader (Moderation.Ask Moderation.Allow)
                    |> Tuple.first
                    |> .confirmation
                    |> Expect.equal Nothing
        , test "confirming an allow keeps pending visible until the server answers" <|
            \_ ->
                { loaded | confirmation = Just Moderation.Allow }
                    |> Moderation.update "/graphql" moderator Moderation.Confirm
                    |> Tuple.first
                    |> (\state -> ( state.working, Maybe.map .status state.record, state.notice ))
                    |> Expect.equal ( True, Just Status.Pending, Nothing )
        ]


user : Identity.User
user =
    { id = 1, username = "synthetic", role = "moderator", email = Nothing, lastLoginAt = Nothing, createdAt = Time.millisToPosix 0, updatedAt = Time.millisToPosix 0 }


reader : Identity.Identity
reader =
    Identity.UserAuthenticated user [ Identity.graphql "moderation" "query" ]


moderator : Identity.Identity
moderator =
    Identity.UserAuthenticated user [ Identity.graphql "moderation" "query", Identity.graphql "moderation" "mutate" ]


loaded : Moderation.State
loaded =
    let
        initial =
            Moderation.empty Route.emptyModeration
    in
    { initial | loading = False, record = Just { identifier = String.repeat 64 "a", status = Status.Pending, origin = "automatic", importedOrigin = Nothing, source = Nothing, revision = "2026-10-14T00:00:00.123456Z", updatedAt = Time.millisToPosix 0, evidence = Nothing, history = { items = [], next = Nothing } } }
