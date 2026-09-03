module ApiKeys exposing
    ( Creation(..)
    , Draft
    , Key
    , Listing(..)
    , Messages
    , Revealed
    , State
    , create
    , delete
    , empty
    , fetch
    , fetchRegistry
    , needsRegistry
    , newDraft
    , offerable
    , suspended
    , toggle
    , view
    , withConfirming
    , withCreation
    , withDraft
    , withExpiry
    , withListing
    , withName
    , withRegistry
    , withRevocation
    )

{-| A User's own API keys: what they may do, how long they last, and taking them back.

Three things about bitmagnet shape this screen, and none of them are obvious from the
schema alone.

**The key's value exists once.** `createAPIKey` returns it in its response and nothing
ever returns it again — `self.apiKeys` has no field for it, by design. So the reveal is not
a detail of the create form, it is the only moment the thing the User came for exists, and
losing it means making another key. It is modelled as its own state rather than as a
message beside the form.

(The one-time value has no canonical term in `CONTEXT.md`: both it and the record are
"API key". It is called `value` here rather than a term the glossary tells us to avoid; the
gap is worth a glossary entry, noted for a domain-modeling pass.)

**A key's Permissions are fixed, but its authority is not.** `apiKey.permissions` is what
the key was selected for when it was made, and it never moves. What the key can actually do is
that selection narrowed by its owner's Role, right now — so a Role that loses an action
suspends the matching half of every key that named it, and regaining the action restores
it. Both halves are shown, because a key that lists an action it cannot exercise is
otherwise indistinguishable from one that works.

**The choice has to be concrete.** `createAPIKey` validates each Object action against
bitmagnet's registry by exact membership, so a wildcard is refused — `admin`'s Role
Permission is literally `**::**::**`, which is a grant and not a choosable action. The
offer therefore has to be the registry filtered by what the User holds, and getting the
registry is itself permissioned. See `offerable`.

-}

import ApiError
import Bitmagnet
import Expiry
import Format
import Graphql.Http
import Graphql.Operation exposing (RootMutation)
import Graphql.OptionalArgument as Opt
import Graphql.SelectionSet as SelectionSet exposing (SelectionSet)
import Html exposing (Html, button, div, h1, h2, input, label, li, option, p, select, span, text, ul)
import Html.Attributes exposing (attribute, checked, class, classList, disabled, for, id, selected, spellcheck, type_, value)
import Html.Events exposing (onCheck, onClick, onInput, onSubmit)
import Identity
import Magnes.Api.InputObject as InputObject
import Magnes.Api.Mutation as Mutation
import Magnes.Api.Object
import Magnes.Api.Object.APIKey as ApiKey
import Magnes.Api.Object.AuthQuery as AuthQuery
import Magnes.Api.Object.CreateAPIKeyResult as CreateResult
import Magnes.Api.Object.SelfMutation as SelfMutation
import Magnes.Api.Object.SelfQuery as SelfQuery
import Magnes.Api.Query as Query
import Set exposing (Set)
import Time


{-| A key as it can be read back. There is deliberately no value field here: the API does
not return one, and a field that could only ever be empty would invite a screen that implies
otherwise.
-}
type alias Key =
    { id : Int
    , name : String
    , permissions : List Identity.ObjectAction
    , expiresAt : Maybe Time.Posix
    , createdAt : Time.Posix
    }


type Listing
    = Loading
    | Failed ApiError.Failure
    | Loaded (List Key)


{-| The key's one-time value, and the name it belongs to, for the render in which it exists.
-}
type alias Revealed =
    { name : String
    , value : String
    }


{-| How making a key is going. `Created` is not a success message — it is the key's value
itself, held until the User dismisses it.
-}
type Creation
    = Ready
    | Creating
    | Rejected ApiError.Failure
    | Created Revealed


{-| A key as it would be made. `chosen` holds `Identity.actionKey` strings rather than
records so that ticking is a set operation.
-}
type alias Draft =
    { name : String
    , expiry : String
    , chosen : Set String
    }


{-| `registry` is `Nothing` until bitmagnet's list of Object actions has been asked for, and
stays `Nothing` for a User who may not ask. See `offerable`.
-}
type alias State =
    { listing : Listing
    , registry : Maybe (List Identity.ObjectAction)
    , draft : Draft
    , creation : Creation
    , confirming : Maybe Int
    , revocation : Maybe ApiError.Failure
    }


