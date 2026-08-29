port module Main exposing (main)

import ApiError
import Bitmagnet exposing (Page, Row)
import Browser
import Browser.Dom
import Browser.Events
import Browser.Navigation as Nav
import Facet
import FileTree
import Format
import Graphql.Http
import Html exposing (Attribute, Html, a, button, div, form, h1, header, input, main_, p, span, text)
import Html.Attributes exposing (attribute, class, classList, href, id, placeholder, spellcheck, type_, value)
import Html.Events exposing (on, onClick, onInput, onSubmit, stopPropagationOn)
import Identity
import InfiniteList
import Json.Decode as Decode exposing (Value)
import Login
import Magnes.Api.Enum.ContentType as ContentType
import Magnes.Api.Enum.FilesStatus exposing (FilesStatus(..))
import Process
import Route exposing (Route)
import Set exposing (Set)
import Sort exposing (Sort)
import Svg
import Svg.Attributes as SvgAttr
import Task
import Time
import Url exposing (Url)
import UserOverview


main : Program Flags Model Msg
main =
    Browser.application
        { init = init
        , view = view
        , update = update
        , subscriptions = subscriptions
        , onUrlRequest = LinkClicked
        , onUrlChange = UrlChanged
        }


{-| The API address and mount path arrive at runtime from `index.html`, so the same build
works against any instance and at either the origin root or a static subpath.
-}
type alias Flags =
    { apiUrl : String
    , basePath : String
    }


type alias Model =
    { key : Nav.Key
    , apiUrl : String
    , basePath : Route.BasePath
    , route : Route
    , identity : Identity.Identity
    , identityEpoch : Int
    , field : String
    , results : Results
    , infiniteList : InfiniteList.Model
    , viewportHeight : Int
    , zone : Time.Zone

    -- Purely presentational, so it is not in the URL. It opens itself when a link arrives
    -- with filters already applied, so a shared search shows what is narrowing it.
    , filtersOpen : Bool

    -- Bumped when the searched-for query actually changes. Responses carry the epoch they
    -- were asked under, so a page that arrives after the query moved on is dropped rather
    -- than appended to the wrong list.
    , epoch : Int

    -- The login form. It lives here rather than being rebuilt per render so a keystroke
    -- does not lose what is typed, and it is cleared on success so a password stops
    -- existing in the model the moment it stops being needed.
    , login : Login.Form

    -- How the sign-out request on the User overview is going. It is a request, not a
    -- local erasure: bitmagnet owns the cookie, so until it answers the User is still
    -- signed in and the page has to be able to say a refusal out loud.
    , signOut : UserOverview.SignOut

    -- Why the Identity in flight is being fetched. It is read only to word a failure —
    -- "could not resolve who you are" is the wrong sentence immediately after someone
    -- deliberately signed out.
    , identityRefresh : Refresh

    -- Separate counter for the debounce, because a keystroke is not yet a new query.
    -- Bumping the epoch here would strand an in-flight page — the reply would be dropped
    -- while `fetching` stayed true, and infinite scroll would quietly stop.
    , typing : Int
    }


{-| What prompted the Identity currently being fetched.

Only the failure sentence depends on it. After a successful sign-out bitmagnet has already
cleared the cookie, so a refresh that then fails has not left the User signed in and must
not read as though resolving them failed: what is unknown is only what this instance
allows without a User.

-}
type Refresh
    = Resolving
    | AfterSignOut


{-| Hand-rolled rather than `RemoteData`: these are the states Magnes actually has, and
`Failed` carries a sentence to show rather than an error to interpret.
-}
type Results
    = Blank
    | Loading
    | Failed String
    | Feed FeedState


type alias FeedState =
    { items : List Item
    , ids : Set String
    , total : Int
    , totalIsEstimate : Bool
    , hasNextPage : Bool
    , fetching : Bool

    -- Counts for the content-type chips, from the first page only. Later pages leave them
    -- alone rather than replacing them with the nothing they asked for.
    , contentTypes : List ( Facet.ContentFilter, Int )
    }


{-| A row plus its own display state.

Expansion lives on the record held in the model rather than being zipped together in the
view. `InfiniteList` wraps `itemView` in `lazy3`, which compares the item by reference, so
building wrapper records during render would allocate fresh ones every frame and quietly
defeat it. The formatted date is precomputed here for the same reason: it keeps `itemView`
a top-level function instead of a closure over the time zone.

-}
type alias Item =
    { row : Row
    , published : String
    , expanded : Bool
    , files : Files
    }


type Files
    = Unopened
    | Fetching
    | FilesFailed String
    | Open OpenFiles


{-| The tree and the omission note are built once, when the files arrive, rather than on
every render — the same reason the formatted date lives on `Item`.
-}
type alias OpenFiles =
    { root : FileTree.Node
    , collapsed : Set String
    , omission : String
    }


pageSize : Int
pageSize =
    50


{-| These four must match the stylesheet. The virtualizer positions every row from the
numbers below, so a disagreement with the rendered heights shows up as drift.
-}
rowHeight : Int
rowHeight =
    34


{-| Two lines — the facts and the info hash — plus a third only when the raw torrent name
says something the title doesn't. Heights vary per item anyway, so an always-drawn empty
line to keep this constant would just be a visible gap.
-}
metaHeight : Item -> Int
metaHeight item =
    if showsRawName item then
        66

    else
        48


showsRawName : Item -> Bool
showsRawName item =
    item.row.name /= item.row.title


fileHeight : Int
fileHeight =
    22


noticeHeight : Int
noticeHeight =
    26


{-| How close to the bottom triggers the next page — roughly a screen of lead time.
-}
loadMoreMargin : Float
loadMoreMargin =
    600


{-| Quiet period before a keystroke becomes a search.
-}
debounceMs : Float
debounceMs =
    300


init : Flags -> Url -> Nav.Key -> ( Model, Cmd Msg )
init flags url key =
    let
        basePath =
            Route.basePath flags.basePath

        route =
            Route.fromUrl basePath url

        results =
            pendingResults route
    in
    ( { key = key
      , apiUrl = flags.apiUrl
      , basePath = basePath
      , route = route
      , identity = Identity.Unknown
      , identityEpoch = 0
      , field = fieldFor route
      , results = results
      , infiniteList = InfiniteList.init
      , viewportHeight = 800
      , zone = Time.utc
      , filtersOpen = not (Facet.isEmpty (filtersFor route))
      , login = Login.empty
      , signOut = UserOverview.Ready
      , identityRefresh = Resolving
      , epoch = 0
      , typing = 0
      }
    , Cmd.batch
        [ Identity.fetch flags.apiUrl (GotIdentity 0)
        , Task.perform (\vp -> Resized (round vp.viewport.height)) Browser.Dom.getViewport
        , Task.perform GotZone Time.here
        ]
    )


