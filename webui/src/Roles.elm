module Roles exposing (Confirming(..), Draft, Listing(..), Messages, Page, Permission, Role, State, Submission(..), delete, desiredActions, draftNamed, empty, fetch, newDraft, ownRole, put, toggle, view, withConfirming, withDeletion, withDraft, withListing, withName, withSubmission, writable)

{-| Administering Roles: what each one grants, and the two ways that changes.

`putRole` is a **replace, not a merge** — the Object actions sent become the Role's entire
stored Permission set, and anything left out is revoked. So the whole point of this screen
is that the form is loaded from the Role before it is edited, and that everything the form
cannot draw is carried through the save rather than quietly dropped. See `docs/auth-api.md`,
"Mutations".

Three kinds of Permission arrive, and only one of them is the form's to change:

  - the ones bitmagnet names in `listObjectActions` — a checkbox each, and the only things
    a save can add or take away;
  - the ones bitmagnet holds in memory, which arrive marked `core`: `admin`'s `**/**/**`
    and the baseline `anon` and `user` get. `putRole` can neither store nor remove them, so
    they are shown ticked and fixed;
  - anything else already stored against the Role. `putRole` accepts any triple from any
    client, so a Permission can exist that no checkbox expresses. Those are named on
    screen and sent back untouched.

-}

import ApiError
import Bitmagnet
import Format
import Graphql.Http
import Graphql.Operation exposing (RootQuery)
import Graphql.SelectionSet as SelectionSet exposing (SelectionSet)
import Html exposing (Html, button, div, h1, h2, h3, input, label, li, p, text, ul)
import Html.Attributes exposing (attribute, checked, class, classList, disabled, for, id, spellcheck, type_, value)
import Html.Events exposing (onCheck, onClick, onInput, onSubmit)
import Identity
import Magnes.Api.InputObject as InputObject
import Magnes.Api.Mutation as Mutation
import Magnes.Api.Object
import Magnes.Api.Object.AuthMutation as AuthMutation
import Magnes.Api.Object.AuthQuery as AuthQuery
import Magnes.Api.Object.Permission as ApiPermission
import Magnes.Api.Object.Role as ApiRole
import Magnes.Api.Query as Query
import Set exposing (Set)


type alias Role =
    { name : String
    , core : Bool
    , permissions : List Permission
    }


{-| `core` is bitmagnet's word for a Permission it holds in memory rather than in the
database. It is the difference between a Permission this screen may revoke and one it may
only report.
-}
type alias Permission =
    { objectAction : Identity.ObjectAction
    , core : Bool
    }


{-| The Roles, and every Object action that exists. They arrive together because neither
is any use alone: a Role whose grants cannot be named, or a grid of checkboxes with no
Role to apply them to.
-}
type alias Page =
    { roles : List Role
    , actions : List Identity.ObjectAction
    }


type Listing
    = Loading
    | Failed ApiError.Failure
    | Loaded Page


{-| How the one act allowed at a time is going. A save and a deletion share it: both
mutate and both refetch, so `Saving` means _something_ is running and everything that
could start a second act waits on it — an armed deletion's own two buttons included.
-}
type Submission
    = Ready
    | Saving
    | Rejected ApiError.Failure
    | Saved String


{-| Whether the Role being written already exists. It decides one thing: `putRole` keys on
the name, so changing the name of a Role that exists would write a second Role and leave
the first where it was. There is no rename, and the form does not offer one.
-}
type Naming
    = Unnamed
    | Named


{-| Whether this Role's Permissions may be written at all, and if not, why.

The spec settles two of them: "`admin` remains fixed at its wildcard Permission and `anon`
remains governed by Anonymous access configuration", and additional Permissions are for
`editor` and `user`. Neither is a rule bitmagnet enforces — `putRole` would write rows for
either — so it is enforced here, and the reason is carried rather than inferred, because a
disabled grid with no explanation is indistinguishable from a broken one.

-}
type Writing
    = Writable
    | ReadOnly String


{-| The ask that has been made but not yet answered. One at a time, as on the User screen:
arming two is how the wrong one gets clicked. Neither carries anything but the Role's name,
because the ask itself is what the confirmation means.
-}
type Confirming
    = Deleting String
    | SavingOwn String


