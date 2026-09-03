module ApiKeysTest exposing (suite)

import ApiKeys
import Expect
import Html.Attributes
import Identity
import Set
import Test exposing (Test, describe, test)
import Test.Html.Query as Query
import Test.Html.Selector as Selector
import Time


{-| The registry a real instance answers, cut to the cases the screen has to tell apart:
a browser action, a non-browser one, and one the ordinary User does not hold.
-}
registry : List Identity.ObjectAction
registry =
    [ Identity.graphql "torrentContent" "query"
    , Identity.graphql "auth" "query"
    , { namespace = "http", object = "import", action = "mutate" }
    , { namespace = "torznab", object = "torznab", action = "query" }
    ]


{-| An administrator: the Role's Permission is the bare wildcard, so nothing can be offered
without expanding it against the registry.
-}
administrator : Identity.Identity
administrator =
    Identity.UserAuthenticated
        { id = 1
        , username = "ada"
        , role = "admin"
        , email = Nothing
        , lastLoginAt = Nothing
        , createdAt = Time.millisToPosix 0
        , updatedAt = Time.millisToPosix 0
        }
        [ { namespace = "**", object = "**", action = "**" } ]


{-| An ordinary User: concrete Permissions, and none in the `auth` namespace, so they can
neither read the registry nor need it.
-}
ordinary : Identity.Identity
ordinary =
    Identity.UserAuthenticated
        { id = 2
        , username = "linus"
        , role = "user"
        , email = Nothing
        , lastLoginAt = Nothing
        , createdAt = Time.millisToPosix 0
        , updatedAt = Time.millisToPosix 0
        }
        [ Identity.graphql "torrentContent" "query"
        , Identity.graphql "torrent" "query"
        ]


messages : ApiKeys.Messages ()
messages =
    { nameChanged = always ()
    , expiryChosen = always ()
    , actionToggled = always ()
    , submitted = ()
    , revealDismissed = ()
    , revokeRequested = always ()
    , revokeConfirmed = always ()
    , revokeCancelled = ()
    }


zone : Time.Zone
zone =
    Time.utc


rendered : Identity.Identity -> ApiKeys.State -> Query.Single ()
rendered identity state =
    ApiKeys.view zone messages identity state
        |> Query.fromHtml


aKey : ApiKeys.Key
aKey =
    { id = 7
    , name = "ci-runner"
    , permissions =
        -- Two actions an admin holds; the ordinary User holds only the first, so the second
        -- is the one that shows as suspended for them.
        [ Identity.graphql "torrentContent" "query"
        , Identity.graphql "auth" "query"
        ]
    , expiresAt = Nothing
    , createdAt = Time.millisToPosix 0
    }


