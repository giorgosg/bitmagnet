module Users exposing (Action(..), Confirming(..), Listing(..), Messages, Page, State, delete, empty, fetch, nextOffset, pageSize, previousOffset, setEnabled, setRole, view, withAction, withConfirming, withListing, withOffset, withQuery)

{-| Administering Users: who is here, what Role each holds, and taking access away.

The screen the Angular UI never had: `setUserRole`, `setUserEnabled` and `deleteUser`
are called nowhere in bitmagnet's own interface, so everything that takes access away
is built here from nothing.

The `User` schema carries no `enabled` field, so this screen cannot show who is
disabled and does not pretend to: disabling and enabling are offered as separate
deliberate acts, and what became of the last one is read back from the server by
refetching, never fabricated. See `docs/auth-api.md`, "Types".

-}

import ApiError
import Bitmagnet
import Format
import Graphql.Http
import Graphql.Operation exposing (RootQuery)
import Graphql.OptionalArgument as Opt
import Graphql.SelectionSet as SelectionSet exposing (SelectionSet)
import Html exposing (Html, button, div, h1, input, label, li, option, p, text, ul)
import Html.Attributes exposing (attribute, class, disabled, for, id, selected, spellcheck, type_, value)
import Html.Events exposing (onClick, onInput)
import Html.Keyed
import Identity
import Magnes.Api.InputObject as InputObject
import Magnes.Api.Mutation as Mutation
import Magnes.Api.Object.AuthMutation as AuthMutation
import Magnes.Api.Object.AuthQuery as AuthQuery
import Magnes.Api.Object.ListUsersResult as ListUsersResult
import Magnes.Api.Object.Role as ApiRole
import Magnes.Api.Query as Query
import Time


type Listing
    = Loading
    | Failed ApiError.Failure
    | Loaded Page


{-| One page of Users, plus the Roles a User may hold. They arrive together because
they are needed together and neither is useful alone: a list whose Roles cannot be
changed, or a Role select built for a list that failed to load.
-}
type alias Page =
    { users : List Identity.User
    , totalCount : Int
    , roles : List String
    }


{-| The ask that has been made but not yet answered. The act's click is always the
second one; arming several at once is how the wrong one gets clicked, so one ask is
all the state can hold. `ChangeOwnRole` carries the Role it would apply — its name is
the contract: only the viewer's own User may be armed with it, which is what
`Main` enforces when the select changes. The rest need nothing but the User, whose
name the row itself supplies.
-}
type Confirming
    = Delete Int
    | Disable Int
    | Enable Int
    | ChangeOwnRole { userId : Int, role : String }


{-| How the act in flight or just attempted is going. One at a time: the acts all
refetch the list on success, so two in flight would race each other's refetch. While
one runs, every row's controls wait — an armed ask's own two buttons included, since
the state could not tell a second click what it belonged to.

`Refused` is announced above the list — the row it was about may not survive the next
refetch, and a refusal that vanishes into a row teaches silence.

-}
type Action
    = None
    | Working
    | Refused ApiError.Failure


type alias State =
    { listing : Listing
    , offset : Int
    , query : String
    , confirming : Maybe Confirming
    , action : Action
    }


empty : State
empty =
    { listing = Loading
    , offset = 0
    , query = ""
    , confirming = Nothing
    , action = None
    }


withListing : Listing -> State -> State
withListing listing state =
    { state | listing = listing }


{-| A changed search is a different listing: whatever page was being read belonged to
the old question, so paging starts over. The query itself travels verbatim — bitmagnet
wraps it in `%…%` on its side, so a space the User typed is part of the search.
-}
withQuery : String -> State -> State
withQuery query state =
    { state | query = query, offset = 0 }


withOffset : Int -> State -> State
withOffset offset state =
    { state | offset = offset }


pageSize : Int
pageSize =
    50



-- REQUESTS


{-| One page of Users, with the Roles they may hold. `usernameLike` is sent only when
it says something: an empty query is no filter, and `Absent` says that honestly.
-}
fetch : String -> String -> Int -> (Result (Graphql.Http.Error Page) Page -> msg) -> Cmd msg
fetch apiUrl query offset toMsg =
    page query offset
        |> Bitmagnet.queryRequest apiUrl
        |> Graphql.Http.send toMsg


