module RouteTest exposing (suite)

import Expect
import Identity
import Route
import Test exposing (Test, describe, test)
import Time
import Url


suite : Test
suite =
    describe "Identity routes"
        [ describe "round trips beneath the configured base path"
            (List.map roundTripTest routes)
        , test "an Anonymous Identity is sent to login with the protected URL" <|
            \_ ->
                Route.guard mount (Identity.Anonymous []) Route.APIKeys
                    |> Expect.equal
                        (Route.RedirectTo
                            (Route.Login { returnUrl = Just "/magnes/account/api-keys" })
                        )
        , test "a User-authenticated Identity is sent away from login" <|
            \_ ->
                Route.guard mount userIdentity (Route.Login { returnUrl = Nothing })
                    |> Expect.equal (Route.RedirectTo Route.UserOverview)
        , test "administration refuses a User without auth query permission" <|
            \_ ->
                Route.guard mount userIdentity Route.AdminUsers
                    |> Expect.equal (Route.Refused "Your Identity does not permit administration.")
        , test "administration allows the admin wildcard" <|
            \_ ->
                Route.guard mount adminIdentity Route.AdminRoles
                    |> Expect.equal Route.Allowed
        , test "Unknown waits and bootstrap failure remains a refusal" <|
            \_ ->
                ( Route.guard mount Identity.Unknown Route.UserOverview
                , Route.guard mount (Identity.Failed "offline") Route.UserOverview
                )
                    |> Expect.equal
                        ( Route.PendingIdentity, Route.Refused "offline" )
        ]


mount : Route.BasePath
mount =
    Route.basePath "/magnes"


routes : List Route.Route
routes =
    [ Route.Login { returnUrl = Just "/magnes/account/api-keys" }
    , Route.Register { code = Just "invitation code" }
    , Route.UserOverview
    , Route.APIKeys
    , Route.AdminUsers
    , Route.AdminRoles
    , Route.AdminInvitations
    ]


roundTripTest : Route.Route -> Test
roundTripTest route =
    test (Route.toHref mount route) <|
        \_ ->
            Route.toHref mount route
                |> (\href -> Url.fromString ("https://example.test" ++ href))
                |> Maybe.map (Route.fromUrl mount)
                |> Expect.equal (Just route)


userIdentity : Identity.Identity
userIdentity =
    Identity.UserAuthenticated user []


adminIdentity : Identity.Identity
adminIdentity =
    Identity.UserAuthenticated user
        [ { namespace = "**", object = "**", action = "**" } ]


user : Identity.User
user =
    { id = 1
    , username = "user"
    , role = "user"
    , email = Nothing
    , lastLoginAt = Nothing
    , createdAt = Time.millisToPosix 0
    , updatedAt = Time.millisToPosix 0
    }