filtersFor : Route -> Facet.Filters
filtersFor route =
    case route of
        Route.Search params ->
            params.filters

        _ ->
            Facet.empty


fieldFor : Route -> String
fieldFor route =
    case route of
        Route.Search params ->
            Maybe.withDefault "" params.q

        _ ->
            ""


subscriptions : Model -> Sub Msg
subscriptions _ =
    Sub.batch
        [ Browser.Events.onResize (\_ height -> Resized height)
        , Browser.Events.onVisibilityChange VisibilityChanged
        , authenticationChanges (\_ -> AuthenticationChanged)
        ]


{-| Notify the other Magnes tabs after browser login or logout. The value carries no
credential; bitmagnet's HttpOnly cookie remains unreadable to JavaScript and Elm.
-}
port authenticationChanged : () -> Cmd msg


{-| A different tab changed browser authentication, so permission-sensitive state must
be discarded and reloaded under the credential the browser now supplies.
-}
port authenticationChanges : (() -> msg) -> Sub msg


type Msg
    = LinkClicked Browser.UrlRequest
    | UrlChanged Url
    | FieldChanged String
    | DebounceElapsed Int
    | Submitted
    | SortChanged String
    | FiltersToggled
    | FilterChanged Facet.Filters
    | Scrolled Value
    | Resized Int
    | GotZone Time.Zone
    | ToggleExpanded String
    | ToggleFiles String
    | ToggleFolder String String
    | GotFiles String (Result (Graphql.Http.Error Bitmagnet.FileList) Bitmagnet.FileList)
    | GotResults Int (Result (Graphql.Http.Error Page) Page)
    | GotIdentity Int (Result (Graphql.Http.Error Identity.Identity) Identity.Identity)
    | VisibilityChanged Browser.Events.Visibility
    | LoginUsernameChanged String
    | LoginPasswordChanged String
    | LoginSubmitted
    | GotLogin (Result (Graphql.Http.Error ()) ())
    | SignOutRequested
    | GotSignOut (Result (Graphql.Http.Error ()) ())
    | AuthenticationChangedHere Refresh
    | AuthenticationChanged
    | Ignored