{-| A Role as it would be saved. `chosen` holds the keys of the Object actions ticked;
`locked` those bitmagnet grants of its own accord; `carried` the stored Permissions no
checkbox can express, kept so that saving cannot revoke them.
-}
type alias Draft =
    { name : String
    , naming : Naming
    , writing : Writing
    , chosen : Set String
    , locked : List Identity.ObjectAction
    , carried : List Identity.ObjectAction
    }


type alias State =
    { listing : Listing
    , draft : Draft
    , submission : Submission
    , confirming : Maybe Confirming
    , deletion : Maybe ApiError.Failure
    }


newDraft : Draft
newDraft =
    { name = ""
    , naming = Unnamed
    , writing = Writable
    , chosen = Set.empty
    , locked = []
    , carried = []
    }


empty : State
empty =
    { listing = Loading
    , draft = newDraft
    , submission = Ready
    , confirming = Nothing
    , deletion = Nothing
    }


withListing : Listing -> State -> State
withListing listing state =
    { state | listing = listing }


{-| Opening a different Draft ends whatever the last save had to say: it described a Role
that is no longer the one on the form.
-}
withDraft : Draft -> State -> State
withDraft draft state =
    { state | draft = draft, submission = Ready }


withSubmission : Submission -> State -> State
withSubmission submission state =
    { state | submission = submission }


{-| Arming an ask also clears the last deletion's refusal, which was about another Role.
-}
withConfirming : Maybe Confirming -> State -> State
withConfirming confirming state =
    { state | confirming = confirming, deletion = Nothing }


withDeletion : Maybe ApiError.Failure -> State -> State
withDeletion deletion state =
    { state | deletion = deletion }


withName : String -> Draft -> Draft
withName name draft =
    { draft | name = name }


{-| The Role under this name as a Draft, with its Permissions sorted into the three kinds:
what the form may change, what it may only report, and what it must carry.
-}
draftNamed : String -> Page -> Maybe Draft
draftNamed name listing =
    listing.roles
        |> List.filter (\role -> role.name == name)
        |> List.head
        |> Maybe.map (draftFor listing.actions)


draftFor : List Identity.ObjectAction -> Role -> Draft
draftFor offered role =
    let
        offeredKeys =
            Set.fromList (List.map Identity.actionKey offered)

        ( held, stored ) =
            List.partition .core role.permissions

        ( shown, unshown ) =
            List.partition (\permission -> Set.member (Identity.actionKey permission.objectAction) offeredKeys) stored
    in
    { name = role.name
    , naming = Named
    , writing = writingFor role
    , chosen = Set.fromList (List.map (.objectAction >> Identity.actionKey) shown)
    , locked = List.map .objectAction held
    , carried = List.map .objectAction unshown
    }


{-| Whether a save would mean anything for this Draft. `Main` asks before sending, and the
form asks before offering a button, so the reason itself stays in here.
-}
writable : Draft -> Bool
writable draft =
    draft.writing == Writable


writingFor : Role -> Writing
writingFor role =
    case role.name of
        "admin" ->
            ReadOnly "bitmagnet fixes admin at its wildcard Permission, which grants everything and is held in memory. Nothing written here would add to it or take anything away."

        "anon" ->
            ReadOnly "What anon holds follows the instance's Anonymous access setting, not this form. Editing it here would be overruled by the setting."

        _ ->
            Writable


toggle : Identity.ObjectAction -> Draft -> Draft
toggle action draft =
    if Set.member (Identity.actionKey action) draft.chosen then
        { draft | chosen = Set.remove (Identity.actionKey action) draft.chosen }

    else
        { draft | chosen = Set.insert (Identity.actionKey action) draft.chosen }


