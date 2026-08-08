module Main exposing (main)

import Bitmagnet exposing (File, Page, Row)
import Browser
import Browser.Dom
import Browser.Events
import Browser.Navigation as Nav
import Format
import Graphql.Http
import Html exposing (Html, a, button, div, form, h1, header, input, main_, p, span, text)
import Html.Attributes exposing (attribute, class, classList, href, id, placeholder, spellcheck, type_, value)
import Html.Events exposing (on, onClick, onInput, onSubmit)
import InfiniteList
import Json.Decode as Decode exposing (Value)
import Magnes.Api.Enum.ContentType as ContentType
import Magnes.Api.Enum.FilesStatus exposing (FilesStatus(..))
import Process
import Route exposing (Route)
import Set exposing (Set)
import Svg
import Svg.Attributes as SvgAttr
import Task
import Time
import Url exposing (Url)


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


{-| bitmagnet's address arrives at runtime from `index.html`, so the same build works
against any instance — and against the proxy, when there is one.
-}
type alias Flags =
    { apiUrl : String }


type alias Model =
    { key : Nav.Key
    , apiUrl : String
    , route : Route
    , field : String
    , results : Results
    , infiniteList : InfiniteList.Model
    , viewportHeight : Int
    , zone : Time.Zone

    -- Bumped when the searched-for query actually changes. Responses carry the epoch they
    -- were asked under, so a page that arrives after the query moved on is dropped rather
    -- than appended to the wrong list.
    , epoch : Int

    -- Separate counter for the debounce, because a keystroke is not yet a new query.
    -- Bumping the epoch here would strand an in-flight page — the reply would be dropped
    -- while `fetching` stayed true, and infinite scroll would quietly stop.
    , typing : Int
    }


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
    | Open Bitmagnet.FileList


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
        route =
            Route.fromUrl url

        ( results, cmd ) =
            load flags.apiUrl 0 route
    in
    ( { key = key
      , apiUrl = flags.apiUrl
      , route = route
      , field = fieldFor route
      , results = results
      , infiniteList = InfiniteList.init
      , viewportHeight = 800
      , zone = Time.utc
      , epoch = 0
      , typing = 0
      }
    , Cmd.batch
        [ cmd
        , Task.perform (\vp -> Resized (round vp.viewport.height)) Browser.Dom.getViewport
        , Task.perform GotZone Time.here
        ]
    )


fieldFor : Route -> String
fieldFor route =
    case route of
        Route.Search params ->
            Maybe.withDefault "" params.q

        _ ->
            ""


subscriptions : Model -> Sub Msg
subscriptions _ =
    Browser.Events.onResize (\_ height -> Resized height)


type Msg
    = LinkClicked Browser.UrlRequest
    | UrlChanged Url
    | FieldChanged String
    | DebounceElapsed Int
    | Submitted
    | Scrolled Value
    | Resized Int
    | GotZone Time.Zone
    | ToggleExpanded String
    | ToggleFiles String
    | GotFiles String (Result (Graphql.Http.Error Bitmagnet.FileList) Bitmagnet.FileList)
    | GotResults Int (Result (Graphql.Http.Error Page) Page)
    | Ignored