update : Msg -> Model -> ( Model, Cmd Msg )
update msg model =
    case msg of
        Ignored ->
            ( model, Cmd.none )

        LoginUsernameChanged username ->
            ( { model | login = Login.withUsername username model.login }, Cmd.none )

        LoginPasswordChanged password ->
            ( { model | login = Login.withPassword password model.login }, Cmd.none )

        LoginSubmitted ->
            if not (Login.canSubmit model.login) then
                ( model, Cmd.none )

            else
                ( { model | login = Login.withState Login.Submitting model.login }
                , Login.submit model.apiUrl (Login.credentials model.login) GotLogin
                )

        GotLogin (Err error) ->
            ( { model | login = Login.withState (Login.Rejected (ApiError.fromError error)) model.login }
            , Cmd.none
            )

        GotLogin (Ok ()) ->
            let
                destination =
                    Route.returnDestination model.basePath model.route

                -- Dropping the form takes the password with it. The rest is exactly what
                -- any other authentication change does — tell the other tabs, and let the
                -- server say what the new Identity may do — so it goes through the same
                -- branch rather than a second copy of it that could drift.
                ( refreshed, refresh ) =
                    update (AuthenticationChangedHere Resolving) { model | login = Login.empty }
            in
            ( refreshed
            , Cmd.batch
                [ Nav.replaceUrl model.key (Route.toHref model.basePath destination)
                , refresh
                ]
            )

        SignOutRequested ->
            if model.signOut == UserOverview.SigningOut then
                ( model, Cmd.none )

            else
                ( { model | signOut = UserOverview.SigningOut }
                , UserOverview.signOut model.apiUrl GotSignOut
                )

        GotSignOut (Ok ()) ->
            signedOut model

        GotSignOut (Err error) ->
            let
                failure =
                    ApiError.fromError error
            in
            if ApiError.isUnauthorized failure then
                -- Refusing to sign out a caller it does not consider signed in is the
                -- outcome that was asked for: whatever the browser is still sending,
                -- bitmagnet no longer accepts it as a User.
                signedOut model

            else
                -- Anything else leaves the cookie where it was, so nothing has changed
                -- and the page says so and offers the action again.
                ( { model | signOut = UserOverview.Refused failure }, Cmd.none )

        AuthenticationChanged ->
            beginIdentityRefresh Resolving model

        AuthenticationChangedHere reason ->
            -- Login and logout will dispatch this after bitmagnet confirms the mutation.
            -- Keeping local state updates in those workflows avoids bouncing our own
            -- notification back through the channel.
            let
                ( refreshing, refresh ) =
                    beginIdentityRefresh reason model
            in
            ( refreshing, Cmd.batch [ authenticationChanged (), refresh ] )

        VisibilityChanged Browser.Events.Visible ->
            ( model, Identity.fetch model.apiUrl (GotIdentity model.identityEpoch) )

        VisibilityChanged Browser.Events.Hidden ->
            ( model, Cmd.none )

        GotIdentity identityEpoch result ->
            if identityEpoch /= model.identityEpoch then
                ( model, Cmd.none )

            else
                case result of
                    Err error ->
                        let
                            message =
                                refreshFailure model.identityRefresh
                                    (ApiError.toMessage (ApiError.fromError error))
                        in
                        ( { model | identity = Identity.Failed message, results = Failed message }
                        , Cmd.none
                        )

                    Ok identity ->
                        identityResolved identity model

        LinkClicked (Browser.Internal url) ->
            case ( Route.fromUrl model.basePath url, model.route ) of
                -- A row's name links to the torrent's own page, which is what makes
                -- middle-click and "open in new tab" work. But an ordinary click on a
                -- result should open it where it already is, so the navigation is turned
                -- back into an expansion. Only from a list: on the torrent page itself the
                -- link is to the page you are on.
                ( Route.Torrent hash, Route.Search _ ) ->
                    ( mapItems
                        (\item ->
                            if item.row.infoHash == hash then
                                { item | expanded = not item.expanded }

                            else
                                item
                        )
                        model
                    , Cmd.none
                    )

                _ ->
                    ( model, Nav.pushUrl model.key (Url.toString url) )

        LinkClicked (Browser.External url) ->
            ( model, Nav.load url )

        UrlChanged url ->
            let
                route =
                    Route.fromUrl model.basePath url
            in
            if route == model.route then
                -- Same query re-submitted; don't throw away results to fetch them again.
                ( model, Cmd.none )

            else
                let
                    epoch =
                        model.epoch + 1

                    ( results, cmd ) =
                        guardedLoad model model.identity epoch route

                    navigated =
                        { model
                            | route = route
                            , field = syncField model route

                            -- A refusal described one attempt on one visit. Leaving the
                            -- page ends it, so coming back does not reopen it as though
                            -- something had just failed.
                            , signOut = UserOverview.Ready
                            , results = results
                            , epoch = epoch
                            , infiniteList = InfiniteList.init
                        }
                in
                ( navigated
                , Cmd.batch
                    [ cmd, scrollListToTop, focusIfLoginAppeared model navigated ]
                )

        FieldChanged field ->
            let
                typing =
                    model.typing + 1
            in
            ( { model | field = field, typing = typing }
            , Process.sleep debounceMs |> Task.perform (\_ -> DebounceElapsed typing)
            )

        DebounceElapsed typing ->
            if typing /= model.typing then
                -- Another keystroke landed; that one owns the search.
                ( model, Cmd.none )

            else
                ( model
                , Nav.replaceUrl model.key (Route.toHref model.basePath (currentSearch model))
                )

        Submitted ->
            ( model, Nav.pushUrl model.key (Route.toHref model.basePath (currentSearch model)) )

        SortChanged raw ->
            -- Choosing an ordering is a deliberate act, so it earns a history entry —
            -- unlike the keystrokes that `replaceUrl` collapses.
            ( model
            , Nav.pushUrl model.key
                (Route.toHref model.basePath (searchRoute model (Sort.fromParam raw) (currentFilters model)))
            )

        FiltersToggled ->
            ( { model | filtersOpen = not model.filtersOpen }, Cmd.none )

        FilterChanged filters ->
            ( model
            , Nav.pushUrl model.key
                (Route.toHref model.basePath (searchRoute model (currentSort model) filters))
            )

        Resized height ->
            ( { model | viewportHeight = height }, Cmd.none )

        GotZone zone ->
            -- Arrives during startup, normally before any results; re-date whatever is
            -- already on screen rather than leaving a stray UTC row behind.
            ( { model | zone = zone }
                |> mapItems (\item -> { item | published = Format.date zone item.row.publishedAt })
            , Cmd.none
            )

        ToggleExpanded rowId ->
            ( mapItem rowId (\item -> { item | expanded = not item.expanded }) model
            , Cmd.none
            )

        ToggleFiles rowId ->
            if not (Identity.can (Identity.graphql "torrent" "query") model.identity) then
                ( model, Cmd.none )

            else
                case itemById rowId model of
                    Just item ->
                        case item.files of
                            Unopened ->
                                ( mapItem rowId (\i -> { i | files = Fetching }) model
                                , Bitmagnet.files item.row.infoHash
                                    |> Bitmagnet.queryRequest model.apiUrl
                                    |> Graphql.Http.send (GotFiles rowId)
                                )

                            _ ->
                                ( mapItem rowId (\i -> { i | files = Unopened }) model, Cmd.none )

                    Nothing ->
                        ( model, Cmd.none )

        GotFiles rowId (Ok fileList) ->
            ( mapItem rowId
                (\item ->
                    { item
                        | files =
                            Open
                                { root = FileTree.root item.row.name fileList.files

                                -- Every folder open. Opening the root is one gesture that
                                -- shows the whole shape; closing them again is per folder.
                                , collapsed = Set.empty
                                , omission = omissions fileList
                                }
                    }
                )
                model
            , Cmd.none
            )

        ToggleFolder rowId key ->
            ( mapItem rowId
                (\item ->
                    case item.files of
                        Open open ->
                            { item
                                | files =
                                    Open
                                        { open
                                            | collapsed =
                                                if Set.member key open.collapsed then
                                                    Set.remove key open.collapsed

                                                else
                                                    Set.insert key open.collapsed
                                        }
                            }

                        _ ->
                            item
                )
                model
            , Cmd.none
            )

        GotFiles rowId (Err error) ->
            onRequestFailure error
                model
                (\message current ->
                    ( mapItem rowId (\item -> { item | files = FilesFailed message }) current
                    , Cmd.none
                    )
                )

        Scrolled event ->
            let
                scrolled =
                    { model | infiniteList = InfiniteList.updateScroll event model.infiniteList }
            in
            if nearBottom event then
                requestMore scrolled

            else
                ( scrolled, Cmd.none )

        GotResults epoch result ->
            if epoch /= model.epoch then
                -- A page for a query the user has already moved on from.
                ( model, Cmd.none )

            else
                case result of
                    Ok page ->
                        let
                            appended =
                                { model | results = append model.zone page model.results }
                        in
                        case model.route of
                            -- The torrent page is the expanded row, standalone: there is
                            -- nothing to scroll and nothing to reveal.
                            Route.Torrent _ ->
                                ( mapItems (\item -> { item | expanded = True }) appended, Cmd.none )

                            _ ->
                                fillViewport appended

                    Err error ->
                        onRequestFailure error
                            model
                            (\message current ->
                                ( { current | results = Failed message }, Cmd.none )
                            )


userOverviewMessages : UserOverview.Messages Msg
userOverviewMessages =
    { signOutRequested = SignOutRequested }


{-| bitmagnet has cleared the cookie, so this is an authentication change like any other:
the other tabs are told and the server is asked what the Identity has become.

The destination is said rather than inferred. The User overview is no longer somewhere
this Identity may be, and the guard answers that by sending it to a login form asking it
to come back — which is the one place someone who just signed out is not going.

-}
signedOut : Model -> ( Model, Cmd Msg )
signedOut model =
    let
        ( refreshed, refresh ) =
            update (AuthenticationChangedHere AfterSignOut)
                { model | signOut = UserOverview.Ready }
    in
    ( refreshed
    , Cmd.batch
        [ Nav.replaceUrl model.key
            (Route.toHref model.basePath (Route.Search Route.emptySearch))
        , refresh
        ]
    )


loginMessages : Login.Messages Msg
loginMessages =
    { usernameChanged = LoginUsernameChanged
    , passwordChanged = LoginPasswordChanged
    , submitted = LoginSubmitted
    }


{-| `autofocus` never fires here: `Browser.application` renders the form as a virtual-DOM
patch rather than a new document. Focus is moved explicitly instead.

What matters is when the form _appears_, which is not the same as when the login route is
entered. Arriving cold on `/login` renders "Resolving Identity…" first, because the guard
cannot decide anything until `self.identity` answers — so focusing on arrival targets an
element that does not exist yet, and silently does nothing. The form appears on whichever
comes second, the navigation or the Identity, so both ask this.

-}
focusIfLoginAppeared : Model -> Model -> Cmd Msg
focusIfLoginAppeared before after =
    if showsLoginForm after && not (showsLoginForm before) then
        Task.attempt (\_ -> Ignored) (Browser.Dom.focus Login.usernameFieldId)

    else
        Cmd.none


