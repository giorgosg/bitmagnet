module UsersTest exposing (suite)

import ApiError
import Expect
import Html.Attributes
import Identity
import Test exposing (Test, describe, test)
import Test.Html.Query as Query
import Test.Html.Selector as Selector
import Time
import Users


ada : Identity.User
ada =
    { id = 1
    , username = "ada"
    , role = "admin"
    , email = Nothing
    , lastLoginAt = Nothing
    , createdAt = Time.millisToPosix 0
    , updatedAt = Time.millisToPosix 0
    }


grace : Identity.User
grace =
    { ada | id = 2, username = "grace", role = "user" }


administrator : Identity.Identity
administrator =
    Identity.UserAuthenticated
        ada
        [ Identity.graphql "auth" "query", Identity.graphql "auth" "mutate" ]


observer : Identity.Identity
observer =
    Identity.UserAuthenticated
        { id = 3
        , username = "linus"
        , role = "editor"
        , email = Nothing
        , lastLoginAt = Nothing
        , createdAt = Time.millisToPosix 0
        , updatedAt = Time.millisToPosix 0
        }
        [ Identity.graphql "auth" "query" ]


messages : Users.Messages ()
messages =
    { queryChanged = always ()
    , pageRequested = always ()
    , roleChosen = \_ _ -> ()
    , disableRequested = always ()
    , enableRequested = always ()
    , deleteRequested = always ()
    , confirmed = ()
    , cancelled = ()
    }


loaded : List Identity.User -> Users.State
loaded users =
    Users.empty
        |> Users.withListing
            (Users.Loaded
                { users = users
                , totalCount = List.length users
                , roles = [ "admin", "editor", "user" ]
                }
            )


rendered : Identity.Identity -> Users.State -> Query.Single ()
rendered identity state =
    Users.view Time.utc messages identity state
        |> Query.fromHtml