suite : Test
suite =
    describe "API-key management"
        [ describe "what may be put on a key"
            [ test "with the registry, the offer is what the Identity may do, expanded" <|
                \_ ->
                    -- The admin's own Permission is one wildcard; the offer is every
                    -- registered action, because the wildcard matches all of them.
                    ApiKeys.offerable administrator (Just registry)
                        |> Expect.equal registry
            , test "without the registry, the offer is the Identity's own concrete actions" <|
                \_ ->
                    ApiKeys.offerable ordinary Nothing
                        |> Expect.equal
                            [ Identity.graphql "torrentContent" "query"
                            , Identity.graphql "torrent" "query"
                            ]
            , test "a wildcard is never offered as a choice, even without the registry" <|
                \_ ->
                    -- createAPIKey refuses a wildcard, so offering one would be a checkbox
                    -- that can only produce a rejected key.
                    ApiKeys.offerable administrator Nothing
                        |> Expect.equal []
            , test "with the registry, an action the Identity lacks is not offered" <|
                \_ ->
                    ApiKeys.offerable ordinary (Just registry)
                        |> Expect.equal [ Identity.graphql "torrentContent" "query" ]
            ]
        , describe "whether the registry is worth fetching"
            [ test "a wildcard held by an auth-reader needs it" <|
                \_ ->
                    ApiKeys.needsRegistry administrator
                        |> Expect.equal True
            , test "concrete Permissions do not need it" <|
                \_ ->
                    ApiKeys.needsRegistry ordinary
                        |> Expect.equal False
            , test "a wildcard that cannot reach auth-query does not ask, because it could not read it" <|
                \_ ->
                    -- A narrower wildcard: it stands for actions (object is **) but does not
                    -- itself grant graphql::auth::query, so it can neither be expanded from
                    -- the registry nor fetch it. `graphql::**::query` would, and does.
                    Identity.UserAuthenticated
                        { id = 3
                        , username = "mallory"
                        , role = "odd"
                        , email = Nothing
                        , lastLoginAt = Nothing
                        , createdAt = Time.millisToPosix 0
                        , updatedAt = Time.millisToPosix 0
                        }
                        [ { namespace = "graphql", object = "torrent", action = "**" } ]
                        |> ApiKeys.needsRegistry
                        |> Expect.equal False
            ]
        , describe "a key's suspended Object actions"
            [ test "names the selected Object actions the owner can no longer exercise" <|
                \_ ->
                    -- The key was selected for two actions; the owner now holds only one of them.
                    ApiKeys.suspended ordinary aKey
                        |> Expect.equal [ Identity.graphql "auth" "query" ]
            , test "is empty when every selected Object action is still held" <|
                \_ ->
                    ApiKeys.suspended administrator aKey
                        |> Expect.equal []
            ]
        , describe "ticking an Object action"
            [ test "adds one that was not chosen" <|
                \_ ->
                    ApiKeys.empty
                        |> ApiKeys.toggle (Identity.graphql "torrent" "query")
                        |> .draft
                        |> .chosen
                        |> Set.member "graphql::torrent::query"
                        |> Expect.equal True
            , test "removes one that was" <|
                \_ ->
                    ApiKeys.empty
                        |> ApiKeys.toggle (Identity.graphql "torrent" "query")
                        |> ApiKeys.toggle (Identity.graphql "torrent" "query")
                        |> .draft
                        |> .chosen
                        |> Set.isEmpty
                        |> Expect.equal True
            ]
        , describe "the create form"
            [ test "offers a checkbox for each action the Identity may grant" <|
                \_ ->
                    loadedFor (Just registry)
                        |> rendered administrator
                        |> Query.findAll [ Selector.attribute (Html.Attributes.type_ "checkbox") ]
                        |> Query.count (Expect.equal (List.length registry))
            , test "cannot be submitted with no name and nothing ticked" <|
                \_ ->
                    loadedFor (Just registry)
                        |> rendered administrator
                        |> Query.find [ Selector.class "submit" ]
                        |> Query.has [ Selector.disabled True ]
            , test "can be submitted once a name and an action are present" <|
                \_ ->
                    loadedFor (Just registry)
                        |> withNamedAndChosen
                        |> rendered administrator
                        |> Query.find [ Selector.class "submit" ]
                        |> Query.has [ Selector.disabled False ]
            , test "says so when the Identity has nothing to grant" <|
                \_ ->
                    loadedFor Nothing
                        |> rendered emptyHanded
                        |> Query.has [ Selector.text "nothing a key of yours could be given" ]
            ]
        , describe "the created key's value"
            [ test "is shown once, with a warning that it will not be shown again" <|
                \_ ->
                    loadedFor (Just registry)
                        |> ApiKeys.withCreation (ApiKeys.Created { name = "ci-runner", value = "sk-abcdef" })
                        |> rendered administrator
                        |> Query.has [ Selector.text "sk-abcdef", Selector.text "only time it is shown" ]
            , test "is absent when no key has just been created" <|
                \_ ->
                    loadedFor (Just registry)
                        |> rendered administrator
                        |> Query.hasNot [ Selector.class "api-key-reveal" ]
            ]
        , describe "the listing"
            [ test "names each key and what it was selected for" <|
                \_ ->
                    loadedWith [ aKey ]
                        |> rendered administrator
                        |> Query.has
                            [ Selector.text "ci-runner"
                            , Selector.text "graphql::torrentContent::query"
                            ]
            , test "marks a selected Object action the owner's Role no longer grants" <|
                \_ ->
                    loadedWith [ aKey ]
                        |> rendered ordinary
                        |> Query.find [ Selector.class "api-key-action-suspended" ]
                        |> Query.has [ Selector.text "graphql::auth::query" ]
            , test "says when there are no keys" <|
                \_ ->
                    loadedWith []
                        |> rendered administrator
                        |> Query.has [ Selector.text "no API keys" ]
            ]
        , describe "revoking a key"
            [ test "asks before it acts" <|
                \_ ->
                    loadedWith [ aKey ]
                        |> ApiKeys.withConfirming (Just 7)
                        |> rendered administrator
                        |> Query.has [ Selector.text "stops working immediately" ]
            , test "an armed key is the only one that shows the ask" <|
                \_ ->
                    loadedWith [ aKey ]
                        |> rendered administrator
                        |> Query.hasNot [ Selector.text "stops working immediately" ]
            ]
        ]


loadedWith : List ApiKeys.Key -> ApiKeys.State
loadedWith keys =
    ApiKeys.empty |> ApiKeys.withListing (ApiKeys.Loaded keys)


loadedFor : Maybe (List Identity.ObjectAction) -> ApiKeys.State
loadedFor maybeRegistry =
    ApiKeys.empty
        |> ApiKeys.withListing (ApiKeys.Loaded [])
        |> ApiKeys.withRegistry maybeRegistry


withNamedAndChosen : ApiKeys.State -> ApiKeys.State
withNamedAndChosen state =
    state
        |> ApiKeys.withDraft (ApiKeys.withName "ci-runner" state.draft)
        |> ApiKeys.toggle (Identity.graphql "torrentContent" "query")


{-| An Identity that holds nothing, so there is nothing a key of theirs could carry.
-}
emptyHanded : Identity.Identity
emptyHanded =
    Identity.UserAuthenticated
        { id = 9
        , username = "nemo"
        , role = "user"
        , email = Nothing
        , lastLoginAt = Nothing
        , createdAt = Time.millisToPosix 0
        , updatedAt = Time.millisToPosix 0
        }
        []