{-| Everything `putRole` will store, which is everything the Role will hold: the ticked
Object actions, and the stored Permissions the form could not draw.

The ticked ones are read out of the list the server offered rather than out of the Draft,
so a Permission this client never heard of cannot be written. bitmagnet validates nothing
here — an unregistered triple is stored happily and grants nothing forever — and the only
defence against that is never sending one.

Core Permissions are absent by design: they are not stored, and `putRole` can neither
create nor remove them. The one thing that costs is a stored row that duplicates a core
Permission, which the schema reports as core and this save therefore drops. Nothing the
Role can do changes, because the core grant is what was answering anyway.

-}
desiredActions : List Identity.ObjectAction -> Draft -> List Identity.ObjectAction
desiredActions offered draft =
    List.filter (\action -> Set.member (Identity.actionKey action) draft.chosen) offered ++ draft.carried



-- REQUESTS


fetch : String -> (Result (Graphql.Http.Error Page) Page -> msg) -> Cmd msg
fetch apiUrl toMsg =
    page
        |> Bitmagnet.queryRequest apiUrl
        |> Graphql.Http.send toMsg


page : SelectionSet Page RootQuery
page =
    Query.auth
        (SelectionSet.map2 Page
            (AuthQuery.listRoles roleSelection)
            (AuthQuery.listObjectActions Identity.objectActionSelection)
        )


roleSelection : SelectionSet Role Magnes.Api.Object.Role
roleSelection =
    SelectionSet.map3 Role
        ApiRole.name
        ApiRole.core
        (ApiRole.permissions permissionSelection)


permissionSelection : SelectionSet Permission Magnes.Api.Object.Permission
permissionSelection =
    SelectionSet.map2 Permission
        (ApiPermission.objectAction Identity.objectActionSelection)
        ApiPermission.core


{-| Write the Role. Creating one and editing one are the same call: `putRole` upserts on
the name, which is also why the name of a Role that exists is not editable here.
-}
put : String -> String -> List Identity.ObjectAction -> (Result (Graphql.Http.Error Role) Role -> msg) -> Cmd msg
put apiUrl name actions toMsg =
    Mutation.auth
        (AuthMutation.putRole
            { role = name, objectActions = List.map actionInput actions }
            roleSelection
        )
        |> Bitmagnet.mutationRequest apiUrl
        |> Graphql.Http.send toMsg


actionInput : Identity.ObjectAction -> InputObject.AuthObjectActionInput
actionInput action =
    InputObject.buildAuthObjectActionInput
        { namespace = action.namespace, object = action.object, action = action.action }


delete : String -> String -> (Result (Graphql.Http.Error ()) () -> msg) -> Cmd msg
delete apiUrl name toMsg =
    Mutation.auth (AuthMutation.deleteRole { role = name } |> SelectionSet.map (always ()))
        |> Bitmagnet.mutationRequest apiUrl
        |> Graphql.Http.send toMsg



-- VIEW


type alias Messages msg =
    { nameChanged : String -> msg
    , actionToggled : Identity.ObjectAction -> msg
    , submitted : msg
    , editRequested : String -> msg
    , editCancelled : msg
    , deleteRequested : String -> msg
    , confirmed : msg
    , cancelled : msg
    }


view : Messages msg -> Identity.Identity -> State -> Html msg
view messages identity state =
    let
        mayMutate =
            Identity.can (Identity.graphql "auth" "mutate") identity

        -- A save and a deletion are the same one act: both mutate, and both refetch the
        -- list when the server accepts. Two in flight would race each other's refetch,
        -- so while either runs, everything that could start another waits.
        busy =
            state.submission == Saving
    in
    div [ class "page" ]
        [ h1 [] [ text "Roles" ]
        , case state.listing of
            Loading ->
                p [ class "notice" ] [ text "Loading Roles…" ]

            Failed failure ->
                p [ class "notice error" ] [ text (ApiError.toMessage failure) ]

            Loaded loadedPage ->
                div []
                    [ if mayMutate then
                        editor messages busy (ownRole identity) state loadedPage

                      else
                        text ""
                    , deletionFailure state.deletion
                    , listed messages mayMutate busy state.confirming loadedPage
                    ]
        ]


{-| The Role the viewer holds, when a User is holding this screen. It is what makes a save
self-affecting, and the only reason this module is given the Identity beyond the gate.
-}
ownRole : Identity.Identity -> Maybe String
ownRole identity =
    case identity of
        Identity.UserAuthenticated user _ ->
            Just user.role

        Identity.APIKeyAuthenticated user _ _ ->
            Just user.role

        _ ->
            Nothing