newDraft : Draft
newDraft =
    { name = "", expiry = "", chosen = Set.empty }


empty : State
empty =
    { listing = Loading
    , registry = Nothing
    , draft = newDraft
    , creation = Ready
    , confirming = Nothing
    , revocation = Nothing
    }


withListing : Listing -> State -> State
withListing listing state =
    { state | listing = listing }


withRegistry : Maybe (List Identity.ObjectAction) -> State -> State
withRegistry registry state =
    { state | registry = registry }


withDraft : Draft -> State -> State
withDraft draft state =
    { state | draft = draft }


withName : String -> Draft -> Draft
withName name draft =
    { draft | name = name }


withExpiry : String -> Draft -> Draft
withExpiry expiry draft =
    { draft | expiry = expiry }


{-| Ticking an Object action also clears a refusal, which described a different selection.
-}
toggle : Identity.ObjectAction -> State -> State
toggle action state =
    let
        chosen =
            state.draft.chosen

        key =
            Identity.actionKey action

        draft =
            state.draft
    in
    { state
        | draft =
            { draft
                | chosen =
                    if Set.member key chosen then
                        Set.remove key chosen

                    else
                        Set.insert key chosen
            }
        , creation = clearRejection state.creation
    }


clearRejection : Creation -> Creation
clearRejection creation =
    case creation of
        Rejected _ ->
            Ready

        other ->
            other


withCreation : Creation -> State -> State
withCreation creation state =
    { state | creation = creation }


{-| Arming a revocation also clears the last one's refusal, which was about another key.
-}
withConfirming : Maybe Int -> State -> State
withConfirming confirming state =
    { state | confirming = confirming, revocation = Nothing }


withRevocation : Maybe ApiError.Failure -> State -> State
withRevocation revocation state =
    { state | revocation = revocation }



-- WHAT MAY BE OFFERED


{-| The Object actions this Identity may put on a key.

`createAPIKey` takes concrete triples from bitmagnet's registry and refuses anything else,
and a key may hold only what its owner holds. So the offer is the registry narrowed to what
the Identity can do — `Identity.can`, so a `**` grant matches the actions it covers.

Without the registry the offer is the Identity's own Permissions, minus any wildcard. That
is not a degraded guess: for a Role whose Permissions are already concrete, the two answers
are the same set. It differs only for a wildcard, which cannot be expanded without the
registry — and which is exactly the case `needsRegistry` says to fetch for.

-}
offerable : Identity.Identity -> Maybe (List Identity.ObjectAction) -> List Identity.ObjectAction
offerable identity registry =
    case registry of
        Just actions ->
            List.filter (\action -> Identity.can action identity) actions

        Nothing ->
            List.filter Identity.concrete (Identity.permissions identity)


{-| Whether this Identity needs bitmagnet's registry before it can be offered a choice.

Only a wildcard does: it stands for actions it cannot name, so there is nothing to tick
without the list it stands for. An Identity whose Permissions are all concrete already
holds its own answer, and asking would spend a request on it.

The two conditions travel together in practice rather than by coincidence. Reading the
registry means `graphql::auth::query`, and a wildcard broad enough to need expanding is
normally broad enough to grant it — `admin`'s `**::**::**` is both. Where it is not, the
concrete half is still offered rather than the screen failing.

One case this does not cover: an Identity whose only broad grant is a wildcard that does
_not_ reach `auth::query` — say `graphql::torrentContent::**`. It cannot fetch the registry
and cannot be expanded from it, so it falls to the concrete branch, where the wildcard is
filtered out and the grid may come up empty. It is unreachable today: `admin`'s is the only
wildcard Role, and it grants `auth::query`, while the Role editor stores only concrete
Object actions. If a partial wildcard ever becomes assignable, this is where it is handled.

-}
needsRegistry : Identity.Identity -> Bool
needsRegistry identity =
    List.any (Identity.concrete >> not) (Identity.permissions identity)
        && Identity.can (Identity.graphql "auth" "query") identity