page : String -> Int -> SelectionSet Page RootQuery
page query offset =
    Query.auth
        (SelectionSet.map2
            (\found roles ->
                { users = found.users
                , totalCount = found.totalCount
                , roles = roles
                }
            )
            (AuthQuery.listUsers
                (\optional ->
                    { optional
                        | input =
                            Opt.Present
                                (InputObject.buildListUsersInput
                                    (\input ->
                                        { input
                                            | usernameLike =
                                                if String.isEmpty query then
                                                    Opt.Absent

                                                else
                                                    Opt.Present query
                                            , pagination =
                                                Opt.Present
                                                    (InputObject.buildPaginationInput
                                                        (\pagination ->
                                                            { pagination
                                                                | limit = Opt.Present pageSize
                                                                , offset = Opt.Present offset
                                                            }
                                                        )
                                                    )
                                        }
                                    )
                                )
                    }
                )
                (SelectionSet.map2 (\users totalCount -> { users = users, totalCount = totalCount })
                    (ListUsersResult.users Identity.userSelection)
                    ListUsersResult.totalCount
                )
            )
            (AuthQuery.listRoles ApiRole.name)
        )


{-| Change the Role a User holds. The answer is a `User`, but the screen reads the
outcome back from a listing refetch rather than splicing it in: the server's copy is
what this screen is for, and `User` carries no `enabled` state to fabricate.
-}
setRole : String -> Int -> String -> (Result (Graphql.Http.Error Identity.User) Identity.User -> msg) -> Cmd msg
setRole apiUrl userId role toMsg =
    Mutation.auth
        (AuthMutation.setUserRole { userId = userId, roleName = role } Identity.userSelection)
        |> Bitmagnet.mutationRequest apiUrl
        |> Graphql.Http.send toMsg


setEnabled : String -> Int -> Bool -> (Result (Graphql.Http.Error Identity.User) Identity.User -> msg) -> Cmd msg
setEnabled apiUrl userId enabled toMsg =
    Mutation.auth
        (AuthMutation.setUserEnabled { userId = userId, enabled = enabled } Identity.userSelection)
        |> Bitmagnet.mutationRequest apiUrl
        |> Graphql.Http.send toMsg


{-| Remove a User entirely. A deleted User cannot sign in again, and their API keys
stop answering — the warning on the ask says so before this is allowed to run.
-}
delete : String -> Int -> (Result (Graphql.Http.Error ()) () -> msg) -> Cmd msg
delete apiUrl userId toMsg =
    Mutation.auth (AuthMutation.deleteUser { userId = userId } |> SelectionSet.map (always ()))
        |> Bitmagnet.mutationRequest apiUrl
        |> Graphql.Http.send toMsg


{-| The offset of the page after this one, if the count says there is one.
-}
nextOffset : { offset : Int, totalCount : Int } -> Maybe Int
nextOffset { offset, totalCount } =
    if offset + pageSize < totalCount then
        Just (offset + pageSize)

    else
        Nothing


previousOffset : Int -> Maybe Int
previousOffset offset =
    if offset <= 0 then
        Nothing

    else
        Just (max 0 (offset - pageSize))


{-| Arming an ask also disarms any other: two armed asks are two wrong clicks waiting.
And it clears the last refusal, which described a different attempt on a different User.
-}
withConfirming : Maybe Confirming -> State -> State
withConfirming confirming state =
    { state | confirming = confirming, action = None }


withAction : Action -> State -> State
withAction action state =
    { state | action = action }


type alias Messages msg =
    { queryChanged : String -> msg
    , pageRequested : Int -> msg
    , roleChosen : Int -> String -> msg
    , disableRequested : Int -> msg
    , enableRequested : Int -> msg
    , deleteRequested : Int -> msg
    , confirmed : msg
    , cancelled : msg
    }


view : Time.Zone -> Messages msg -> Identity.Identity -> State -> Html msg
view zone messages identity state =
    let
        mayMutate =
            Identity.can (Identity.graphql "auth" "mutate") identity

        self =
            case identity of
                Identity.UserAuthenticated user _ ->
                    Just user.id

                _ ->
                    Nothing
    in
    div [ class "page" ]
        [ h1 [] [ text "Users" ]
        , case state.listing of
            Loading ->
                p [ class "notice" ] [ text "Loading Users…" ]

            Failed failure ->
                p [ class "notice error" ] [ text (ApiError.toMessage failure) ]

            Loaded loadedPage ->
                div []
                    [ disabledNotice
                    , searchBox messages state
                    , actionFailure state.action
                    , listed zone messages mayMutate self state loadedPage
                    , paging messages state loadedPage
                    ]
        ]


{-| The last refused act, until the next ask disarms it. It belongs above the list
rather than in the row: the row it was about may not be there any more, since the
list is refetched around the acts.
-}
actionFailure : Action -> Html msg
actionFailure action =
    case action of
        Refused failure ->
            p
                [ class "notice error", attribute "role" "alert" ]
                [ text (ApiError.toMessage failure) ]

        _ ->
            text ""