{-| One form for both acts, because bitmagnet has one mutation for both. What changes
between them is the name: typed for a Role being made, fixed for one being edited.
-}
editor : Messages msg -> Bool -> Maybe String -> State -> Page -> Html msg
editor messages busy own state loadedPage =
    let
        mayWrite =
            writable state.draft
    in
    Html.form [ class "panel role-editor", onSubmit messages.submitted ]
        [ h2 [] [ text (heading state.draft) ]
        , naming messages busy state.draft
        , readOnlyNotice state.draft
        , permissionGrid messages (busy || not mayWrite) loadedPage.actions state.draft
        , lockedNotice state.draft
        , carriedNotice state.draft
        , outcome state.submission
        , if not mayWrite then
            -- Visible, and immutable: there is nothing to press, because nothing pressed
            -- here would change what the Role holds.
            text ""

          else
            case state.confirming of
                Just (SavingOwn _) ->
                    ownSaveAsk messages busy state.draft

                _ ->
                    button
                        [ type_ "submit"
                        , class "submit"

                        -- A Role with no name is not a Role bitmagnet will take, and
                        -- `onSubmit` still fires on Enter, so `Main` refuses it as well.
                        , disabled (busy || String.isEmpty (String.trim state.draft.name))
                        ]
                        [ text (submitLabel state own) ]
        ]


{-| Saving the Role you yourself hold, asked before it happens.

The spec requires a clear warning before a self-affecting or potentially locking mutation,
and this is the locking one on this screen: bitmagnet does not stop a Role from writing
away the `auth::mutate` its own holder is standing on, and once written there is no screen
left to undo it from. The ask names that rather than the general shape of the change.

-}
ownSaveAsk : Messages msg -> Bool -> Draft -> Html msg
ownSaveAsk messages busy draft =
    div [ class "role-confirm", attribute "role" "alert" ]
        [ Html.span [] [ text (ownSaveWarning draft) ]
        , button
            [ type_ "button", class "danger", disabled busy, onClick messages.confirmed ]
            [ text ("Save " ++ draft.name) ]
        , button
            [ type_ "button", disabled busy, onClick messages.cancelled ]
            [ text "Keep" ]
        ]


ownSaveWarning : Draft -> String
ownSaveWarning draft =
    if Set.member (Identity.actionKey (Identity.graphql "auth" "mutate")) draft.chosen then
        "Save your own Role, " ++ draft.name ++ "? It is the Role you are holding this screen with, so what you have ticked takes effect on you."

    else
        "Save your own Role, " ++ draft.name ++ ", without administration? You are removing the Permission this screen runs on, and only another administrator could give it back."


readOnlyNotice : Draft -> Html msg
readOnlyNotice draft =
    case draft.writing of
        ReadOnly reason ->
            p [ class "notice" ] [ text reason ]

        Writable ->
            text ""


heading : Draft -> String
heading draft =
    case ( draft.naming, draft.writing ) of
        ( Unnamed, _ ) ->
            "Create a Role"

        ( Named, ReadOnly _ ) ->
            draft.name

        ( Named, Writable ) ->
            "Editing " ++ draft.name


submitLabel : State -> Maybe String -> String
submitLabel state own =
    if state.submission == Saving then
        "Saving…"

    else
        case state.draft.naming of
            Unnamed ->
                "Create Role"

            Named ->
                if own == Just state.draft.name then
                    -- The click that follows is an ask, not the save.
                    "Save " ++ state.draft.name ++ "…"

                else
                    "Save " ++ state.draft.name