{-| The Object actions a key names but its owner can no longer exercise.

Enforcement needs both the key's selection and the owner's Role, so losing a Role action
suspends the key's matching half without changing the key. Naming those is what keeps a
listed Object action honest; it is the compact form of the distinction, with the full
comparison left to a troubleshooting view that does not exist yet.

This asks `Identity.can` of the owning User's own effective Object actions, which is what
`self.identity.permissions` reports. Effective authority also folds in the Anonymous
identity's actions, so an action granted _only_ to Anonymous and not the User's Role could
in principle be marked suspended here; a User's Role effectively covers the Anonymous
actions, so it does not misfire in practice.

-}
suspended : Identity.Identity -> Key -> List Identity.ObjectAction
suspended identity key =
    List.filter (\action -> not (Identity.can action identity)) key.permissions



-- REQUESTS


fetch : String -> (Result (Graphql.Http.Error (List Key)) (List Key) -> msg) -> Cmd msg
fetch apiUrl toMsg =
    SelfQuery.apiKeys keySelection
        |> Query.self
        |> Bitmagnet.queryRequest apiUrl
        |> Graphql.Http.send toMsg


{-| Asked for on its own, not alongside the keys.

`Query.auth` is non-null and permissioned, so a refusal nulls the whole response rather
than that one field. Asked for together, an ordinary User would lose their key list to a
field they never needed.

-}
fetchRegistry :
    String
    -> (Result (Graphql.Http.Error (List Identity.ObjectAction)) (List Identity.ObjectAction) -> msg)
    -> Cmd msg
fetchRegistry apiUrl toMsg =
    AuthQuery.listObjectActions Identity.objectActionSelection
        |> Query.auth
        |> Bitmagnet.queryRequest apiUrl
        |> Graphql.Http.send toMsg


keySelection : SelectionSet Key Magnes.Api.Object.APIKey
keySelection =
    SelectionSet.map5 Key
        ApiKey.id
        ApiKey.name
        (ApiKey.permissions Identity.objectActionSelection)
        ApiKey.expiresAt
        ApiKey.createdAt


create :
    String
    -> List Identity.ObjectAction
    -> Draft
    -> (Result (Graphql.Http.Error Revealed) Revealed -> msg)
    -> Cmd msg
create apiUrl offered draft toMsg =
    createSelection offered draft
        |> Bitmagnet.mutationRequest apiUrl
        |> Graphql.Http.send toMsg


{-| An empty expiry is `Absent`, not an empty Duration: bitmagnet reads a missing expiry as
"never", and an empty string is not a duration it can parse.
-}
createSelection : List Identity.ObjectAction -> Draft -> SelectionSet Revealed RootMutation
createSelection offered draft =
    SelfMutation.createAPIKey
        { input =
            InputObject.buildCreateAPIKeyInput
                { name = String.trim draft.name
                , permissions = List.map inputFor (chosenActions offered draft)
                }
                (\optional ->
                    { optional
                        | expiry =
                            if String.isEmpty draft.expiry then
                                Opt.Absent

                            else
                                Opt.Present draft.expiry
                    }
                )
        }
        (SelectionSet.map2 Revealed CreateResult.name CreateResult.apiKey)
        |> Mutation.self


{-| The offered Object actions that are ticked. Filtered from `offered` rather than parsed
back out of the key strings — the same shape as `Roles.desiredActions` — so a ticked key
whose action is no longer offered (the registry narrowed the choice after it was ticked) is
never sent, and there is no split that has to stay the inverse of `Identity.actionKey`.
-}
chosenActions : List Identity.ObjectAction -> Draft -> List Identity.ObjectAction
chosenActions offered draft =
    List.filter (\action -> Set.member (Identity.actionKey action) draft.chosen) offered


inputFor : Identity.ObjectAction -> InputObject.AuthObjectActionInput
inputFor action =
    { namespace = action.namespace, object = action.object, action = action.action }


delete : String -> Int -> (Result (Graphql.Http.Error ()) () -> msg) -> Cmd msg
delete apiUrl id toMsg =
    SelfMutation.deleteAPIKey { id = id }
        |> Mutation.self
        |> SelectionSet.map (always ())
        |> Bitmagnet.mutationRequest apiUrl
        |> Graphql.Http.send toMsg



-- VIEW


type alias Messages msg =
    { nameChanged : String -> msg
    , expiryChosen : String -> msg
    , actionToggled : Identity.ObjectAction -> msg
    , submitted : msg
    , revealDismissed : msg
    , revokeRequested : Int -> msg
    , revokeConfirmed : Int -> msg
    , revokeCancelled : msg
    }