showsLoginForm : Model -> Bool
showsLoginForm model =
    case ( model.route, Route.guard model.basePath model.identity model.route ) of
        ( Route.Login _, Route.Allowed ) ->
            True

        _ ->
            False


{-| Every failed request asks the same question first: does this say the Identity Magnes
is holding has gone stale? If so the answer is always to refetch it, and whatever the call
site wanted to show instead never happens. Only once that is ruled out does the call site
get its message.
-}
onRequestFailure :
    Graphql.Http.Error a
    -> Model
    -> (String -> Model -> ( Model, Cmd Msg ))
    -> ( Model, Cmd Msg )
onRequestFailure error model toFailed =
    let
        failure =
            ApiError.fromError error
    in
    if ApiError.isUnauthorized failure then
        beginIdentityRefresh Resolving model

    else
        toFailed (ApiError.toMessage failure) model


{-| The URL is the source of truth for what was searched, but not for what is in the box:
adopting the parsed value mid-typing would eat a trailing space. Only take the URL's
version when it says something different from what is typed — a back button, or a link.
-}
syncField : Model -> Route -> String
syncField model route =
    case route of
        Route.Search params ->
            if trimToMaybe model.field == params.q then
                model.field

            else
                Maybe.withDefault "" params.q

        _ ->
            model.field


{-| The search the box and the menu currently describe. Both the field and the ordering
travel together, so changing one never silently drops the other.
-}
searchRoute : Model -> Sort -> Facet.Filters -> Route
searchRoute model sort filters =
    Route.Search { q = trimToMaybe model.field, sort = sort, filters = filters }


currentSort : Model -> Sort
currentSort model =
    case model.route of
        Route.Search params ->
            params.sort

        _ ->
            Sort.default


currentFilters : Model -> Facet.Filters
currentFilters model =
    filtersFor model.route


{-| The search as the box, the menu and the chips currently describe it. All three travel
together, so changing one never silently drops the others.
-}
currentSearch : Model -> Route
currentSearch model =
    searchRoute model (currentSort model) (currentFilters model)


trimToMaybe : String -> Maybe String
trimToMaybe raw =
    case String.trim raw of
        "" ->
            Nothing

        trimmed ->
            Just trimmed



-- ITEMS


itemById : String -> Model -> Maybe Item
itemById rowId model =
    case model.results of
        Feed feed ->
            List.filter (\item -> item.row.id == rowId) feed.items |> List.head

        _ ->
            Nothing


{-| Rebuilds only the item that changed; every other record keeps its identity, so the
per-item `lazy3` still skips them.
-}
mapItem : String -> (Item -> Item) -> Model -> Model
mapItem rowId change model =
    mapItems
        (\item ->
            if item.row.id == rowId then
                change item

            else
                item
        )
        model


mapItems : (Item -> Item) -> Model -> Model
mapItems change model =
    case model.results of
        Feed feed ->
            { model | results = Feed { feed | items = List.map change feed.items } }

        _ ->
            model



-- FETCHING


pendingResults : Route -> Results
pendingResults route =
    case route of
        Route.NotFound ->
            Blank

        _ ->
            Loading


loadWhenReady : Identity.Identity -> String -> Int -> Route -> ( Results, Cmd Msg )
loadWhenReady identity apiUrl epoch route =
    case identity of
        Identity.Unknown ->
            ( pendingResults route, Cmd.none )

        Identity.Failed message ->
            ( Failed message, Cmd.none )

        _ ->
            if routeNeedsSearch route && not (Identity.can (Identity.graphql "torrentContent" "query") identity) then
                ( Failed "Your Identity does not permit searching torrent content.", Cmd.none )

            else
                load apiUrl epoch route


guardedLoad : Model -> Identity.Identity -> Int -> Route -> ( Results, Cmd Msg )
guardedLoad model identity epoch route =
    case Route.guard model.basePath identity route of
        Route.PendingIdentity ->
            ( pendingResults route, Cmd.none )

        Route.Refused message ->
            ( Failed message, Cmd.none )

        Route.RedirectTo destination ->
            ( Blank
            , Nav.replaceUrl model.key (Route.toHref model.basePath destination)
            )

        Route.Allowed ->
            loadWhenReady identity model.apiUrl epoch route


routeNeedsSearch : Route -> Bool
routeNeedsSearch route =
    case route of
        Route.Search _ ->
            True

        Route.Torrent _ ->
            True

        Route.Login _ ->
            False

        Route.Register _ ->
            False

        Route.UserOverview ->
            False

        Route.APIKeys ->
            False

        Route.AdminUsers ->
            False

        Route.AdminRoles ->
            False

        Route.AdminInvitations ->
            False

        Route.NotFound ->
            False


{-| The sentence for an Identity that could not be fetched, given what asked for it.
-}
refreshFailure : Refresh -> String -> String
refreshFailure reason message =
    case reason of
        Resolving ->
            message

        AfterSignOut ->
            "You are signed out. Magnes could not load what this instance allows without a User: "
                ++ message


beginIdentityRefresh : Refresh -> Model -> ( Model, Cmd Msg )
beginIdentityRefresh reason model =
    let
        epoch =
            model.epoch + 1

        identityEpoch =
            model.identityEpoch + 1
    in
    ( { model
        | identity = Identity.Unknown
        , identityEpoch = identityEpoch
        , identityRefresh = reason

        -- Whatever the Identity turns out to be, it is no longer the one any sign-out in
        -- flight or already refused was about.
        , signOut = UserOverview.Ready
        , results = pendingResults model.route
        , epoch = epoch
        , infiniteList = InfiniteList.init
      }
    , Cmd.batch
        [ Identity.fetch model.apiUrl (GotIdentity identityEpoch)
        , scrollListToTop
        ]
    )


identityResolved : Identity.Identity -> Model -> ( Model, Cmd Msg )
identityResolved identity model =
    let
        ( resolved, cmd ) =
            identityApplied identity model
    in
    -- The reason is spent once an Identity arrives; a later failure is its own event.
    ( { resolved | identityRefresh = Resolving }
    , Cmd.batch [ cmd, focusIfLoginAppeared model resolved ]
    )


identityApplied : Identity.Identity -> Model -> ( Model, Cmd Msg )
identityApplied identity model =
    case model.identity of
        Identity.Unknown ->
            let
                ( results, cmd ) =
                    guardedLoad model identity model.epoch model.route
            in
            ( { model | identity = identity, results = results }, cmd )

        Identity.Failed _ ->
            let
                ( results, cmd ) =
                    guardedLoad model identity model.epoch model.route
            in
            ( { model | identity = identity, results = results }, cmd )

        previous ->
            if previous == identity then
                ( { model | identity = identity }, Cmd.none )

            else
                let
                    epoch =
                        model.epoch + 1

                    ( results, cmd ) =
                        guardedLoad model identity epoch model.route
                in
                ( { model
                    | identity = identity
                    , results = results
                    , epoch = epoch
                    , infiniteList = InfiniteList.init
                  }
                , Cmd.batch [ cmd, scrollListToTop ]
                )


