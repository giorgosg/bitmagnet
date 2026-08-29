module Invitations exposing (Invitation, Listing(..), Messages, Page, State, Submission(..), create, empty, expiries, fetch, nextOffset, pageSize, previousOffset, registrationLink, view, withConfirming, withExpiry, withListing, withOffset, withRole, withSubmission, withWithdrawal, withdraw)

{-| Administering Invitations: what exists, making one, and withdrawing one.

An Invitation is a single-use code that permits its bearer to register a User, and its
Role is the Role that User gets — `Register` reads it straight off the Invitation. So
this screen decides what a stranger becomes, which is why the Role is chosen from the
Roles the server names rather than typed: `Mutation.auth.invite` writes the string
through without validating it, and `invitations.role_name` is a foreign key, so a Role
that does not exist fails in the database and comes back as an opaque server error
rather than as anything a person could act on.

No email address is collected or shown, following the spec: bitmagnet's email
verification is inert, so an address here would imply a verification nothing performs.
`InviteInput.email` is optional, so nothing is lost by omitting it.

-}

import ApiError
import Bitmagnet
import Format
import Graphql.Http
import Graphql.Operation exposing (RootMutation, RootQuery)
import Graphql.OptionalArgument as Opt
import Graphql.SelectionSet as SelectionSet exposing (SelectionSet)
import Html exposing (Html, a, button, div, h1, h2, label, li, option, p, span, text, ul)
import Html.Attributes exposing (attribute, class, disabled, for, href, id, selected, type_, value)
import Html.Events exposing (onClick, onInput, onSubmit)
import Identity
import Magnes.Api.InputObject as InputObject
import Magnes.Api.Mutation as Mutation
import Magnes.Api.Object
import Magnes.Api.Object.AuthMutation as AuthMutation
import Magnes.Api.Object.AuthQuery as AuthQuery
import Magnes.Api.Object.Invitation as ApiInvitation
import Magnes.Api.Object.ListInvitationsResult as ListInvitationsResult
import Magnes.Api.Object.Role as ApiRole
import Magnes.Api.Object.User as ApiUser
import Magnes.Api.Query as Query
import Route
import Time


type alias Invitation =
    { code : String
    , role : String
    , createdBy : Maybe String
    , claimedBy : Maybe String
    , expiresAt : Maybe Time.Posix
    , createdAt : Time.Posix
    }


{-| One page of Invitations, plus the Roles one may be created for. They arrive together
because they are needed together and neither is useful alone: a list nobody may add to,
or a form offering Roles for a list that failed to load.
-}
type alias Page =
    { invitations : List Invitation
    , totalCount : Int
    , roles : List String
    }


type Listing
    = Loading
    | Failed ApiError.Failure
    | Loaded Page


type Submission
    = Ready
    | Submitting
    | Rejected ApiError.Failure
    | Created Invitation


{-| What is on screen. `confirming` holds the code whose withdrawal has been asked about
but not yet confirmed — one at a time, because arming several at once is how the wrong
one gets clicked.
-}
type alias State =
    { listing : Listing
    , offset : Int
    , role : String
    , expiry : String
    , creation : Submission
    , confirming : Maybe String
    , withdrawal : Maybe ApiError.Failure
    }


{-| `user` is the default Role because it is the one an ordinary Invitation is for, and
because it is a core Role that always exists — the form is built before the server has
said which Roles there are.
-}
empty : State
empty =
    { listing = Loading
    , offset = 0
    , role = "user"
    , expiry = ""
    , creation = Ready
    , confirming = Nothing
    , withdrawal = Nothing
    }


withListing : Listing -> State -> State
withListing listing state =
    { state | listing = listing }


withOffset : Int -> State -> State
withOffset offset state =
    { state | offset = offset }


withRole : String -> State -> State
withRole role state =
    { state | role = role, creation = Ready }


withExpiry : String -> State -> State
withExpiry chosen state =
    { state | expiry = chosen, creation = Ready }