{-| A Role that exists is named, not renameable: `putRole` writes to the name it is given,
so a changed one would leave this Role alone and make a second beside it. Saying so is
better than a disabled box that looks like it might become editable.
-}
naming : Messages msg -> Bool -> Draft -> Html msg
naming messages busy draft =
    case draft.naming of
        Unnamed ->
            div [ class "role-naming" ]
                [ label [ for "role-name" ] [ text "Name" ]
                , input
                    [ id "role-name"
                    , type_ "text"
                    , value draft.name
                    , onInput messages.nameChanged
                    , attribute "autocomplete" "off"
                    , spellcheck False
                    , disabled busy
                    ]
                    []
                ]

        Named ->
            div [ class "role-naming" ]
                [ case draft.writing of
                    Writable ->
                        p [ class "notice" ]
                            [ text ("Editing " ++ draft.name ++ ". A Role cannot be renamed: saving under another name would make a second Role and leave this one as it is.") ]

                    ReadOnly _ ->
                        text ""
                , button
                    [ type_ "button", disabled busy, onClick messages.editCancelled ]
                    [ text "Create a Role instead" ]
                ]


permissionGrid : Messages msg -> Bool -> List Identity.ObjectAction -> Draft -> Html msg
permissionGrid messages busy offered draft =
    div [ class "permissions" ]
        (List.map (namespaceGroup messages busy draft) (Identity.byNamespace offered))


{-| Grouped by namespace, in the order bitmagnet listed them. Only `graphql` is reachable
from a browser, but `http` and `torznab` are real grants an API key may need, so a Role
editor that hid them would be unable to describe the Roles that exist.
-}
namespaceGroup : Messages msg -> Bool -> Draft -> ( String, List Identity.ObjectAction ) -> Html msg
namespaceGroup messages busy draft ( namespace, actions ) =
    div [ class "permission-group" ]
        [ h3 [] [ text namespace ]
        , div [ class "permission-list" ]
            (List.map (permissionBox messages busy draft) actions)
        ]


permissionBox : Messages msg -> Bool -> Draft -> Identity.ObjectAction -> Html msg
permissionBox messages busy draft action =
    let
        held =
            holding draft action
    in
    label [ classList [ ( "permission", True ), ( "permission-fixed", held == Core ) ] ]
        [ input
            [ type_ "checkbox"
            , checked (held /= NotHeld)
            , disabled (busy || held == Core)
            , onCheck (\_ -> messages.actionToggled action)
            ]
            []
        , Html.span [] [ text (action.object ++ "::" ++ action.action) ]
        ]


type Holding
    = Held
    | NotHeld
    | Core


holding : Draft -> Identity.ObjectAction -> Holding
holding draft action =
    if List.member action draft.locked then
        Core

    else if Set.member (Identity.actionKey action) draft.chosen then
        Held

    else
        NotHeld


{-| The Permissions bitmagnet grants this Role itself. They are ticked because the Role
really does hold them, and fixed because `putRole` writes the stored set and these are not
in it — unticking one would do nothing and report that it had.
-}
lockedNotice : Draft -> Html msg
lockedNotice draft =
    if List.isEmpty draft.locked then
        text ""

    else
        p [ class "notice" ]
            [ text
                ("Fixed: bitmagnet grants "
                    ++ Format.forCount (List.length draft.locked)
                        { one = "this Permission", many = "these Permissions" }
                    ++ " to the Role itself, and they cannot be revoked here — "
                    ++ String.join ", " (List.map Identity.actionKey draft.locked)
                    ++ ". "
                    ++ Format.forCount (List.length draft.locked)
                        { one = "It is ticked above unless bitmagnet does not name it as an Object action, in which case no box can show it."
                        , many = "They are ticked above except any bitmagnet does not name as an Object action, which no box can show."
                        }
                )
            ]


{-| Permissions already stored that no checkbox can express, because bitmagnet does not
name them in `listObjectActions`. Saving keeps them, which is the whole reason they are
counted here rather than lost in the replace.
-}
carriedNotice : Draft -> Html msg
carriedNotice draft =
    if List.isEmpty draft.carried then
        text ""

    else
        p [ class "notice" ]
            [ text
                ("Kept as they are: this Role holds "
                    ++ Format.forCount (List.length draft.carried)
                        { one = "a Permission", many = "Permissions" }
                    ++ " bitmagnet does not list as an Object action, so no box above can show "
                    ++ Format.forCount (List.length draft.carried) { one = "it", many = "them" }
                    ++ " — "
                    ++ String.join ", " (List.map Identity.actionKey draft.carried)
                    ++ ". Saving leaves "
                    ++ Format.forCount (List.length draft.carried) { one = "it", many = "them" }
                    ++ " in place."
                )
            ]


