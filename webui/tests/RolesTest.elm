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


{-| `anon`: core, and the one Role the Anonymous access setting governs.
-}
anonymous : Roles.Role
anonymous =
    { name = "anon"
    , core = True
    , permissions = [ { objectAction = Identity.graphql "self" "query", core = True } ]
    }


page : Roles.Page
page =
    { roles = [ administrator, reader, curator, anonymous ], actions = offered }


{-| Someone administering while holding a Role this screen can write — which is what makes
saving it self-affecting.
-}
curatorIdentity : Identity.Identity
curatorIdentity =
    Identity.UserAuthenticated
        { id = 4
        , username = "grace"
        , role = "curator"
        , email = Nothing
        , lastLoginAt = Nothing
        , createdAt = Time.millisToPosix 0
        , updatedAt = Time.millisToPosix 0
        }
        [ Identity.graphql "auth" "query", Identity.graphql "auth" "mutate" ]


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
    , confirmed = ()
    , cancelled = ()
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
            , test "anon keeps pre-existing auth Permissions as carried, never as boxes" <|
                \_ ->
                    let
                        listing =
                            { roles = [ { anonymous | permissions = [ { objectAction = Identity.graphql "auth" "query", core = False } ] } ], actions = offered }

                        draft =
                            Roles.draftNamed "anon" listing |> Maybe.withDefault Roles.newDraft
                    in
                    Roles.empty
                        |> Roles.withListing (Roles.Loaded listing)
                        |> Roles.withDraft draft
                        |> rendered administratorIdentity
                        |> Expect.all
                            [ Query.has [ Selector.text "graphql::auth::query" ]
                            , Query.has [ Selector.text "Saving preserves this access" ]
                            , Query.hasNot [ Selector.tag "label", Selector.text "auth::query" ]
                            , always (Expect.equal [ Identity.graphql "auth" "query" ] (Roles.desiredActions offered draft))
                            ]
            , test "naming a new draft anon cannot add auth Permissions" <|
                \_ ->
                    Roles.newDraft
                        |> Roles.toggle (Identity.graphql "auth" "query")
                        |> Roles.withName " anon "
                        |> Roles.desiredActions offered
                        |> Expect.equal []
            , test "anon cannot select any auth action or a wildcard granting auth" <|
                \_ ->
                    let
                        forbidden =
                            [ Identity.graphql "auth" "mutate", Identity.graphql "auth" "delete", { namespace = "**", object = "**", action = "**" } ]

                        draft =
                            List.foldl Roles.toggle (draftOf "anon") forbidden
                    in
                    Roles.desiredActions (forbidden ++ offered) draft |> Expect.equal []
            , test "a carried wildcard granting auth is named and warned about" <|
                \_ ->
                    let
                        draft =
                            draftOf "anon"
                    in
                    loaded
                        |> Roles.withDraft { draft | carried = [ { namespace = "**", object = "**", action = "**" } ] }
                        |> rendered administratorIdentity
                        |> Expect.all
                            [ Query.has [ Selector.text "**::**::**" ]
                            , Query.has [ Selector.text "Saving preserves this access" ]
                            ]
            , test "typing admin into a create draft still leaves it read-only" <|
                \_ ->
                    Roles.newDraft |> Roles.withName " admin " |> Roles.writable |> Expect.equal False
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
            , test "names the fixed Permissions rather than only counting them" <|
                \_ ->
                    -- admin's is `**::**::**`, which no checkbox draws, so a bare count
                    -- would leave the Role that holds everything looking like it holds
                    -- nothing.
                    loaded
                        |> Roles.withDraft (draftOf "admin")
                        |> rendered administratorIdentity
                        |> Query.has [ Selector.text "**::**::**" ]
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
                    -- admin, user and anon; curator is the instance's own.
                    rendered administratorIdentity loaded
                        |> Query.findAll [ Selector.class "role-core" ]
                        |> Query.count (Expect.equal 3)
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
                        |> Roles.withConfirming (Just (Roles.Deleting "curator"))
                        |> rendered administratorIdentity
                        |> Query.has [ Selector.text "Delete the Role curator?" ]
            , test "says the deletion will be refused while anyone holds the Role" <|
                \_ ->
                    -- `users.role_name` references `roles(name)` with no cascade, so
                    -- Postgres refuses and bitmagnet passes the refusal through as an
                    -- opaque database error. Better to say so before the click.
                    loaded
                        |> Roles.withConfirming (Just (Roles.Deleting "curator"))
                        |> rendered administratorIdentity
                        |> Query.has [ Selector.text "still holds it" ]
            , test "says every Invitation issued for it goes too, claimed or not" <|
                \_ ->
                    -- `invitations.role_name` cascades with no claimed/unclaimed
                    -- distinction, so saying "unclaimed" would understate it.
                    loaded
                        |> Roles.withConfirming (Just (Roles.Deleting "curator"))
                        |> rendered administratorIdentity
                        |> Query.has [ Selector.text "claimed or not" ]
            , test "the ask can be declined" <|
                \_ ->
                    loaded
                        |> Roles.withConfirming (Just (Roles.Deleting "curator"))
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
                    -- A save and a deletion both refetch the list, so two in flight would
                    -- race. Four Roles: a View for admin, an Edit each for anon and user,
                    -- an Edit and a Delete for curator, and the form's own submit. All of
                    -- them wait.
                    loaded
                        |> Roles.withSubmission Roles.Saving
                        |> rendered administratorIdentity
                        |> Expect.all
                            [ Query.findAll [ Selector.tag "button" ]
                                >> Query.count (Expect.equal 6)
                            , Query.findAll [ Selector.tag "button", Selector.disabled True ]
                                >> Query.count (Expect.equal 6)
                            ]
            , test "an armed deletion waits too, rather than being the one live pair" <|
                \_ ->
                    -- curator's row is now its ask, so its Delete and Keep stand in for
                    -- the Edit and Delete it had: six either way, and none of them live.
                    loaded
                        |> Roles.withConfirming (Just (Roles.Deleting "curator"))
                        |> Roles.withSubmission Roles.Saving
                        |> rendered administratorIdentity
                        |> Query.findAll [ Selector.tag "button", Selector.disabled True ]
                        |> Query.count (Expect.equal 6)
            ]
        , describe "the core Roles"
            [ test "admin is opened to be read, not edited" <|
                \_ ->
                    -- The spec: "`admin` remains fixed at its wildcard Permission". A save
                    -- would write rows beside a Permission held in memory and change
                    -- nothing, so there is nothing to press.
                    loaded
                        |> Roles.withDraft (draftOf "admin")
                        |> rendered administratorIdentity
                        |> Expect.all
                            [ Query.hasNot [ Selector.class "submit" ]
                            , Query.has [ Selector.text "fixes admin at its wildcard Permission" ]
                            ]
            , test "anon offers an editable save and explains its setting dependency" <|
                \_ ->
                    loaded
                        |> Roles.withDraft (draftOf "anon")
                        |> rendered administratorIdentity
                        |> Expect.all
                            [ Query.has [ Selector.class "submit" ]
                            , Query.has [ Selector.text "auth.anonymous_access" ]
                            , Query.has [ Selector.text "Magnes cannot see whether" ]
                            ]
            , test "anon omits auth actions and explains why" <|
                \_ ->
                    loaded
                        |> Roles.withDraft (draftOf "anon")
                        |> rendered administratorIdentity
                        |> Expect.all
                            [ Query.hasNot [ Selector.tag "label", Selector.text "auth::query" ]
                            , Query.has [ Selector.text "hand the instance away" ]
                            , Query.findAll [ Selector.attribute (Html.Attributes.type_ "checkbox"), Selector.disabled False ]
                                >> Query.count (Expect.equal 4)
                            ]
            , test "its row offers a read rather than an edit" <|
                \_ ->
                    Roles.empty
                        |> Roles.withListing (Roles.Loaded { roles = [ administrator ], actions = offered })
                        |> rendered administratorIdentity
                        |> Expect.all
                            [ Query.has [ Selector.tag "button", Selector.text "View" ]
                            , Query.hasNot [ Selector.tag "button", Selector.text "Edit" ]
                            ]
            , test "a core Role the spec does allow assigning to is still edited" <|
                \_ ->
                    -- "Additional Permissions may be assigned to the `editor` and `user`
                    -- Roles" — core is not the same question as writable.
                    Roles.empty
                        |> Roles.withListing (Roles.Loaded { roles = [ reader ], actions = offered })
                        |> rendered administratorIdentity
                        |> Query.has [ Selector.tag "button", Selector.text "Edit" ]
            ]
        , describe "saving the Role you hold yourself"
            [ test "asks before doing it" <|
                \_ ->
                    -- The spec requires a clear warning before a self-affecting or
                    -- potentially locking mutation, and bitmagnet prevents neither.
                    loaded
                        |> Roles.withDraft (draftOf "curator")
                        |> Roles.withConfirming (Just (Roles.SavingOwn "curator"))
                        |> rendered curatorIdentity
                        |> Query.has [ Selector.text "Save your own Role, curator" ]
            , test "says what is lost when administration is not among the ticks" <|
                \_ ->
                    -- curator holds no `auth::mutate`, so saving it as drawn takes this
                    -- very screen away from the person pressing the button.
                    loaded
                        |> Roles.withDraft (draftOf "curator")
                        |> Roles.withConfirming (Just (Roles.SavingOwn "curator"))
                        |> rendered curatorIdentity
                        |> Query.has [ Selector.text "only another administrator could give it back" ]
            , test "says it takes effect on you when administration is kept" <|
                \_ ->
                    loaded
                        |> Roles.withDraft (Roles.toggle (Identity.graphql "auth" "mutate") (draftOf "curator"))
                        |> Roles.withConfirming (Just (Roles.SavingOwn "curator"))
                        |> rendered curatorIdentity
                        |> Query.has [ Selector.text "takes effect on you" ]
            , test "the ask can be declined" <|
                \_ ->
                    loaded
                        |> Roles.withDraft (draftOf "curator")
                        |> Roles.withConfirming (Just (Roles.SavingOwn "curator"))
                        |> rendered curatorIdentity
                        |> Query.has [ Selector.tag "button", Selector.text "Keep" ]
            , test "someone else's Role is saved without an ask" <|
                \_ ->
                    loaded
                        |> Roles.withDraft (draftOf "curator")
                        |> rendered administratorIdentity
                        |> Query.has [ Selector.class "submit" ]
            ]
        , describe "Anonymous writes"
            [ test "read-only anon changes do not require confirmation" <|
                \_ ->
                    Roles.confirmationFor offered Nothing (Roles.toggle (Identity.graphql "torrent" "query") (draftOf "anon"))
                        |> Expect.equal Nothing
            , test "every anon mutate and delete save requires confirmation" <|
                \_ ->
                    [ Identity.graphql "torrent" "mutate", Identity.graphql "torrent" "delete", { namespace = "http", object = "import", action = "mutate" } ]
                        |> List.map (\action -> Roles.confirmationFor (action :: offered) Nothing (Roles.toggle action (draftOf "anon")))
                        |> Expect.equal [ Just Roles.SavingAnonymous, Just Roles.SavingAnonymous, Just Roles.SavingAnonymous ]
            , test "carried write wildcards also require confirmation" <|
                \_ ->
                    let
                        draft =
                            draftOf "anon"
                    in
                    Roles.confirmationFor offered Nothing { draft | carried = [ wildcard ] }
                        |> Expect.equal (Just Roles.SavingAnonymous)
            , test "confirmation names the cross-origin risk and offers an explicit choice" <|
                \_ ->
                    loaded
                        |> Roles.withDraft (Roles.toggle (Identity.graphql "torrent" "mutate") (draftOf "anon"))
                        |> Roles.withConfirming (Just Roles.SavingAnonymous)
                        |> rendered administratorIdentity
                        |> Expect.all
                            [ Query.has [ Selector.attribute (Html.Attributes.attribute "role" "alert"), Selector.text "cross-origin" ]
                            , Query.has [ Selector.text "CORS defaults to *" ]
                            , Query.has [ Selector.tag "button", Selector.text "Allow Anonymous writes" ]
                            , Query.has [ Selector.tag "button", Selector.text "Keep" ]
                            , Query.hasNot [ Selector.class "submit" ]
                            ]
            , test "editing a confirmed draft requires a new decision" <|
                \_ ->
                    loaded
                        |> Roles.withConfirming (Just (Roles.SavingOwn "curator"))
                        |> Roles.withDraft (draftOf "anon")
                        |> .confirming
                        |> Expect.equal Nothing
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