withSubmission : Submission -> State -> State
withSubmission creation state =
    { state | creation = creation }


{-| Arming a withdrawal also clears the last one's failure: the message described a
different attempt on a different Invitation.
-}
withConfirming : Maybe String -> State -> State
withConfirming code state =
    { state | confirming = code, withdrawal = Nothing }


withWithdrawal : Maybe ApiError.Failure -> State -> State
withWithdrawal failure state =
    { state | withdrawal = failure }


{-| What an Invitation may be given for, as labels and the Go duration strings bitmagnet
parses with `time.ParseDuration` — not seconds, and not ISO 8601.

A fixed set rather than a free-text field. Every value here is one `time.ParseDuration`
accepts, which a typed one need not be, and these are the spans anyone actually wants.

-}
expiries : List ( String, String )
expiries =
    [ ( "Never", "" )
    , ( "24 hours", "24h0m0s" )
    , ( "7 days", "168h0m0s" )
    , ( "30 days", "720h0m0s" )
    ]


pageSize : Int
pageSize =
    50


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


{-| Where the Invitation is meant to be used. Built through `Route.toHref`, so it carries
the mount: a Magnes served under `/magnes` hands out `/magnes/register?code=…`, and a link
that dropped the prefix would 404 for whoever received it.
-}
registrationLink : Route.BasePath -> Invitation -> String
registrationLink mount entry =
    Route.toHref mount (Route.Register { code = Just entry.code })



-- REQUESTS


fetch : String -> Int -> (Result (Graphql.Http.Error Page) Page -> msg) -> Cmd msg
fetch apiUrl offset toMsg =
    page offset
        |> Bitmagnet.queryRequest apiUrl
        |> Graphql.Http.send toMsg