{-| What a route needs fetched, and the state to show until it arrives.
-}
load : String -> Int -> Route -> ( Results, Cmd Msg )
load apiUrl epoch route =
    case route of
        Route.Search params ->
            ( Loading, fetch apiUrl epoch (searchArgs params 0) )

        Route.Torrent infoHash ->
            ( Loading, fetch apiUrl epoch (Bitmagnet.byInfoHash infoHash) )

        Route.Login _ ->
            ( Blank, Cmd.none )

        Route.Register _ ->
            ( Blank, Cmd.none )

        Route.UserOverview ->
            ( Blank, Cmd.none )

        Route.APIKeys ->
            ( Blank, Cmd.none )

        Route.AdminUsers ->
            ( Blank, Cmd.none )

        Route.AdminRoles ->
            ( Blank, Cmd.none )

        Route.AdminInvitations ->
            ( Blank, Cmd.none )

        Route.NotFound ->
            ( Blank, Cmd.none )


searchArgs : Route.SearchParams -> Int -> Bitmagnet.SearchArgs
searchArgs params offset =
    { queryString = params.q
    , infoHashes = []
    , sort = params.sort
    , filters = params.filters

    -- Only the first page asks for facet counts; they do not change as you page through,
    -- and recomputing them over millions of rows on every scroll would be paid for nothing.
    , aggregate = offset == 0
    , limit = pageSize
    , offset = offset
    }


fetch : String -> Int -> Bitmagnet.SearchArgs -> Cmd Msg
fetch apiUrl epoch args =
    Bitmagnet.search args
        |> Bitmagnet.queryRequest apiUrl
        |> Graphql.Http.send (GotResults epoch)


{-| The next page, if there is one and none is already in flight. `fetching` is the guard
that keeps a burst of scroll events from firing a dozen identical requests.
-}
requestMore : Model -> ( Model, Cmd Msg )
requestMore model =
    case ( model.results, model.route ) of
        ( Feed feed, Route.Search params ) ->
            if feed.hasNextPage && not feed.fetching then
                ( { model | results = Feed { feed | fetching = True } }
                , fetch model.apiUrl model.epoch (searchArgs params (List.length feed.items))
                )

            else
                ( model, Cmd.none )

        _ ->
            ( model, Cmd.none )


{-| A batch that doesn't reach the bottom of the container can never be scrolled, so it
can never ask for the next one. Keep pulling until there is something to scroll.
-}
fillViewport : Model -> ( Model, Cmd Msg )
fillViewport model =
    case model.results of
        Feed feed ->
            if List.length feed.items * rowHeight < listHeight model then
                requestMore model

            else
                ( model, Cmd.none )

        _ ->
            ( model, Cmd.none )


{-| A new search reuses the scroll container, and the browser keeps its `scrollTop`. The
virtualizer's model is reset in step with it, so without this the list would render from
the old offset into a list that no longer has those rows.
-}
scrollListToTop : Cmd Msg
scrollListToTop =
    Browser.Dom.setViewportOf "results" 0 0 |> Task.attempt (\_ -> Ignored)


{-| Appends a page, dropping rows already shown. Offset paging over a live crawler
re-serves rows: new torrents arrive while you scroll and shift everything down.
-}
append : Time.Zone -> Page -> Results -> Results
append zone page results =
    let
        base =
            case results of
                Feed feed ->
                    feed

                _ ->
                    { items = [], ids = Set.empty, total = 0, totalIsEstimate = False, hasNextPage = False, fetching = False, contentTypes = [] }

        fresh =
            page.items
                |> List.filter (\row -> not (Set.member row.id base.ids))
                |> List.map
                    (\row ->
                        { row = row
                        , published = Format.date zone row.publishedAt
                        , expanded = False
                        , files = Unopened
                        }
                    )
    in
    Feed
        { base
            | items = base.items ++ fresh
            , ids = List.foldl (\item -> Set.insert item.row.id) base.ids fresh
            , total = page.totalCount
            , totalIsEstimate = page.totalCountIsEstimate
            , hasNextPage = page.hasNextPage
            , fetching = False
            , contentTypes = Maybe.withDefault base.contentTypes page.contentTypes
        }


{-| The scroll event is decoded twice from one handler — once by the virtualizer to work
out what to render, once here to work out whether to fetch. Elm allows one `on "scroll"`
per node, so the two questions share a listener.
-}
nearBottom : Value -> Bool
nearBottom event =
    let
        decoder =
            Decode.map3 (\top height client -> top + client >= height - loadMoreMargin)
                (Decode.at [ "target", "scrollTop" ] Decode.float)
                (Decode.at [ "target", "scrollHeight" ] Decode.float)
                (Decode.at [ "target", "clientHeight" ] Decode.float)
    in
    Decode.decodeValue decoder event |> Result.withDefault False



-- VIEW


{-| The virtualizer needs the container's height as a number, but the stylesheet is what
actually sizes it — a flex child filling whatever the header leaves. The window height is
therefore an overestimate, which the package documents as costing only a few extra
rendered rows, and it avoids a second copy of the layout arithmetic that would silently
go stale.
-}
listHeight : Model -> Int
listHeight model =
    model.viewportHeight


view : Model -> Browser.Document Msg
view model =
    { title = documentTitle model
    , body =
        [ header []
            [ div [ class "bar" ]
                [ a [ class "wordmark", href (Route.toHref model.basePath (Route.Search Route.emptySearch)) ]
                    [ h1 [] [ text "magnes" ] ]
                , searchBox model
                , identityLink model
                ]
            , viewFilters model
            ]
        , main_ [] [ viewRoute model ]
        ]
    }


documentTitle : Model -> String
documentTitle model =
    case model.route of
        Route.Search { q } ->
            Maybe.map (\term -> term ++ " — magnes") q |> Maybe.withDefault "magnes"

        Route.Torrent infoHash ->
            case model.results of
                Feed { items } ->
                    case items of
                        item :: _ ->
                            item.row.title ++ " — magnes"

                        [] ->
                            infoHash ++ " — magnes"

                _ ->
                    infoHash ++ " — magnes"

        Route.Login _ ->
            "sign in — magnes"

        Route.Register _ ->
            "register User — magnes"

        Route.UserOverview ->
            "User — magnes"

        Route.APIKeys ->
            "API keys — magnes"

        Route.AdminUsers ->
            "Users — magnes"

        Route.AdminRoles ->
            "Roles — magnes"

        Route.AdminInvitations ->
            "Invitations — magnes"

        Route.NotFound ->
            "not found — magnes"