suite : Test
suite =
    describe "User administration"
        [ describe "the list"
            [ test "names the Users the server listed" <|
                \_ ->
                    rendered administrator (loaded [ ada, grace ])
                        |> Query.has [ Selector.text "ada", Selector.text "grace" ]
            , test "a row says which Role its User holds" <|
                \_ ->
                    rendered administrator (loaded [ grace ])
                        |> Query.has [ Selector.text "user" ]
            , test "a User who has never signed in says so rather than showing a blank" <|
                \_ ->
                    -- bitmagnet leaves `lastLoginAt` null for such a User; the date the
                    -- User was created would be a lie in that slot.
                    rendered administrator (loaded [ grace ])
                        |> Query.has [ Selector.text "Not recorded" ]
            , test "claims nothing about who is currently disabled" <|
                \_ ->
                    -- The `User` schema carries no `enabled` field, so any status here
                    -- would be invented. Acceptance criterion: the UI does not invent one.
                    rendered administrator (loaded [ grace ])
                        |> Expect.all
                            [ Query.hasNot [ Selector.text "Enabled" ]
                            , Query.hasNot [ Selector.text "Disabled" ]
                            ]
            , test "says why no enabled state is shown" <|
                \_ ->
                    rendered administrator (loaded [ grace ])
                        |> Query.has [ Selector.text "does not report" ]
            ]
        , describe "what the Identity may do"
            [ test "an Identity that can mutate is offered the acts" <|
                \_ ->
                    rendered administrator (loaded [ grace ])
                        |> Expect.all
                            [ Query.has [ Selector.tag "button", Selector.text "Disable" ]
                            , Query.has [ Selector.tag "button", Selector.text "Enable" ]
                            , Query.has [ Selector.tag "button", Selector.text "Delete" ]
                            , Query.has [ Selector.tag "select" ]
                            ]
            , test "the Roles offered are the ones the server named" <|
                \_ ->
                    -- As with Invitations: a Role added on the server is offered without
                    -- a client change, and one that does not exist cannot be chosen.
                    rendered administrator (loaded [ grace ])
                        |> Query.findAll [ Selector.tag "option" ]
                        |> Query.count (Expect.equal 3)
            , test "an Identity that cannot mutate is offered none of it" <|
                \_ ->
                    rendered observer (loaded [ grace ])
                        |> Expect.all
                            [ Query.hasNot [ Selector.tag "button" ]
                            , Query.hasNot [ Selector.tag "select" ]
                            ]
            ]
        , describe "deleting"
            [ test "asks before doing it" <|
                \_ ->
                    -- One click arms it; the destructive click is the second one.
                    loaded [ grace ]
                        |> Users.withConfirming (Just (Users.Delete 2))
                        |> rendered administrator
                        |> Query.has [ Selector.text "Delete User grace?" ]
            , test "the ask can be declined" <|
                \_ ->
                    loaded [ grace ]
                        |> Users.withConfirming (Just (Users.Delete 2))
                        |> rendered administrator
                        |> Query.has [ Selector.tag "button", Selector.text "Keep" ]
            , test "arms only the User it was asked about" <|
                \_ ->
                    loaded [ ada, grace ]
                        |> Users.withConfirming (Just (Users.Delete 2))
                        |> rendered administrator
                        |> Query.findAll [ Selector.text "Delete User grace?" ]
                        |> Query.count (Expect.equal 1)
            , test "removing yourself is named as removing yourself" <|
                \_ ->
                    -- The spec requires a clear warning before a self-affecting mutation.
                    loaded [ ada ]
                        |> Users.withConfirming (Just (Users.Delete 1))
                        |> rendered administrator
                        |> Query.has [ Selector.text "your own User" ]
            ]
        , describe "disabling"
            [ test "asks before doing it" <|
                \_ ->
                    loaded [ grace ]
                        |> Users.withConfirming (Just (Users.Disable 2))
                        |> rendered administrator
                        |> Query.has [ Selector.text "Disable grace?" ]
            , test "says the disable lasts until someone enables them again" <|
                \_ ->
                    loaded [ grace ]
                        |> Users.withConfirming (Just (Users.Disable 2))
                        |> rendered administrator
                        |> Query.has [ Selector.text "until someone enables them again" ]
            , test "disabling yourself says you cannot sign back in" <|
                \_ ->
                    -- The one lockout bitmagnet does not prevent: no one may be left
                    -- with the authority to undo it.
                    loaded [ ada ]
                        |> Users.withConfirming (Just (Users.Disable 1))
                        |> rendered administrator
                        |> Query.has [ Selector.text "cannot sign back in" ]
            ]
        , describe "enabling"
            [ test "asks before doing it, like every other act" <|
                \_ ->
                    -- The ticket asks that enabling be deliberate too, even though it
                    -- grants rather than takes.
                    loaded [ grace ]
                        |> Users.withConfirming (Just (Users.Enable 2))
                        |> rendered administrator
                        |> Query.has [ Selector.text "Enable grace?" ]
            , test "says what it grants without claiming to know they were disabled" <|
                \_ ->
                    -- No row can know who is disabled, so the ask must not imply it.
                    loaded [ grace ]
                        |> Users.withConfirming (Just (Users.Enable 2))
                        |> rendered administrator
                        |> Query.has [ Selector.text "If they were disabled" ]
            ]
        , describe "changing your own Role"
            [ test "names the Role it would apply" <|
                \_ ->
                    loaded [ ada ]
                        |> Users.withConfirming (Just (Users.ChangeOwnRole { userId = 1, role = "user" }))
                        |> rendered administrator
                        |> Query.has [ Selector.text "Change your own Role to user?" ]
            ]
        , describe "a refused act"
            [ test "is announced rather than leaving the row unchanged in silence" <|
                \_ ->
                    loaded [ grace ]
                        |> Users.withAction (Users.Refused ApiError.Unreachable)
                        |> rendered administrator
                        |> Query.has
                            [ Selector.attribute (Html.Attributes.attribute "role" "alert")
                            , Selector.text (ApiError.toMessage ApiError.Unreachable)
                            ]
            ]
        , describe "searching"
            [ test "a changed search reads from the first page again" <|
                \_ ->
                    -- Page three of "all Users" is not page three of "Users called ada";
                    -- keeping the offset would show a page the new search never had.
                    Users.empty
                        |> Users.withOffset 100
                        |> Users.withQuery "ada"
                        |> .offset
                        |> Expect.equal 0
            , test "the query being searched is on display in the box" <|
                \_ ->
                    loaded [ grace ]
                        |> Users.withQuery "ada"
                        |> rendered administrator
                        |> Query.find [ Selector.tag "input" ]
                        |> Query.has [ Selector.attribute (Html.Attributes.value "ada") ]
            , test "a search that finds nothing says so, not that there are no Users" <|
                \_ ->
                    loaded []
                        |> Users.withQuery "zzz"
                        |> rendered administrator
                        |> Query.has [ Selector.text "No User matches" ]
            ]
        , describe "paging"
            [ test "asks for the next page by offset" <|
                \_ ->
                    Users.nextOffset { offset = 0, totalCount = 120 }
                        |> Expect.equal (Just Users.pageSize)
            , test "there is no page after the last one" <|
                \_ ->
                    Users.nextOffset { offset = 100, totalCount = 120 }
                        |> Expect.equal Nothing
            , test "the previous page is the one before this" <|
                \_ ->
                    Users.previousOffset 100
                        |> Expect.equal (Just 50)
            , test "there is nothing before the first page" <|
                \_ ->
                    Users.previousOffset 0
                        |> Expect.equal Nothing
            ]
        , describe "an act in flight"
            [ test "no row offers another act while one is running" <|
                \_ ->
                    -- The state can hold one act; a second click while it runs is either
                    -- a queue nobody promised or a silent nothing. Two rows, four
                    -- controls each: all of them wait.
                    loaded [ ada, grace ]
                        |> Users.withAction Users.Working
                        |> rendered administrator
                        |> Query.findAll [ Selector.disabled True ]
                        |> Query.count (Expect.equal 8)
            , test "an armed ask waits too, rather than being the one live pair" <|
                \_ ->
                    -- An ask armed while the screen was quiet outlives the act that
                    -- started on another row. Both its buttons are clicks the state
                    -- could not place: confirming would put a second act in flight, and
                    -- declining would clear the action the running one is still using.
                    loaded [ ada, grace ]
                        |> Users.withConfirming (Just (Users.Delete 2))
                        |> Users.withAction Users.Working
                        |> rendered administrator
                        |> Expect.all
                            [ Query.findAll [ Selector.tag "button" ]
                                >> Query.count (Expect.equal 5)
                            , Query.findAll [ Selector.tag "button", Selector.disabled True ]
                                >> Query.count (Expect.equal 5)
                            ]
            ]
        ]