outcome : Submission -> Html msg
outcome submission =
    case submission of
        Rejected failure ->
            p
                [ class "notice error field-error", attribute "role" "alert" ]
                [ text (ApiError.toMessage failure) ]

        Saved name ->
            p
                [ class "notice", attribute "role" "status" ]
                [ text ("Saved the Role " ++ name ++ ".") ]

        Ready ->
            text ""

        Saving ->
            text ""


{-| A refused deletion belongs above the list rather than in the row: the row it was about
may not be there any more, since the list is refetched around it.
-}
deletionFailure : Maybe ApiError.Failure -> Html msg
deletionFailure deletion =
    case deletion of
        Just failure ->
            p [ class "notice error", attribute "role" "alert" ]
                [ text (ApiError.toMessage failure) ]

        Nothing ->
            text ""


listed : Messages msg -> Bool -> Bool -> Maybe Confirming -> Page -> Html msg
listed messages mayMutate busy confirming loadedPage =
    if List.isEmpty loadedPage.roles then
        p [ class "notice" ] [ text "No Roles." ]

    else
        ul [ class "roles" ]
            (List.map (row messages mayMutate busy confirming) loadedPage.roles)


row : Messages msg -> Bool -> Bool -> Maybe Confirming -> Role -> Html msg
row messages mayMutate busy confirming role =
    li [ class "role" ]
        [ div [ class "role-name" ] [ text role.name ]
        , div [ class "role-facts" ]
            [ Html.span [] [ text (grants role) ]
            , if role.core then
                Html.span [ class "role-core" ] [ text "core" ]

              else
                text ""
            ]
        , acts messages mayMutate busy confirming role
        ]


grants : Role -> String
grants role =
    let
        n =
            List.length role.permissions
    in
    Format.count n ++ " " ++ Format.plural n "Permission"


{-| A core Role is offered no deletion, because bitmagnet refuses one: `DeleteRole` checks
the name against its own list before it touches the database. Offering the click would
only teach that the screen does not know the rules it is presenting.
-}
acts : Messages msg -> Bool -> Bool -> Maybe Confirming -> Role -> Html msg
acts messages mayMutate busy confirming role =
    if not mayMutate then
        text ""

    else if confirming == Just (Deleting role.name) then
        div [ class "role-confirm" ]
            [ Html.span [] [ text (warning role) ]
            , button
                [ type_ "button", class "danger", disabled busy, onClick messages.confirmed ]
                [ text "Delete" ]
            , button
                [ type_ "button", disabled busy, onClick messages.cancelled ]
                [ text "Keep" ]
            ]

    else
        div [ class "role-acts" ]
            [ button
                [ type_ "button", disabled busy, onClick (messages.editRequested role.name) ]
                [ text (openLabel role) ]
            , if role.core then
                text ""

              else
                button
                    [ type_ "button", class "danger", disabled busy, onClick (messages.deleteRequested role.name) ]
                    [ text "Delete" ]
            ]


{-| A Role whose Permissions cannot be written is opened to be read, and the button says
so rather than promising an edit that the form will then refuse.
-}
openLabel : Role -> String
openLabel role =
    case writingFor role of
        Writable ->
            "Edit"

        ReadOnly _ ->
            "View"


{-| What deleting costs, in the terms of the database that will decide it.

`users.role_name` references `roles(name)` with no cascade, so Postgres refuses the delete
while anyone holds the Role and bitmagnet passes the refusal through as an opaque database
error — worth saying before the click rather than after it. `invitations.role_name`
cascades with no claimed/unclaimed distinction, so every Invitation issued for this Role
goes with it silently — the codes nobody has used yet and the record of the ones already
claimed alike — and this is the only place that would ever say so.

-}
warning : Role -> String
warning role =
    "Delete the Role "
        ++ role.name
        ++ "? It will be refused while any User still holds it, and every Invitation issued for it, claimed or not, is deleted with it."
