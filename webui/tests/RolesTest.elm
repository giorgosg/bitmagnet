module RolesTest exposing (suite)

import ApiError
import Expect
import Html.Attributes
import Identity
import Roles
import Test exposing (Test, describe, test)
import Test.Html.Query as Query
import Test.Html.Selector as Selector
import Time


{-| The Object actions the server names. A real instance answers seventeen; these five
carry every case the screen has to tell apart.
-}
offered : List Identity.ObjectAction
offered =
    [ Identity.graphql "torrent" "query"
    , Identity.graphql "torrent" "mutate"
    , Identity.graphql "auth" "query"
    , { namespace = "http", object = "import", action = "mutate" }
    , { namespace = "torznab", object = "torznab", action = "query" }
    ]


{-| A stored Permission no checkbox can express. `putRole` accepts any triple, so one can
exist however the form would have written it.
-}
wildcard : Identity.ObjectAction
wildcard =
    { namespace = "http", object = "**", action = "**" }


{-| The `admin` Role as the server reports it: one in-memory Permission granting
everything, which no checkbox offers and `putRole` cannot store.
-}
administrator : Roles.Role
administrator =
    { name = "admin"
    , core = True
    , permissions = [ { objectAction = { namespace = "**", object = "**", action = "**" }, core = True } ]
    }


{-| A core Role whose grant is one the form does offer, so it can be seen ticked.
-}
reader : Roles.Role
reader =
    { name = "user"
    , core = True
    , permissions = [ { objectAction = Identity.graphql "torrent" "query", core = True } ]
    }


{-| Not core: it can be edited and deleted, and it holds one Permission the form shows
and one it cannot.
-}
curator : Roles.Role
curator =
    { name = "curator"
    , core = False
    , permissions =
        [ { objectAction = Identity.graphql "torrent" "query", core = False }
        , { objectAction = wildcard, core = False }
        ]
    }


page : Roles.Page
page =
    { roles = [ administrator, reader, curator ], actions = offered }


administratorIdentity : Identity.Identity
administratorIdentity =
    Identity.UserAuthenticated
        { id = 1
        , username = "ada"
        , role = "admin"
        , email = Nothing
        , lastLoginAt = Nothing
        , createdAt = Time.millisToPosix 0
        , updatedAt = Time.millisToPosix 0
        }
        [ Identity.graphql "auth" "query", Identity.graphql "auth" "mutate" ]


observer : Identity.Identity
observer =
    Identity.UserAuthenticated
        { id = 2
        , username = "linus"
        , role = "editor"
        , email = Nothing
        , lastLoginAt = Nothing
        , createdAt = Time.millisToPosix 0
        , updatedAt = Time.millisToPosix 0
        }
        [ Identity.graphql "auth" "query" ]


messages : Roles.Messages ()
messages =
    { nameChanged = always ()
    , actionToggled = always ()
    , submitted = ()
    , editRequested = always ()
    , editCancelled = ()
    , deleteRequested = always ()
    , deleteConfirmed = always ()
    , deleteCancelled = ()
    }


loaded : Roles.State
loaded =
    Roles.empty |> Roles.withListing (Roles.Loaded page)


draftOf : String -> Roles.Draft
draftOf name =
    Roles.draftNamed name page |> Maybe.withDefault Roles.newDraft


rendered : Identity.Identity -> Roles.State -> Query.Single ()
rendered identity state =
    Roles.view messages identity state
        |> Query.fromHtml