{-| The way to the User overview, and the only thing in the chrome that changes with the
Identity. An Anonymous Identity is offered the way in instead, remembering where it was so
signing in returns here rather than to the front page.

An API-key Identity reports an owning User but may not manage it, and the guard refuses it
the overview, so it is offered the same way in as an Anonymous one. Nothing is drawn while
the Identity is unknown or failed: there is no answer to give yet, and guessing would make
the header flicker between two states on every load.

-}
identityLink : Model -> Html Msg
identityLink model =
    case model.identity of
        Identity.UserAuthenticated user _ ->
            a
                [ class "identity"
                , href (Route.toHref model.basePath Route.UserOverview)
                ]
                [ text user.username ]

        Identity.Anonymous _ ->
            signInLink model

        Identity.APIKeyAuthenticated _ _ _ ->
            signInLink model

        Identity.Unknown ->
            text ""

        Identity.Failed _ ->
            text ""


signInLink : Model -> Html Msg
signInLink model =
    a
        [ class "identity"
        , href
            (Route.toHref model.basePath
                (Route.Login { returnUrl = returnHere model })
            )
        ]
        [ text "Sign in" ]


{-| Where signing in should come back to. A login form that returns to itself is no
return at all, and neither is one that returns to a page that was not found.
-}
returnHere : Model -> Maybe String
returnHere model =
    case model.route of
        Route.Login _ ->
            Nothing

        Route.Register _ ->
            Nothing

        Route.NotFound ->
            Nothing

        route ->
            Just (Route.toHref model.basePath route)


searchBox : Model -> Html Msg
searchBox model =
    form [ class "search", onSubmit Submitted ]
        [ input
            [ type_ "search"
            , placeholder "search the index"
            , value model.field
            , spellcheck False
            , attribute "autocomplete" "off"
            , onInput FieldChanged
            ]
            []
        , syntaxHint
        , sortMenu (currentSort model)
        , filtersButton model
        ]


{-| bitmagnet's query language, as a native tooltip. Everything it documents fits in six
lines, and most of it is the syntax people already expect from a search box — so it is a
reminder, not a manual, and it stays out of the way until pointed at.
-}
syntaxHint : Html Msg
syntaxHint =
    span
        [ class "hint"
        , attribute "title" syntaxSummary
        , attribute "aria-label" syntaxSummary
        , attribute "tabindex" "0"
        ]
        [ text "?" ]


syntaxSummary : String
syntaxSummary =
    String.join "\n"
        [ "Search syntax"
        , ""
        , "\"exact phrase\"   these words, in this order"
        , "linux | bsd     either term"
        , "!term           exclude the term"
        , "appl*           starts with (suffix only)"
        , "( )             group, to control precedence"
        , "a . b           b immediately after a"
        , ""
        , "Case and punctuation are ignored."
        ]


filtersButton : Model -> Html Msg
filtersButton model =
    let
        active =
            Facet.count (currentFilters model)
    in
    button
        [ class "filters-toggle"
        , classList [ ( "open", model.filtersOpen ), ( "active", active > 0 ) ]
        , type_ "button"
        , attribute "aria-expanded"
            (if model.filtersOpen then
                "true"

             else
                "false"
            )
        , onClick FiltersToggled
        ]
        [ text
            (if active == 0 then
                "filters"

             else
                "filters (" ++ String.fromInt active ++ ")"
            )
        ]


{-| Two facets out of the nine bitmagnet offers — see `Facet` for why the other seven are
not drawn. Content type comes from the aggregation, so it lists only what this search
actually contains and carries counts; file type is the fixed enum, drawn in full and
without counts.

A selected value is always shown even when the aggregation stops returning it, which it
does as soon as it is the only thing selected — otherwise the chip you just clicked would
vanish and leave no way to unclick it.

-}
viewFilters : Model -> Html Msg
viewFilters model =
    let
        filters =
            currentFilters model

        buckets =
            case model.results of
                Feed feed ->
                    feed.contentTypes

                _ ->
                    []

        listed =
            List.map Tuple.first buckets

        contentValues =
            buckets ++ List.map (\value -> ( value, 0 )) (List.filter (\v -> not (List.member v listed)) filters.content)
    in
    if not model.filtersOpen then
        text ""

    else
        div [ class "facets" ]
            [ viewFacet "kind"
                (List.map
                    (\( value, n ) ->
                        chip (Facet.contentLabel value)
                            (Just n)
                            (List.member value filters.content)
                            (FilterChanged (Facet.toggleContent value filters))
                    )
                    contentValues
                )
            , viewFacet "files"
                (List.map
                    (\value ->
                        chip (Facet.fileLabel value)
                            Nothing
                            (List.member value filters.files)
                            (FilterChanged (Facet.toggleFile value filters))
                    )
                    Facet.fileTypes
                )
            , if Facet.isEmpty filters then
                text ""

              else
                button
                    [ class "clear", type_ "button", onClick (FilterChanged Facet.empty) ]
                    [ text "clear filters" ]
            ]


viewFacet : String -> List (Html Msg) -> Html Msg
viewFacet label chips =
    if List.isEmpty chips then
        text ""

    else
        div [ class "facet" ]
            [ span [ class "facet-label" ] [ text label ]
            , div [ class "chips" ] chips
            ]


chip : String -> Maybe Int -> Bool -> Msg -> Html Msg
chip label maybeCount selected msg =
    button
        [ class "chip"
        , classList [ ( "on", selected ) ]
        , type_ "button"
        , attribute "aria-pressed"
            (if selected then
                "true"

             else
                "false"
            )
        , onClick msg
        ]
        (text label
            :: (case maybeCount of
                    Just n ->
                        [ span [ class "chip-count" ] [ text (Format.count n) ] ]

                    Nothing ->
                        []
               )
        )


{-| A plain `select`, so it is a real form control: keyboard-operable, and rendered by the
platform rather than reimplemented.
-}
sortMenu : Sort -> Html Msg
sortMenu selected =
    Html.select
        [ class "sort"
        , attribute "aria-label" "Sort results"
        , onInput SortChanged
        ]
        (List.map
            (\sort ->
                Html.option
                    [ value (Sort.toParam sort)
                    , Html.Attributes.selected (sort == selected)
                    ]
                    [ text (Sort.label sort) ]
            )
            Sort.all
        )


viewRoute : Model -> Html Msg
viewRoute model =
    case Route.guard model.basePath model.identity model.route of
        Route.PendingIdentity ->
            p [ class "notice" ] [ text "Resolving Identity…" ]

        Route.Refused message ->
            p [ class "notice error" ] [ text message ]

        Route.RedirectTo _ ->
            p [ class "notice" ] [ text "Redirecting…" ]

        Route.Allowed ->
            viewAllowedRoute model