page : Int -> SelectionSet Page RootQuery
page offset =
    Query.auth
        (SelectionSet.map2 (\found roles -> { invitations = found.invitations, totalCount = found.totalCount, roles = roles })
            (AuthQuery.listInvitations
                (\optional ->
                    { optional
                        | input =
                            Opt.Present
                                (InputObject.buildListInvitationsInput
                                    (\input ->
                                        { input
                                            | pagination =
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
                (SelectionSet.map2 (\invitations totalCount -> { invitations = invitations, totalCount = totalCount })
                    (ListInvitationsResult.invitations invitation)
                    ListInvitationsResult.totalCount
                )
            )
            (AuthQuery.listRoles ApiRole.name)
        )


invitation : SelectionSet Invitation Magnes.Api.Object.Invitation
invitation =
    SelectionSet.map6 Invitation
        ApiInvitation.code
        ApiInvitation.role
        (ApiInvitation.createdBy ApiUser.username)
        (ApiInvitation.claimedBy ApiUser.username)
        ApiInvitation.expiresAt
        ApiInvitation.createdAt


create : String -> { role : String, expiry : String } -> (Result (Graphql.Http.Error Invitation) Invitation -> msg) -> Cmd msg
create apiUrl wanted toMsg =
    invite wanted
        |> Bitmagnet.mutationRequest apiUrl
        |> Graphql.Http.send toMsg


{-| An empty expiry is `Absent`, not an empty Duration: bitmagnet reads a missing expiry
as "never" and would fail to parse `""`.
-}
invite : { role : String, expiry : String } -> SelectionSet Invitation RootMutation
invite wanted =
    Mutation.auth
        (AuthMutation.invite
            { input =
                InputObject.buildInviteInput
                    (\input ->
                        { input
                            | role = Opt.Present wanted.role
                            , expiry =
                                if String.isEmpty wanted.expiry then
                                    Opt.Absent

                                else
                                    Opt.Present wanted.expiry
                        }
                    )
            }
            invitation
        )


withdraw : String -> String -> (Result (Graphql.Http.Error ()) () -> msg) -> Cmd msg
withdraw apiUrl code toMsg =
    Mutation.auth (AuthMutation.deleteInvitation { code = code } |> SelectionSet.map (always ()))
        |> Bitmagnet.mutationRequest apiUrl
        |> Graphql.Http.send toMsg



-- VIEW


type alias Messages msg =
    { roleChosen : String -> msg
    , expiryChosen : String -> msg
    , submitted : msg
    , withdrawRequested : String -> msg
    , withdrawConfirmed : String -> msg
    , withdrawCancelled : msg
    , pageRequested : Int -> msg
    }


view : Route.BasePath -> Time.Zone -> Messages msg -> Identity.Identity -> State -> Html msg
view mount zone messages identity state =
    let
        mayInvite =
            Identity.can (Identity.graphql "auth" "mutate") identity
    in
    div [ class "page" ]
        [ h1 [] [ text "Invitations" ]
        , case state.listing of
            Loading ->
                p [ class "notice" ] [ text "Loading Invitations…" ]

            Failed failure ->
                p [ class "notice error" ] [ text (ApiError.toMessage failure) ]

            Loaded loaded ->
                div []
                    [ if mayInvite then
                        createForm mount messages state loaded.roles

                      else
                        text ""
                    , withdrawalFailure state
                    , listed mount zone messages mayInvite state loaded
                    , paging messages state loaded
                    ]
        ]


{-| The Roles come from `listRoles` rather than being written here, so a Role added on the
server is offered without a client change — and one that does not exist cannot be chosen.
-}
createForm : Route.BasePath -> Messages msg -> State -> List String -> Html msg
createForm mount messages state roles =
    Html.form [ class "panel", onSubmit messages.submitted ]
        [ h2 [] [ text "Invite someone" ]
        , label [ for "invitation-role" ] [ text "Role" ]
        , Html.select
            [ id "invitation-role", onInput messages.roleChosen ]
            (List.map
                (\role ->
                    option [ value role, selected (role == state.role) ] [ text role ]
                )
                roles
            )
        , label [ for "invitation-expiry" ] [ text "Expires" ]
        , Html.select
            [ id "invitation-expiry", onInput messages.expiryChosen ]
            (List.map
                (\( label_, duration ) ->
                    option [ value duration, selected (duration == state.expiry) ] [ text label_ ]
                )
                expiries
            )
        , creationOutcome mount state.creation
        , button
            [ type_ "submit", class "submit", disabled (state.creation == Submitting) ]
            [ text
                (if state.creation == Submitting then
                    "Inviting…"

                 else
                    "Create Invitation"
                )
            ]
        ]


{-| What the last creation did.

A success is announced as loudly as a refusal, because the code is the entire product of
the mutation: once it is one row among fifty, nothing distinguishes the one just made.
`role="status"` rather than `alert` — it is good news, and it should not interrupt.

-}
creationOutcome : Route.BasePath -> Submission -> Html msg
creationOutcome mount creation =
    case creation of
        Rejected failure ->
            p
                [ class "notice error field-error", attribute "role" "alert" ]
                [ text (ApiError.toMessage failure) ]

        Created entry ->
            div [ class "created", attribute "role" "status" ]
                [ p [] [ text ("Invitation created, for a " ++ entry.role ++ ". Send this link:") ]
                , a [ href (registrationLink mount entry) ] [ text (registrationLink mount entry) ]
                ]

        Ready ->
            text ""

        Submitting ->
            text ""


{-| A withdrawal that failed belongs above the list rather than in the row: the row it was
about may not be there any more, since the list is refetched around it.
-}
withdrawalFailure : State -> Html msg
withdrawalFailure state =
    case state.withdrawal of
        Just failure ->
            p
                [ class "notice error", attribute "role" "alert" ]
                [ text (ApiError.toMessage failure) ]

        Nothing ->
            text ""


listed : Route.BasePath -> Time.Zone -> Messages msg -> Bool -> State -> Page -> Html msg
listed mount zone messages mayInvite state loaded =
    if List.isEmpty loaded.invitations then
        p [ class "notice" ] [ text "No Invitations. Anyone registering will need one made here." ]

    else
        ul [ class "invitations" ]
            (List.map (row mount zone messages mayInvite state.confirming) loaded.invitations)


{-| The armed code rather than the whole state: a row's only interest in what else is on
screen is whether it is the one being asked about.
-}
row : Route.BasePath -> Time.Zone -> Messages msg -> Bool -> Maybe String -> Invitation -> Html msg
row mount zone messages mayInvite confirming entry =
    let
        link =
            registrationLink mount entry
    in
    li [ class "invitation" ]
        [ div [ class "invitation-link" ] [ a [ href link ] [ text link ] ]
        , div [ class "invitation-facts" ]
            [ span [ class "invitation-role" ] [ text entry.role ]
            , span [] [ text ("Expires: " ++ expiryOf zone entry) ]
            , span [] [ text ("Created: " ++ Format.date zone entry.createdAt) ]
            , span [] [ text (claim entry) ]
            ]
        , withdrawal messages mayInvite confirming entry
        ]


{-| An Invitation with no expiry is not one whose expiry is unknown, and a blank would
read as the second. bitmagnet leaves `expiresAt` null when none was asked for.
-}
expiryOf : Time.Zone -> Invitation -> String
expiryOf zone entry =
    entry.expiresAt
        |> Maybe.map (Format.dateTime zone)
        |> Maybe.withDefault "Never"


claim : Invitation -> String
claim entry =
    case ( entry.claimedBy, entry.createdBy ) of
        ( Just user, _ ) ->
            "Claimed by " ++ user

        ( Nothing, Just author ) ->
            "Unclaimed, from " ++ author

        ( Nothing, Nothing ) ->
            -- bitmagnet mints one of these itself on first run, when there is no
            -- administrator to attribute it to. It is the way the first admin registers.
            "Unclaimed, created by bitmagnet"


{-| Only an unclaimed Invitation is worth withdrawing: withdrawing a claimed one takes
nothing back, because the User it made already exists.

The first click arms; the second withdraws. A single click is how the wrong row goes.

-}
withdrawal : Messages msg -> Bool -> Maybe String -> Invitation -> Html msg
withdrawal messages mayInvite confirming entry =
    if not mayInvite || entry.claimedBy /= Nothing then
        text ""

    else if confirming == Just entry.code then
        div [ class "invitation-confirm" ]
            [ span [] [ text (warning entry) ]
            , button
                [ type_ "button"
                , class "danger"
                , onClick (messages.withdrawConfirmed entry.code)
                ]
                [ text "Withdraw" ]
            , button
                [ type_ "button", onClick messages.withdrawCancelled ]
                [ text "Keep it" ]
            ]

    else
        button
            [ type_ "button"
            , onClick (messages.withdrawRequested entry.code)
            ]
            [ text "Withdraw" ]


{-| What withdrawing this particular Invitation costs. The Role is named, because an
Invitation decides what its bearer becomes: withdrawing one that would have made an
administrator is not the same act as withdrawing one that would have made a User.

bitmagnet's own first-run Invitation is called out separately. It is the path by which a
first administrator registers and has no author to attribute it to, so withdrawing it
looks more final than it is — bitmagnet mints another at startup while no enabled
administrator exists. Saying so beats both silence and a false alarm.

-}
warning : Invitation -> String
warning entry =
    let
        stake =
            "Withdraw it? Whoever holds this link can no longer register as "
                ++ entry.role
                ++ "."
    in
    if entry.createdBy == Nothing then
        stake ++ " bitmagnet created this one so a first administrator could register; it mints another at startup while no administrator exists."

    else
        stake


paging : Messages msg -> State -> Page -> Html msg
paging messages state loaded =
    let
        previous =
            previousOffset state.offset

        next =
            nextOffset { offset = state.offset, totalCount = loaded.totalCount }
    in
    if previous == Nothing && next == Nothing then
        text ""

    else
        div [ class "paging" ]
            [ pageButton messages "Previous" previous
            , span [ class "paging-count" ]
                [ text (Format.count loaded.totalCount ++ " in total") ]
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