suite : Test
suite =
    describe "Role administration"
        [ describe "what a save would store"
            [ test "sends the Object actions that are ticked" <|
                \_ ->
                    -- `putRole` replaces the whole set, so this list is the Role.
                    Roles.desiredActions offered (draftOf "curator")
                        |> List.member (Identity.graphql "torrent" "query")
                        |> Expect.equal True
            , test "keeps a stored Permission the form cannot show" <|
                \_ ->
                    -- The acceptance criterion: an edit may not revoke a Permission
                    -- merely because no checkbox could express it.
                    Roles.desiredActions offered (draftOf "curator")
                        |> List.member wildcard
                        |> Expect.equal True
            , test "sends nothing for a Role whose only Permission is core" <|
                \_ ->
                    -- admin's `**` is held in memory, not stored. Sending it back would
                    -- write a row for something `putRole` can neither grant nor revoke.
                    Roles.desiredActions offered (draftOf "admin")
                        |> Expect.equal []
            , test "a Role being created starts with nothing" <|
                \_ ->
                    Roles.desiredActions offered Roles.newDraft
                        |> Expect.equal []
            , test "ticking an Object action twice leaves it as it was" <|
                \_ ->
                    Roles.newDraft
                        |> Roles.toggle (Identity.graphql "auth" "query")
                        |> Roles.toggle (Identity.graphql "auth" "query")
                        |> Roles.desiredActions offered
                        |> Expect.equal []
            , test "cannot send an Object action the server never named" <|
                \_ ->
                    -- bitmagnet stores whatever it is given, so a typo would become a
                    -- Permission that grants nothing and says nothing. The list the
                    -- server offered is the only source of what goes back.
                    Roles.newDraft
                        |> Roles.toggle { namespace = "nonsense", object = "nonsense", action = "**" }
                        |> Roles.desiredActions offered
                        |> Expect.equal []
            ]
        , describe "the editor"
            [ test "offers every Object action the server named" <|
                \_ ->
                    rendered administratorIdentity loaded
                        |> Query.findAll [ Selector.attribute (Html.Attributes.type_ "checkbox") ]
                        |> Query.count (Expect.equal (List.length offered))
            , test "sorts them under the namespace they belong to" <|
                \_ ->
                    rendered administratorIdentity loaded
                        |> Query.has
                            [ Selector.text "graphql", Selector.text "http", Selector.text "torznab" ]
            , test "editing a Role arrives with what it already holds ticked" <|
                \_ ->
                    loaded
                        |> Roles.withDraft (draftOf "curator")
                        |> rendered administratorIdentity
                        |> Query.findAll [ Selector.checked True ]
                        |> Query.count (Expect.equal 1)
            , test "a Permission the server holds in memory is ticked and cannot be untied" <|
                \_ ->
                    loaded
                        |> Roles.withDraft (draftOf "user")
                        |> rendered administratorIdentity
                        |> Query.findAll [ Selector.checked True, Selector.disabled True ]
                        |> Query.count (Expect.equal 1)
            , test "says why that one cannot be turned off" <|
                \_ ->
                    loaded
                        |> Roles.withDraft (draftOf "user")
                        |> rendered administratorIdentity
                        |> Query.has [ Selector.text "cannot be revoked" ]
            , test "names the Permissions it cannot show rather than hiding them" <|
                \_ ->
                    loaded
                        |> Roles.withDraft (draftOf "curator")
                        |> rendered administratorIdentity
                        |> Query.has [ Selector.text "http::**::**" ]
            , test "the name of a Role that exists cannot be retyped" <|
                \_ ->
                    -- `putRole` keys on the name: a changed one writes a second Role and
                    -- leaves the first, which is not what editing means.
                    loaded
                        |> Roles.withDraft (draftOf "curator")
                        |> rendered administratorIdentity
                        |> Query.hasNot [ Selector.attribute (Html.Attributes.type_ "text") ]
            , test "a Role being created is named by typing" <|
                \_ ->
                    rendered administratorIdentity loaded
                        |> Query.has [ Selector.attribute (Html.Attributes.type_ "text") ]
            ]
        , describe "the list"
            [ test "names every Role the server returned" <|
                \_ ->
                    rendered administratorIdentity loaded
                        |> Expect.all
                            [ Query.has [ Selector.text "admin" ]
                            , Query.has [ Selector.text "user" ]
                            , Query.has [ Selector.text "curator" ]
                            ]
            , test "says which Roles are core" <|
                \_ ->
                    rendered administratorIdentity loaded
                        |> Query.findAll [ Selector.class "role-core" ]
                        |> Query.count (Expect.equal 2)
            , test "a core Role offers no deletion" <|
                \_ ->
                    -- The server refuses it, so offering the click would only teach that
                    -- the screen does not know its own rules.
                    Roles.empty
                        |> Roles.withListing (Roles.Loaded { roles = [ administrator ], actions = offered })
                        |> rendered administratorIdentity
                        |> Query.hasNot [ Selector.tag "button", Selector.text "Delete" ]
            , test "a Role that is not core can be deleted" <|
                \_ ->
                    Roles.empty
                        |> Roles.withListing (Roles.Loaded { roles = [ curator ], actions = offered })
                        |> rendered administratorIdentity
                        |> Query.has [ Selector.tag "button", Selector.text "Delete" ]
            ]
        , describe "deleting"
            [ test "asks before doing it" <|
                \_ ->
                    loaded
                        |> Roles.withConfirming (Just "curator")
                        |> rendered administratorIdentity
                        |> Query.has [ Selector.text "Delete the Role curator?" ]
            , test "says the deletion will be refused while anyone holds the Role" <|
                \_ ->
                    -- `users.role_name` references `roles(name)` with no cascade, so
                    -- Postgres refuses and bitmagnet passes the refusal through as an
                    -- opaque database error. Better to say so before the click.
                    loaded
                        |> Roles.withConfirming (Just "curator")
                        |> rendered administratorIdentity
                        |> Query.has [ Selector.text "still holds it" ]
            , test "says the Invitations issued for it go too" <|
                \_ ->
                    -- `invitations.role_name` cascades, so unclaimed codes for this Role
                    -- disappear with it and nothing else would ever say so.
                    loaded
                        |> Roles.withConfirming (Just "curator")
                        |> rendered administratorIdentity
                        |> Query.has [ Selector.text "Invitation" ]
            , test "the ask can be declined" <|
                \_ ->
                    loaded
                        |> Roles.withConfirming (Just "curator")
                        |> rendered administratorIdentity
                        |> Query.has [ Selector.tag "button", Selector.text "Keep" ]
            ]
        , describe "what the Identity may do"
            [ test "an Identity that cannot mutate is offered no editor and no acts" <|
                \_ ->
                    rendered observer loaded
                        |> Expect.all
                            [ Query.hasNot [ Selector.tag "button" ]
                            , Query.hasNot [ Selector.tag "input" ]
                            ]
            , test "but still reads the Roles" <|
                \_ ->
                    rendered observer loaded
                        |> Query.has [ Selector.text "curator" ]
            ]
        , describe "an act in flight"
            [ test "nothing offers a second act while one is running" <|
                \_ ->
                    -- A save and a deletion both refetch the list, so two in flight
                    -- would race. Three Roles: two core with an Edit each, one with an
                    -- Edit and a Delete, and the form's own submit. All of them wait.
                    loaded
                        |> Roles.withSubmission Roles.Saving
                        |> rendered administratorIdentity
                        |> Expect.all
                            [ Query.findAll [ Selector.tag "button" ]
                                >> Query.count (Expect.equal 5)
                            , Query.findAll [ Selector.tag "button", Selector.disabled True ]
                                >> Query.count (Expect.equal 5)
                            ]
            , test "an armed deletion waits too, rather than being the one live pair" <|
                \_ ->
                    loaded
                        |> Roles.withConfirming (Just "curator")
                        |> Roles.withSubmission Roles.Saving
                        |> rendered administratorIdentity
                        |> Query.findAll [ Selector.tag "button", Selector.disabled True ]
                        |> Query.count (Expect.equal 5)
            ]
        , describe "a refused save"
            [ test "is announced rather than leaving the form looking saved" <|
                \_ ->
                    loaded
                        |> Roles.withSubmission (Roles.Rejected ApiError.Unreachable)
                        |> rendered administratorIdentity
                        |> Query.has
                            [ Selector.attribute (Html.Attributes.attribute "role" "alert")
                            , Selector.text (ApiError.toMessage ApiError.Unreachable)
                            ]
            ]
        ]