viewAllowedRoute : Model -> Html Msg
viewAllowedRoute model =
    case model.route of
        Route.Search _ ->
            viewResults model

        Route.Torrent _ ->
            viewTorrent model

        Route.Login _ ->
            Login.view loginMessages model.login

        Route.Register _ ->
            p [ class "notice" ] [ text "Register User." ]

        Route.UserOverview ->
            case model.identity of
                Identity.UserAuthenticated user _ ->
                    UserOverview.view model.zone userOverviewMessages model.signOut user

                -- Unreachable: the guard admits no other Identity to this route, and has
                -- already answered them with a redirect.
                _ ->
                    p [ class "notice" ] [ text "Resolving Identity…" ]

        Route.APIKeys ->
            p [ class "notice" ] [ text "API keys." ]

        Route.AdminUsers ->
            p [ class "notice" ] [ text "User administration." ]

        Route.AdminRoles ->
            p [ class "notice" ] [ text "Role administration." ]

        Route.AdminInvitations ->
            p [ class "notice" ] [ text "Invitation administration." ]

        Route.NotFound ->
            p [ class "notice" ] [ text "No such page." ]


{-| The same expanded row the list draws, on its own — so a torrent can be linked without
linking the search that found it.
-}
viewTorrent : Model -> Html Msg
viewTorrent model =
    case model.results of
        Feed feed ->
            case feed.items of
                item :: _ ->
                    div [ class "single" ] [ viewItem model.basePath (canQueryFiles model) item ]

                [] ->
                    p [ class "notice" ] [ text "No torrent with that hash is in the index." ]

        Loading ->
            p [ class "notice" ] [ text "Looking it up…" ]

        Failed message ->
            p [ class "notice error" ] [ text message ]

        Blank ->
            p [ class "notice" ] [ text "No torrent with that hash is in the index." ]


viewResults : Model -> Html Msg
viewResults model =
    case model.results of
        Blank ->
            p [ class "notice" ] [ text "Search the index, or press enter to browse everything." ]

        Loading ->
            p [ class "notice" ] [ text "Searching…" ]

        Failed message ->
            p [ class "notice error" ] [ text message ]

        Feed feed ->
            if List.isEmpty feed.items then
                p [ class "notice" ] [ text "Nothing matched." ]

            else
                div [ class "results" ]
                    [ p [ class "tally" ] [ text (tally feed) ]
                    , div
                        [ class "list"
                        , id "results"
                        , on "scroll" (Decode.map Scrolled Decode.value)
                        ]
                        [ InfiniteList.view (listConfig model) model.infiniteList feed.items ]
                    ]


listConfig : Model -> InfiniteList.Config Item Msg
listConfig model =
    InfiniteList.config
        { itemView = \_ _ item -> viewItem model.basePath (canQueryFiles model) item
        , itemHeight = InfiniteList.withVariableHeight (\_ item -> itemHeight (canQueryFiles model) item)
        , containerHeight = listHeight model
        }


canQueryFiles : Model -> Bool
canQueryFiles model =
    Identity.can (Identity.graphql "torrent" "query") model.identity


{-| Told to the virtualizer, so it has to be what the browser actually lays out.
-}
itemHeight : Bool -> Item -> Int
itemHeight canQuery item =
    if item.expanded then
        rowHeight + metaHeight item + filesHeight canQuery item

    else
        rowHeight


{-| Counted off `viewFiles`'s own list, so the two cannot disagree.
-}
filesHeight : Bool -> Item -> Int
filesHeight canQuery item =
    case item.row.filesStatus of
        Over_threshold ->
            noticeHeight

        Multi ->
            if not canQuery then
                0

            else
                (fileHeight * List.length (fileEntries item))
                    + (case item.files of
                        Fetching ->
                            noticeHeight

                        FilesFailed _ ->
                            noticeHeight

                        Open open ->
                            if open.omission == "" then
                                0

                            else
                                noticeHeight

                        Unopened ->
                            0
                      )

        _ ->
            0


{-| The tree's rows. Before the files arrive there is still one: the torrent's own folder,
closed, which is what you click to fetch them.
-}
fileEntries : Item -> List FileTree.Entry
fileEntries item =
    case item.files of
        Open open ->
            FileTree.flatten open.collapsed open.root

        _ ->
            [ { depth = 0
              , key = item.row.name
              , name = item.row.name
              , size = item.row.size
              , isFolder = True
              , collapsed = True
              }
            ]


{-| The count is an estimate on broad queries and exact on narrow ones, and the response
says which — so say which, rather than hedging every figure.
-}
tally : FeedState -> String
tally feed =
    let
        counted =
            Format.count feed.total ++ " " ++ plural feed.total "result"
    in
    if feed.totalIsEstimate then
        "about " ++ counted

    else
        counted


plural : Int -> String -> String
plural n noun =
    if n == 1 then
        noun

    else
        noun ++ "s"


viewItem : Route.BasePath -> Bool -> Item -> Html Msg
viewItem basePath canQuery item =
    div [ class "item" ]
        (viewRow basePath item
            :: (if item.expanded then
                    viewMeta item ++ viewFiles canQuery item

                else
                    []
               )
        )


{-| One line: chevron, name, size, magnet. Everything else is behind the chevron — on a
real index most rows have no metadata, so a column of it is a column of blanks.
-}
viewRow : Route.BasePath -> Item -> Html Msg
viewRow basePath item =
    div [ class "row", onRowClick item.row.id ]
        [ button
            [ class "twist"
            , classList [ ( "open", item.expanded ) ]
            , attribute "aria-expanded"
                (if item.expanded then
                    "true"

                 else
                    "false"
                )
            , attribute "aria-label" ("Details for " ++ item.row.title)
            , type_ "button"

            -- The row already toggles; without stopping here the click would be counted
            -- twice and cancel itself out. The button stays for the keyboard, and as the
            -- thing that shows the state.
            , stopPropagationOn "click" (Decode.succeed ( ToggleExpanded item.row.id, True ))
            ]
            [ chevronIcon ]
        , a
            [ class "name"
            , href (Route.toHref basePath (Route.Torrent item.row.infoHash))

            -- Elm installs its own click listener on every anchor, and it runs before
            -- anything on an ancestor — so the row's handler would fire *as well*, toggling
            -- twice and appearing to do nothing. The anchor's click is handled entirely by
            -- `LinkClicked`; this only keeps it from reaching the row.
            , stopPropagationOn "click" (Decode.succeed ( Ignored, True ))
            ]
            [ text item.row.title ]
        , span [ class "size" ] [ text (Format.bytes item.row.size) ]
        , a
            [ class "magnet"
            , href item.row.magnetUri
            , attribute "aria-label" ("Magnet link for " ++ item.row.title)
            , attribute "title" "magnet link"

            -- The one thing in the row that is not "expand": let the click reach the
            -- browser as an ordinary link, but keep it away from the row's handler.
            , stopPropagationOn "click" (Decode.succeed ( Ignored, True ))
            ]
            [ magnetIcon ]
        ]