{-| Why no row says whether its User is disabled: bitmagnet's `User` schema has no
`enabled` field, so nothing here can know it. The gap is named rather than papered
over with a column of guesses — and the acts themselves, disabling and enabling,
remain available and take effect on the server whatever is or is not displayed.
-}
disabledNotice : Html msg
disabledNotice =
    p [ class "notice" ]
        [ text "Whether a User is disabled is not shown: bitmagnet does not report it." ]


{-| The search is one `usernameLike` substring, answered live: bitmagnet matches it
against usernames wrapped in `%…%`, so it reads as "called something like this". There
is no submit — the query travels as it is typed, debounced by the caller.
-}
searchBox : Messages msg -> State -> Html msg
searchBox messages state =
    div [ class "user-search" ]
        [ label [ for "user-search" ] [ text "Find a User" ]
        , input
            [ id "user-search"
            , type_ "search"
            , value state.query
            , spellcheck False
            , attribute "autocomplete" "off"
            , onInput messages.queryChanged
            ]
            []
        ]


listed : Time.Zone -> Messages msg -> Bool -> Maybe Int -> State -> Page -> Html msg
listed zone messages mayMutate self state loadedPage =
    if List.isEmpty loadedPage.users then
        if String.isEmpty state.query then
            p [ class "notice" ] [ text "No Users." ]

        else
            p [ class "notice" ]
                [ text ("No User matches \"" ++ state.query ++ "\".") ]

    else
        ul [ class "users" ]
            (List.map (row zone messages mayMutate self state.action state.confirming loadedPage.roles) loadedPage.users)


row : Time.Zone -> Messages msg -> Bool -> Maybe Int -> Action -> Maybe Confirming -> List String -> Identity.User -> Html msg
row zone messages mayMutate self action confirming roles user =
    li [ class "user" ]
        [ div [ class "user-name" ] [ text user.username ]
        , div [ class "user-facts" ]
            [ Html.span [ class "user-role" ] [ text user.role ]
            , Html.span [] [ text ("Last signed in: " ++ lastSignIn zone user) ]
            ]
        , acts messages mayMutate self action confirming roles user
        ]


{-| The ask armed for this row, if any — an ask names one User, and only that row
shows it.
-}
askFor : Identity.User -> Maybe Confirming -> Maybe Confirming
askFor user confirming =
    let
        forThisUser id =
            if id == user.id then
                confirming

            else
                Nothing
    in
    case confirming of
        Just (Delete id) ->
            forThisUser id

        Just (Disable id) ->
            forThisUser id

        Just (Enable id) ->
            forThisUser id

        Just ((ChangeOwnRole changed) as ask) ->
            if changed.userId == user.id then
                Just ask

            else
                Nothing

        Nothing ->
            Nothing


{-| The acts that take access away or move it. Gated here on the caller's own
Object action so an Identity without `auth::mutate` is not shown controls that would
only refuse — bitmagnet stays the enforcer either way. An armed ask replaces the
whole block: the select included, so a declined Role choice cannot stay on display
as though it were still pending.

The block is keyed because a `select` the person has touched is the browser's to
display, not Elm's: the DOM keeps whatever was chosen, and Elm patches nothing while
the Role the options are drawn from has not moved. A refused Role change moves
nothing, so the rejected Role would sit in the box looking applied. `roleKey` throws
the stale control away and builds an honest one — after a refusal, and after a Role
that really did change — while leaving the choice on display for as long as the act
is still in flight. Verified in a browser, since `Test.Html` does not model the DOM
state a form control carries.

-}
acts : Messages msg -> Bool -> Maybe Int -> Action -> Maybe Confirming -> List String -> Identity.User -> Html msg
acts messages mayMutate self action confirming roles user =
    let
        busy =
            working action
    in
    if not mayMutate then
        text ""

    else
        Html.Keyed.node "div" [ class "user-acts" ] <|
            case askFor user confirming of
                Just ask ->
                    [ ( "ask", askPanel messages busy self user ask ) ]

                Nothing ->
                    [ ( roleKey action user
                      , Html.select
                            [ class "user-role-select"
                            , attribute "aria-label" ("Role for " ++ user.username)
                            , disabled busy
                            , onInput (messages.roleChosen user.id)
                            ]
                            (List.map
                                (\role ->
                                    option [ value role, selected (role == user.role) ] [ text role ]
                                )
                                roles
                            )
                      )
                    , ( "disable", button [ type_ "button", disabled busy, onClick (messages.disableRequested user.id) ] [ text "Disable" ] )
                    , ( "enable", button [ type_ "button", disabled busy, onClick (messages.enableRequested user.id) ] [ text "Enable" ] )
                    , ( "delete", button [ type_ "button", class "danger", disabled busy, onClick (messages.deleteRequested user.id) ] [ text "Delete" ] )
                    ]


