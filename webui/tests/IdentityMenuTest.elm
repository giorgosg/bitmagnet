module IdentityMenuTest exposing (suite)

import Expect
import Html.Attributes
import Identity
import IdentityMenu
import Route
import Test exposing (Test, describe, test)
import Test.Html.Query as Query
import Test.Html.Selector as Selector
import Time


mount : Route.BasePath
mount =
    Route.basePath ""


user : String -> Identity.User
user username =
    { id = 1
    , username = username
    , role = "user"
    , email = Nothing
    , lastLoginAt = Nothing
    , createdAt = Time.millisToPosix 0
    , updatedAt = Time.millisToPosix 0
    }


ordinary : Identity.Identity
ordinary =
    Identity.UserAuthenticated (user "grace") [ Identity.graphql "torrentContent" "query" ]


administrator : Identity.Identity
administrator =
    Identity.UserAuthenticated (user "ada")
        [ Identity.graphql "auth" "query", Identity.graphql "auth" "mutate" ]


anonymous : Identity.Identity
anonymous =
    Identity.Anonymous [ Identity.graphql "torrentContent" "query" ]


messages : IdentityMenu.Messages ()
messages =
    { toggled = (), dismissed = (), signOutRequested = () }


rendered : Bool -> Identity.Identity -> Query.Single ()
rendered open identity =
    IdentityMenu.view mount messages identity (Route.Search Route.emptySearch) open
        |> Query.fromHtml


labels : Identity.Identity -> List String
labels identity =
    IdentityMenu.destinations mount identity |> List.map .label


suite : Test
suite =
    describe "IdentityMenu"
        [ describe "what it offers"
            [ test "an ordinary User is offered their own pages and nothing else" <|
                \_ ->
                    labels ordinary
                        |> Expect.equal [ "Your User", "API keys" ]
            , test "an administrator is offered the administration pages too" <|
                \_ ->
                    labels administrator
                        |> Expect.equal
                            [ "Your User", "API keys", "Users", "Invitations", "Roles" ]
            , test "the pages are the ones the guard admits, not a second opinion" <|
                \_ ->
                    -- Every destination offered must survive the guard that protects it,
                    -- or the menu would advertise a page that bounces on arrival.
                    IdentityMenu.destinations mount administrator
                        |> List.map (\entry -> Route.guard mount administrator entry.route)
                        |> Expect.equal (List.repeat 5 Route.Allowed)
            , test "an Anonymous Identity is offered a way in rather than a menu" <|
                \_ ->
                    rendered False anonymous
                        |> Expect.all
                            [ Query.has [ Selector.text "Sign in" ]
                            , Query.hasNot [ Selector.tag "button" ]
                            ]
            , test "signing in returns to where it was asked from" <|
                \_ ->
                    IdentityMenu.view mount messages anonymous (Route.Torrent "abc") False
                        |> Query.fromHtml
                        |> Query.has
                            [ Selector.attribute
                                (Html.Attributes.href "/login?returnUrl=%2Ftorrent%2Fabc")
                            ]
            ]
        , describe "the closed menu"
            [ test "is an icon that says whose it is" <|
                \_ ->
                    rendered False ordinary
                        |> Query.find [ Selector.class "identity-button" ]
                        |> Query.has
                            [ Selector.attribute (Html.Attributes.attribute "aria-label" "grace")
                            , Selector.attribute (Html.Attributes.attribute "aria-expanded" "false")
                            ]
            , test "offers no destinations until it is opened" <|
                \_ ->
                    rendered False administrator
                        |> Query.hasNot [ Selector.text "Invitations" ]
            ]
        , describe "the open menu"
            [ test "says it is open" <|
                \_ ->
                    rendered True ordinary
                        |> Query.find [ Selector.class "identity-button" ]
                        |> Query.has
                            [ Selector.attribute (Html.Attributes.attribute "aria-expanded" "true") ]
            , test "links to each page it offers" <|
                \_ ->
                    rendered True administrator
                        |> Query.has
                            [ Selector.attribute (Html.Attributes.href "/admin/invitations") ]
            , test "ends with signing out" <|
                \_ ->
                    -- Last, because it is the one entry that is not a page, and the one
                    -- nobody should reach by aiming at something else.
                    rendered True ordinary
                        |> Query.findAll [ Selector.tag "li" ]
                        |> Query.count (Expect.equal 3)
            , test "signing out is an action, not a link" <|
                \_ ->
                    rendered True ordinary
                        |> Query.find [ Selector.class "identity-signout" ]
                        |> Query.has [ Selector.tag "button", Selector.text "Sign out" ]
            ]
        , describe "an API-key Identity"
            [ test "is offered a way to sign in as a User, not that User's pages" <|
                \_ ->
                    -- It reports an owning User but is not a User-authenticated Identity,
                    -- and the guard refuses it those pages.
                    IdentityMenu.view mount
                        messages
                        (Identity.APIKeyAuthenticated (user "ada") "a key" [])
                        (Route.Search Route.emptySearch)
                        False
                        |> Query.fromHtml
                        |> Query.has [ Selector.text "Sign in" ]
            ]
        , describe "an unresolved Identity"
            [ test "draws nothing rather than flickering between two answers" <|
                \_ ->
                    rendered False Identity.Unknown
                        |> Query.children []
                        |> Query.count (Expect.equal 0)
            ]
        ]