{-| A plain click anywhere on the line expands it, including on the name — which is still
a real link, so the ways of opening a link elsewhere all keep working.

Those are the cases deliberately left alone. A modified click (ctrl, cmd, shift, alt) is
the browser's "open this somewhere else" and falls through to `Browser.application`, which
skips modified clicks itself and lets the browser have them. Middle click fires `auxclick`
rather than `click` and so never arrives here at all.

`stopPropagation` matters as much as `preventDefault`: `Browser.application` listens for
clicks on the document, and preventing the default alone would not stop it from turning
this into a navigation.

-}
onRowClick : String -> Attribute Msg
onRowClick rowId =
    Html.Events.custom "click"
        (Decode.map4
            (\ctrl meta shift alt ->
                if ctrl || meta || shift || alt then
                    { message = Ignored, stopPropagation = False, preventDefault = False }

                else
                    { message = ToggleExpanded rowId, stopPropagation = True, preventDefault = True }
            )
            (Decode.field "ctrlKey" Decode.bool)
            (Decode.field "metaKey" Decode.bool)
            (Decode.field "shiftKey" Decode.bool)
            (Decode.field "altKey" Decode.bool)
        )


viewMeta : Item -> List (Html Msg)
viewMeta item =
    let
        row =
            item.row

        facts =
            List.filterMap identity
                [ Just item.published
                , Maybe.map (\ct -> String.replace "_" " " (ContentType.toString ct)) row.contentType
                , Maybe.map (\n -> Format.count n ++ " " ++ plural n "file") row.filesCount
                , Maybe.map (\n -> Format.count n ++ " " ++ plural n "seeder") row.seeders
                , Maybe.map (\n -> Format.count n ++ " " ++ plural n "leecher") row.leechers
                ]
    in
    [ div [ class "meta", classList [ ( "with-name", showsRawName item ) ] ]
        (div [ class "facts" ] [ text (String.join " · " facts) ]
            :: div [ class "hash" ] [ text row.infoHash ]
            :: (if showsRawName item then
                    [ div [ class "rawname" ] [ text row.name ] ]

                else
                    []
               )
        )
    ]


{-| Only `multi` has a file list. The other three all had an expander that simply wasn't
drawn, which reads as a missing feature rather than as an answer — especially for
`over_threshold`, which reports a file count and so looks like it is hiding something. So
that case says why instead. `no_info` has no count to explain away, and `single` is a
torrent that _is_ its one file, so both stay silent.
-}
viewFiles : Bool -> Item -> List (Html Msg)
viewFiles canQuery item =
    case item.row.filesStatus of
        Over_threshold ->
            [ div [ class "files-notice" ]
                [ text "Too many files for bitmagnet to index — no list to show." ]
            ]

        Multi ->
            if not canQuery then
                []

            else
                List.map (viewEntry item) (fileEntries item)
                    ++ (case item.files of
                            Unopened ->
                                []

                            Fetching ->
                                [ div [ class "files-notice" ] [ text "Loading files…" ] ]

                            FilesFailed message ->
                                [ div [ class "files-notice error" ] [ text message ] ]

                            Open open ->
                                if open.omission == "" then
                                    []

                                else
                                    [ div [ class "files-notice" ] [ text open.omission ] ]
                       )

        _ ->
            []


{-| One line of the tree. Folders carry a twist and are clickable; files are inert.

Clicking the root while the files have not been fetched is what fetches them; after that
the same row just opens and closes like any other folder.

-}
viewEntry : Item -> FileTree.Entry -> Html Msg
viewEntry item entry =
    let
        indent =
            -- Matches the stylesheet's own left padding for the panel, plus a step per
            -- level. Horizontal only, so it cannot disturb the row height.
            Html.Attributes.style "padding-left"
                (String.fromFloat (2.6 + toFloat entry.depth * 1.05) ++ "rem")

        opens =
            case item.files of
                Unopened ->
                    ToggleFiles item.row.id

                _ ->
                    ToggleFolder item.row.id entry.key
    in
    if entry.isFolder then
        div
            [ class "file folder"
            , classList [ ( "open", not entry.collapsed ) ]
            , indent
            , attribute "role" "button"
            , attribute "aria-expanded"
                (if entry.collapsed then
                    "false"

                 else
                    "true"
                )
            , onClick opens
            ]
            [ span [ class "branch" ] [ chevronIcon ]
            , span [ class "path" ] [ text entry.name ]
            , span [ class "size" ] [ text (Format.bytes entry.size) ]
            ]

    else
        div [ class "file", indent ]
            [ span [ class "branch" ] []
            , span [ class "path" ] [ text entry.name ]
            , span [ class "size" ] [ text (Format.bytes entry.size) ]
            ]


{-| What the list isn't showing, if anything. Returning the sentence itself keeps the
height calculation and the view reading off one function instead of two conditions that
can drift apart.
-}
omissions : Bitmagnet.FileList -> String
omissions fileList =
    let
        padding =
            if fileList.hidden == 0 then
                []

            else
                [ String.fromInt fileList.hidden ++ " padding " ++ plural fileList.hidden "file" ++ " hidden" ]

        cap =
            if fileList.capped then
                [ "first " ++ String.fromInt Bitmagnet.fileLimit ++ " files only" ]

            else
                []
    in
    case padding ++ cap of
        [] ->
            ""

        notes ->
            String.join " · " notes


chevronIcon : Html msg
chevronIcon =
    Svg.svg
        [ SvgAttr.viewBox "0 0 24 24"
        , SvgAttr.width "12"
        , SvgAttr.height "12"
        , SvgAttr.fill "currentColor"
        , attribute "aria-hidden" "true"
        ]
        [ Svg.path [ SvgAttr.d "M8 4l10 8-10 8Z" ] [] ]


{-| A horseshoe magnet: a filled arch, open at the bottom.
-}
magnetIcon : Html msg
magnetIcon =
    Svg.svg
        [ SvgAttr.viewBox "0 0 24 24"
        , SvgAttr.width "15"
        , SvgAttr.height "15"
        , SvgAttr.fill "currentColor"
        , attribute "aria-hidden" "true"
        ]
        [ Svg.path [ SvgAttr.d "M3 20V12a9 9 0 0 1 18 0v8h-5v-8a4 4 0 0 0-8 0v8Z" ] [] ]