working : Action -> Bool
working action =
    case action of
        Working ->
            True

        _ ->
            False


{-| What the Role select is showing the truth of. It changes when the Role changes, and
when an act is refused — the two moments the box may be displaying something the server
never agreed to. It does not change while an act is in flight, so a choice being applied
stays on screen until there is an answer.
-}
roleKey : Action -> Identity.User -> String
roleKey action user =
    case action of
        Refused _ ->
            "role-refused-" ++ user.role

        _ ->
            "role-" ++ user.role


{-| One armed ask: what it would do, then the two clicks — the second one does it,
the first is what asking was for. The confirmation carries no User: the ask armed in
the state is the one thing it can mean, and the row it is on supplies the rest.

Both clicks wait while an act is in flight, for the same reason every other control
does. Declining waits too, and not only for symmetry: it clears the action along with
the ask, so a decline mid-flight would announce that nothing was running while
something still was.

-}
askPanel : Messages msg -> Bool -> Maybe Int -> Identity.User -> Confirming -> Html msg
askPanel messages busy self user ask =
    div [ class "user-confirm" ]
        [ Html.span [] [ text (warning self user ask) ]
        , case ask of
            Delete _ ->
                confirmButton messages busy "Delete"

            Disable _ ->
                confirmButton messages busy "Disable"

            Enable _ ->
                confirmButton messages busy "Enable"

            ChangeOwnRole _ ->
                confirmButton messages busy "Change"
        , button
            [ type_ "button", disabled busy, onClick messages.cancelled ]
            [ text "Keep" ]
        ]


confirmButton : Messages msg -> Bool -> String -> Html msg
confirmButton messages busy label_ =
    button
        [ type_ "button", class "danger", disabled busy, onClick messages.confirmed ]
        [ text label_ ]


{-| What the ask costs — or, for enabling, grants — in the terms of the person it
lands on.

bitmagnet does not prevent the self-affecting or locking version of any of these, so
the warning is where the honesty lives: a deleted User stops existing and cannot sign
in again; a disabled one stops working — their API keys with them — until someone
with the authority enables them; and a Role change to your own User takes effect on
the Identity now holding this screen. Enabling is the one ask that grants rather than
takes, so its warning admits what this screen cannot know: whether the User was
disabled to begin with. Nothing here claims to count administrators, because the
schema gives it no way to know.

-}
warning : Maybe Int -> Identity.User -> Confirming -> String
warning self user ask =
    let
        own =
            self == Just user.id
    in
    case ask of
        Delete _ ->
            if own then
                "Delete your own User, " ++ user.username ++ "? You will be removing yourself."

            else
                "Delete User " ++ user.username ++ "? They will stop existing and cannot sign in again."

        Disable _ ->
            if own then
                "Disable yourself? You cannot sign back in until another administrator enables you."

            else
                "Disable " ++ user.username ++ "? Their sign-ins stop being accepted, and their API keys stop answering, until someone enables them again."

        Enable _ ->
            "Enable " ++ user.username ++ "? If they were disabled, their sign-ins and API keys work again."

        ChangeOwnRole { role } ->
            "Change your own Role to " ++ role ++ "? You may be signing this screen away."


paging : Messages msg -> State -> Page -> Html msg
paging messages state loadedPage =
    let
        previous =
            previousOffset state.offset

        next =
            nextOffset { offset = state.offset, totalCount = loadedPage.totalCount }
    in
    if previous == Nothing && next == Nothing then
        text ""

    else
        div [ class "paging" ]
            [ pageButton messages "Previous" previous
            , Html.span [ class "paging-count" ]
                [ text (Format.count loadedPage.totalCount ++ " in total") ]
            , pageButton messages "Next" next
            ]


pageButton : Messages msg -> String -> Maybe Int -> Html msg
pageButton messages label_ offset =
    case offset of
        Just target ->
            button
                [ type_ "button", onClick (messages.pageRequested target) ]
                [ text label_ ]

        Nothing ->
            button [ type_ "button", disabled True ] [ text label_ ]


{-| bitmagnet leaves `lastLoginAt` null for a User who has never signed in — a User
created through registration and reached by API key, for instance. Saying so is honest;
substituting the date the User was created would not be.
-}
lastSignIn : Time.Zone -> Identity.User -> String
lastSignIn zone user =
    user.lastLoginAt
        |> Maybe.map (Format.dateTime zone)
        |> Maybe.withDefault "Not recorded"