view : Time.Zone -> Messages msg -> Identity.Identity -> State -> Html msg
view zone messages identity state =
    div [ class "page" ]
        [ h1 [] [ text "API keys" ]
        , p [ class "notice" ]
            [ text "An API key signs in for a program rather than a person. It can do only what you can, and only what you name here." ]
        , revealPanel messages state.creation
        , createForm messages identity state
        , h2 [] [ text "Your keys" ]
        , listingView zone messages identity state
        ]


{-| The one render in which the key's value exists.

It stands above the form rather than inside it, and it is dismissed deliberately, because
navigating away is the failure this screen exists to prevent.

-}
revealPanel : Messages msg -> Creation -> Html msg
revealPanel messages creation =
    case creation of
        Created revealed ->
            div [ class "panel api-key-reveal", attribute "role" "alert" ]
                [ h2 [] [ text ("Your new key: " ++ revealed.name) ]
                , p [ class "api-key-warning" ]
                    [ text "Copy it now. This is the only time it is shown — bitmagnet stores no copy it can give back, so if it is lost the key has to be replaced." ]
                , div [ class "api-key-reveal-value" ]
                    [ Html.code [] [ text revealed.value ] ]
                , button
                    [ type_ "button", onClick messages.revealDismissed ]
                    [ text "I have stored it" ]
                ]

        _ ->
            text ""


createForm : Messages msg -> Identity.Identity -> State -> Html msg
createForm messages identity state =
    let
        busy =
            state.creation == Creating

        offered =
            offerable identity state.registry
    in
    Html.form [ class "panel api-key-form", onSubmit messages.submitted ]
        [ h2 [] [ text "Make a key" ]
        , label [ for "api-key-name" ] [ text "Name" ]
        , input
            [ id "api-key-name"
            , type_ "text"
            , value state.draft.name
            , spellcheck False
            , disabled busy
            , onInput messages.nameChanged
            ]
            []
        , label [ for "api-key-expiry" ] [ text "Expires" ]
        , select
            [ id "api-key-expiry", disabled busy, onInput messages.expiryChosen ]
            (List.map
                (\( label_, duration ) ->
                    option [ value duration, selected (duration == state.draft.expiry) ] [ text label_ ]
                )
                Expiry.options
            )
        , permissionGrid messages busy offered state.draft
        , offerNotice offered
        , creationNotice state.creation
        , button
            [ type_ "submit"
            , class "submit"
            , disabled (busy || not (submittable state.draft))
            ]
            [ text
                (if busy then
                    "Making…"

                 else
                    "Make key"
                )
            ]
        ]


{-| A key with no name cannot be told from another, and one with nothing ticked can do
nothing. bitmagnet would take both; neither is worth making.
-}
submittable : Draft -> Bool
submittable draft =
    not (String.isEmpty (String.trim draft.name)) && not (Set.isEmpty draft.chosen)


permissionGrid : Messages msg -> Bool -> List Identity.ObjectAction -> Draft -> Html msg
permissionGrid messages busy offered draft =
    div [ class "permissions" ]
        (List.map (namespaceGroup messages busy draft) (Identity.byNamespace offered))


{-| Grouped by namespace, as on the Role screen. `http` and `torznab` are offered as well as
`graphql`: a browser cannot reach them, but a key is made precisely for the things that are
not this browser, and Torznab is one of the reasons to want a key at all.
-}
namespaceGroup : Messages msg -> Bool -> Draft -> ( String, List Identity.ObjectAction ) -> Html msg
namespaceGroup messages busy draft ( namespace, actions ) =
    div [ class "permission-group" ]
        [ Html.h3 [] [ text namespace ]
        , div [ class "permission-list" ]
            (List.map (permissionBox messages busy draft) actions)
        ]


permissionBox : Messages msg -> Bool -> Draft -> Identity.ObjectAction -> Html msg
permissionBox messages busy draft action =
    label [ class "permission" ]
        [ input
            [ type_ "checkbox"
            , checked (Set.member (Identity.actionKey action) draft.chosen)
            , disabled busy
            , onCheck (\_ -> messages.actionToggled action)
            ]
            []
        , span [] [ text (action.object ++ "::" ++ action.action) ]
        ]


