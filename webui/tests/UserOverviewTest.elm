module UserOverviewTest exposing (suite)

import ApiError
import Expect
import Html.Attributes
import Identity
import Test exposing (Test, describe, test)
import Test.Html.Query as Query
import Test.Html.Selector as Selector
import Time
import UserOverview


ada : Identity.User
ada =
    { id = 1
    , username = "ada"
    , role = "editor"
    , email = Just "ada@example.test"
    , lastLoginAt = Just (Time.millisToPosix 1787666587000)
    , createdAt = Time.millisToPosix 1700000000000
    , updatedAt = Time.millisToPosix 1787666587000
    }


messages : UserOverview.Messages ()
messages =
    { signOutRequested = () }


rendered : UserOverview.SignOut -> Identity.User -> Query.Single ()
rendered state user =
    UserOverview.view Time.utc messages state user
        |> Query.fromHtml


suite : Test
suite =
    describe "UserOverview"
        [ describe "who you are"
            [ test "names the User" <|
                \_ ->
                    rendered UserOverview.Ready ada
                        |> Query.has [ Selector.text "ada" ]
            , test "names the Role" <|
                \_ ->
                    rendered UserOverview.Ready ada
                        |> Query.has [ Selector.text "editor" ]
            , test "gives the last sign-in to the minute" <|
                \_ ->
                    rendered UserOverview.Ready ada
                        |> Query.has [ Selector.text "2026-08-25 14:03" ]
            , test "says so rather than inventing one when there is no last sign-in" <|
                \_ ->
                    rendered UserOverview.Ready { ada | lastLoginAt = Nothing }
                        |> Query.has [ Selector.text "Not recorded" ]
            , test "shows no email while verification is inert in bitmagnet" <|
                \_ ->
                    rendered UserOverview.Ready ada
                        |> Query.hasNot [ Selector.text "ada@example.test" ]
            ]
        , describe "no password section"
            -- bitmagnet has no password-change mutation, so there is nothing here to
            -- offer. A disabled control would promise one that does not exist.
            [ test "renders no password field" <|
                \_ ->
                    rendered UserOverview.Ready ada
                        |> Query.findAll [ Selector.attribute (Html.Attributes.type_ "password") ]
                        |> Query.count (Expect.equal 0)
            ]
        , describe "signing out"
            [ test "offers the action when nothing is in flight" <|
                \_ ->
                    rendered UserOverview.Ready ada
                        |> Query.find [ Selector.tag "button" ]
                        |> Query.has [ Selector.text "Sign out", Selector.disabled False ]
            , test "does not offer a second one while the first is in flight" <|
                \_ ->
                    rendered UserOverview.SigningOut ada
                        |> Query.find [ Selector.tag "button" ]
                        |> Query.has [ Selector.disabled True ]
            , test "a refusal is announced, and leaves the User signed in" <|
                \_ ->
                    rendered (UserOverview.Refused ApiError.Unreachable) ada
                        |> Expect.all
                            [ Query.has
                                [ Selector.attribute (Html.Attributes.attribute "role" "alert")
                                , Selector.text (ApiError.toMessage ApiError.Unreachable)
                                ]
                            , Query.find [ Selector.tag "button" ]
                                >> Query.has [ Selector.disabled False ]
                            ]
            ]
        ]