update : Msg -> Model -> ( Model, Cmd Msg )
update msg model =
    case msg of
        Ignored ->
            ( model, Cmd.none )

        LinkClicked (Browser.Internal url) ->
            ( model, Nav.pushUrl model.key (Url.toString url) )

        LinkClicked (Browser.External url) ->
            ( model, Nav.load url )

        UrlChanged url ->
            let
                route =
                    Route.fromUrl url
            in
            if route == model.route then
                -- Same query re-submitted; don't throw away results to fetch them again.
                ( model, Cmd.none )

            else
                let
                    epoch =
                        model.epoch + 1

                    ( results, cmd ) =
                        load model.apiUrl epoch route
                in
                ( { model
                    | route = route
                    , field = syncField model route
                    , results = results
                    , epoch = epoch
                    , infiniteList = InfiniteList.init
                  }
                , Cmd.batch [ cmd, scrollListToTop ]
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
                , Nav.replaceUrl model.key
                    (Route.toHref (Route.Search { q = trimToMaybe model.field }))
                )

        Submitted ->
            ( model
            , Nav.pushUrl model.key
                (Route.toHref (Route.Search { q = trimToMaybe model.field }))
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
            case itemById rowId model of
                Just item ->
                    case item.files of
                        Unopened ->
                            ( mapItem rowId (\i -> { i | files = Fetching }) model
                            , Bitmagnet.files item.row.infoHash
                                |> Graphql.Http.queryRequest model.apiUrl
                                |> Graphql.Http.send (GotFiles rowId)
                            )

                        _ ->
                            ( mapItem rowId (\i -> { i | files = Unopened }) model, Cmd.none )

                Nothing ->
                    ( model, Cmd.none )

        GotFiles rowId (Ok fileList) ->
            ( mapItem rowId (\item -> { item | files = Open fileList }) model, Cmd.none )

        GotFiles rowId (Err error) ->
            ( mapItem rowId (\item -> { item | files = FilesFailed (Bitmagnet.errorToString error) }) model
            , Cmd.none
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
                        ( { model | results = Failed (Bitmagnet.errorToString error) }, Cmd.none )


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


{-| What a route needs fetched, and the state to show until it arrives.
-}
load : String -> Int -> Route -> ( Results, Cmd Msg )
load apiUrl epoch route =
    case route of
        Route.Search params ->
            ( Loading, fetch apiUrl epoch (searchArgs params.q 0) )

        Route.Torrent infoHash ->
            ( Loading, fetch apiUrl epoch (Bitmagnet.byInfoHash infoHash) )

        Route.NotFound ->
            ( Blank, Cmd.none )


searchArgs : Maybe String -> Int -> Bitmagnet.SearchArgs
searchArgs queryString offset =
    { queryString = queryString, infoHashes = [], limit = pageSize, offset = offset }


fetch : String -> Int -> Bitmagnet.SearchArgs -> Cmd Msg
fetch apiUrl epoch args =
    Bitmagnet.search args
        |> Graphql.Http.queryRequest apiUrl
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
                , fetch model.apiUrl model.epoch (searchArgs params.q (List.length feed.items))
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
                    { items = [], ids = Set.empty, total = 0, totalIsEstimate = False, hasNextPage = False, fetching = False }

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
            [ a [ class "wordmark", href (Route.toHref (Route.Search Route.emptySearch)) ]
                [ h1 [] [ text "magnes" ] ]
            , searchBox model.field
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

        Route.NotFound ->
            "not found — magnes"


searchBox : String -> Html Msg
searchBox field =
    form [ class "search", onSubmit Submitted ]
        [ input
            [ type_ "search"
            , placeholder "search the index"
            , value field
            , spellcheck False
            , attribute "autocomplete" "off"
            , onInput FieldChanged
            ]
            []
        ]


viewRoute : Model -> Html Msg
viewRoute model =
    case model.route of
        Route.Search _ ->
            viewResults model

        Route.Torrent _ ->
            viewTorrent model

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
                    div [ class "single" ] [ viewItem item ]

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
        { itemView = \_ _ item -> viewItem item
        , itemHeight = InfiniteList.withVariableHeight (\_ item -> itemHeight item)
        , containerHeight = listHeight model
        }


{-| Told to the virtualizer, so it has to be what the browser actually lays out.
-}
itemHeight : Item -> Int
itemHeight item =
    if item.expanded then
        rowHeight + metaHeight item + filesAffordanceHeight item + filesHeight item

    else
        rowHeight


{-| The one line under the metadata: either the toggle, or the reason there isn't one.
-}
filesAffordanceHeight : Item -> Int
filesAffordanceHeight item =
    case item.row.filesStatus of
        Multi ->
            noticeHeight

        Over_threshold ->
            noticeHeight

        _ ->
            0


filesHeight : Item -> Int
filesHeight item =
    case item.files of
        Unopened ->
            0

        Fetching ->
            noticeHeight

        FilesFailed _ ->
            noticeHeight

        Open fileList ->
            (fileHeight * List.length fileList.files)
                + (if omissions fileList == "" then
                    0

                   else
                    noticeHeight
                  )


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


viewItem : Item -> Html Msg
viewItem item =
    div [ class "item" ]
        (viewRow item
            :: (if item.expanded then
                    viewMeta item ++ viewFiles item

                else
                    []
               )
        )


{-| One line: chevron, name, size, magnet. Everything else is behind the chevron — on a
real index most rows have no metadata, so a column of it is a column of blanks.
-}
viewRow : Item -> Html Msg
viewRow item =
    div [ class "row" ]
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
            , onClick (ToggleExpanded item.row.id)
            ]
            [ chevronIcon ]
        , a [ class "name", href (Route.toHref (Route.Torrent item.row.infoHash)) ]
            [ text item.row.title ]
        , span [ class "size" ] [ text (Format.bytes item.row.size) ]
        , a
            [ class "magnet"
            , href item.row.magnetUri
            , attribute "aria-label" ("Magnet link for " ++ item.row.title)
            , attribute "title" "magnet link"
            ]
            [ magnetIcon ]
        ]


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
viewFiles : Item -> List (Html Msg)
viewFiles item =
    case item.row.filesStatus of
        Over_threshold ->
            [ div [ class "files-notice" ]
                [ text "Too many files for bitmagnet to index — no list to show." ]
            ]

        Multi ->
            button
                [ class "files-toggle", type_ "button", onClick (ToggleFiles item.row.id) ]
                [ text
                    (case item.files of
                        Unopened ->
                            "show files"

                        _ ->
                            "hide files"
                    )
                ]
                :: (case item.files of
                        Unopened ->
                            []

                        Fetching ->
                            [ div [ class "files-notice" ] [ text "Loading files…" ] ]

                        FilesFailed message ->
                            [ div [ class "files-notice error" ] [ text message ] ]

                        Open fileList ->
                            List.map viewFile fileList.files
                                ++ (case omissions fileList of
                                        "" ->
                                            []

                                        note ->
                                            [ div [ class "files-notice" ] [ text note ] ]
                                   )
                   )

        _ ->
            []


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


viewFile : File -> Html Msg
viewFile file =
    div [ class "file" ]
        [ span [ class "path" ] [ text file.path ]
        , span [ class "size" ] [ text (Format.bytes file.size) ]
        ]


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