{-| An empty offer is not an empty grid with no explanation.
-}
offerNotice : List Identity.ObjectAction -> Html msg
offerNotice offered =
    if List.isEmpty offered then
        p [ class "notice" ]
            [ text "Your Identity holds no Object actions, so there is nothing a key of yours could be given." ]

    else
        text ""


creationNotice : Creation -> Html msg
creationNotice creation =
    case creation of
        Rejected failure ->
            p [ class "notice error", attribute "role" "alert" ]
                [ text (ApiError.toMessage failure) ]

        _ ->
            text ""


listingView : Time.Zone -> Messages msg -> Identity.Identity -> State -> Html msg
listingView zone messages identity state =
    case state.listing of
        Loading ->
            p [ class "notice" ] [ text "Loading your API keys…" ]

        Failed failure ->
            p [ class "notice error" ] [ text (ApiError.toMessage failure) ]

        Loaded [] ->
            p [ class "notice" ] [ text "You have no API keys." ]

        Loaded keys ->
            div []
                [ revocationNotice state.revocation
                , ul [ class "api-keys" ]
                    (List.map (keyRow zone messages identity state) keys)
                ]


revocationNotice : Maybe ApiError.Failure -> Html msg
revocationNotice revocation =
    case revocation of
        Just failure ->
            p [ class "notice error", attribute "role" "alert" ]
                [ text (ApiError.toMessage failure) ]

        Nothing ->
            text ""


keyRow : Time.Zone -> Messages msg -> Identity.Identity -> State -> Key -> Html msg
keyRow zone messages identity state key =
    li [ class "api-key" ]
        [ div [ class "api-key-name" ] [ text key.name ]
        , div [ class "api-key-facts" ]
            [ span [] [ text ("Created: " ++ Format.date zone key.createdAt) ]
            , span [] [ text (expiryOf zone key) ]
            ]
        , permissionsOf identity key
        , revokeControl messages state key
        ]


{-| A key with no expiry is not one whose expiry is unknown.
-}
expiryOf : Time.Zone -> Key -> String
expiryOf zone key =
    case key.expiresAt of
        Just moment ->
            "Expires: " ++ Format.date zone moment

        Nothing ->
            "Never expires"


{-| The Object actions the key was selected for, and which of them are not currently reachable.

The suspended ones are still listed — they are part of the key and come back when the
owner's Role does — but they are marked, because a Permission that reads as working and
does not is worse than one that is absent.

-}
permissionsOf : Identity.Identity -> Key -> Html msg
permissionsOf identity key =
    let
        withheld =
            suspended identity key
    in
    div [ class "api-key-permissions" ]
        [ ul [ class "api-key-actions" ]
            (List.map (actionItem withheld) key.permissions)
        , if List.isEmpty withheld then
            text ""

          else
            p [ class "notice" ]
                [ text
                    (String.fromInt (List.length withheld)
                        ++ " "
                        ++ Format.plural (List.length withheld) "action"
                        ++ " here your Role no longer grants, so the key cannot use "
                        ++ Format.forCount (List.length withheld) { one = "it", many = "them" }
                        ++ " until it does again."
                    )
                ]
        ]


actionItem : List Identity.ObjectAction -> Identity.ObjectAction -> Html msg
actionItem withheld action =
    li
        [ classList
            [ ( "api-key-action", True )
            , ( "api-key-action-suspended", List.member action withheld )
            ]
        ]
        [ text (Identity.actionKey action) ]


revokeControl : Messages msg -> State -> Key -> Html msg
revokeControl messages state key =
    if state.confirming == Just key.id then
        div [ class "api-key-confirm", attribute "role" "alert" ]
            [ span []
                [ text ("Revoke " ++ key.name ++ "? Anything using it stops working immediately.") ]
            , button
                [ type_ "button", class "danger", onClick (messages.revokeConfirmed key.id) ]
                [ text "Revoke" ]
            , button
                [ type_ "button", onClick messages.revokeCancelled ]
                [ text "Keep it" ]
            ]

    else
        button
            [ type_ "button"
            , class "danger"
            , disabled (state.confirming /= Nothing)
            , onClick (messages.revokeRequested key.id)
            ]
            [ text "Revoke" ]
