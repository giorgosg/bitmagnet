module Moderation exposing (Decision(..), Msg(..), State, empty, fetch, update, view, withFlagContext)

{-| Private review uses its own API, never the catalogue's download controls.
Revisions are opaque strings, passed back exactly as received.
-}

import ApiError
import Bitmagnet
import Format
import Graphql.Http
import Graphql.OptionalArgument exposing (OptionalArgument(..))
import Graphql.SelectionSet as SelectionSet exposing (with)
import Html exposing (Html, a, button, dd, div, dl, dt, form, h1, h2, h3, input, label, li, p, pre, text, ul)
import Html.Attributes exposing (attribute, class, disabled, href, id, maxlength, type_, value)
import Html.Events exposing (onClick, onInput, onSubmit)
import Identity
import Magnes.Api.Enum.ModerationStatus as Status exposing (ModerationStatus)
import Magnes.Api.InputObject as Input
import Magnes.Api.Mutation
import Magnes.Api.Object
import Magnes.Api.Object.ModerationAllowResult as AllowResult
import Magnes.Api.Object.ModerationEvidence as Evidence
import Magnes.Api.Object.ModerationHistoryEntry as HistoryEntry
import Magnes.Api.Object.ModerationHistoryPage as HistoryPage
import Magnes.Api.Object.ModerationMutation as Mutation
import Magnes.Api.Object.ModerationQuery as Query
import Magnes.Api.Object.ModerationRecord as Record
import Magnes.Api.Object.ModerationSearchPage as SearchPage
import Magnes.Api.Object.ModerationSummary as Summary
import Magnes.Api.Query
import Magnes.Api.Scalar as Scalar
import Route
import Task
import Time


type alias Summary =
    { identifier : String, status : ModerationStatus, origin : String, name : Maybe String }


type alias Page =
    { items : List Summary, next : Maybe String }


type alias Evidence =
    { name : String, files : Maybe (List String), rule : String, matches : Maybe (List String), infoHash : Maybe Scalar.Hash20 }


type alias Event =
    { action : String, status : ModerationStatus, origin : String, actor : String, createdAt : Time.Posix, importedOrigin : Maybe String, source : Maybe String }


type alias History =
    { items : List Event, next : Maybe String }


type alias Record =
    { identifier : String, status : ModerationStatus, origin : String, importedOrigin : Maybe String, source : Maybe String, revision : String, updatedAt : Time.Posix, evidence : Maybe Evidence, history : History }


type alias State =
    { query : String
    , hash : String
    , page : Maybe Page
    , record : Maybe Record
    , loading : Bool
    , working : Bool
    , blocked : Bool
    , confirmation : Maybe Decision
    , error : Maybe String
    , notice : Maybe String
    , uncertainFlag : Maybe String
    }


empty : Route.ModerationParams -> State
empty params =
    { query = params.query, hash = "", page = Nothing, record = Nothing, loading = True, working = False, blocked = False, confirmation = Nothing, error = Nothing, notice = Nothing, uncertainFlag = params.uncertainFlag }


type Decision
    = Allow
    | Ban
    | Reopen
    | Purge


type Msg
    = Navigate Route.ModerationParams
    | QueryEdited String
    | HashEdited String
    | Search Route.ModerationParams
    | Refresh Route.ModerationParams
    | GotPage (Result (Graphql.Http.Error Page) Page)
    | GotRecord Bool (Result (Graphql.Http.Error Record) Record)
    | Ask Decision
    | Cancel
    | Confirm
    | Flag
    | GotFlag String (Result (Graphql.Http.Error ( Record, Bool )) ( Record, Bool ))
    | GotDecision (Result (Graphql.Http.Error ( Record, Bool )) ( Record, Bool ))
    | MoreHistory


fetch : String -> Route.ModerationParams -> Cmd Msg
fetch apiUrl params =
    case params.identifier of
        Just identifier ->
            fetchRecord apiUrl identifier Nothing False

        Nothing ->
            Magnes.Api.Query.moderation
                (Query.search { input = Input.buildModerationSearchInput (\opts -> { opts | status = Present params.status, query = Present params.query, limit = Present 50, after = optional params.after }) }
                    (SelectionSet.map2 Page
                        (SearchPage.items (SelectionSet.map4 Summary Summary.identifier Summary.status Summary.origin Summary.name))
                        SearchPage.next
                    )
                )
                |> Bitmagnet.queryRequest apiUrl
                |> Graphql.Http.send GotPage


optional : Maybe a -> OptionalArgument a
optional =
    Maybe.map Present >> Maybe.withDefault Absent


recordSelection : Maybe String -> SelectionSet.SelectionSet Record Magnes.Api.Object.ModerationRecord
recordSelection after =
    SelectionSet.succeed Record
        |> with Record.identifier
        |> with Record.status
        |> with Record.origin
        |> with Record.importedOrigin
        |> with Record.source
        |> with Record.revision
        |> with Record.updatedAt
        |> with (Record.evidence (SelectionSet.map5 Evidence Evidence.name Evidence.files Evidence.rule Evidence.matches Evidence.infoHash))
        |> with
            (Record.history { input = Input.buildModerationPageInput (\opts -> { opts | limit = Present 50, after = optional after }) }
                (SelectionSet.map2 History
                    (HistoryPage.items (SelectionSet.succeed Event |> with HistoryEntry.action |> with HistoryEntry.status |> with HistoryEntry.origin |> with HistoryEntry.actor |> with HistoryEntry.createdAt |> with HistoryEntry.importedOrigin |> with HistoryEntry.source))
                    HistoryPage.next
                )
            )


fetchRecord : String -> String -> Maybe String -> Bool -> Cmd Msg
fetchRecord apiUrl identifier after append =
    Magnes.Api.Query.moderation (Query.record { identifier = identifier } (recordSelection after))
        |> Bitmagnet.queryRequest apiUrl
        |> Graphql.Http.send (GotRecord append)


update : String -> Identity.Identity -> Msg -> State -> ( State, Cmd Msg )
update apiUrl identity msg state =
    case msg of
        QueryEdited query ->
            ( { state | query = query }, Cmd.none )

        HashEdited hash ->
            ( { state | hash = hash }, Cmd.none )

        Search params ->
            ( state, cmdNavigate { params | query = state.query, after = Nothing, identifier = Nothing, uncertainFlag = state.uncertainFlag } )

        Navigate _ ->
            ( state, Cmd.none )

        Refresh params ->
            if state.working then
                ( state, Cmd.none )

            else
                ( { state | loading = True, record = Nothing, confirmation = Nothing, error = Nothing, notice = Nothing }, fetch apiUrl params )

        GotPage result ->
            case result of
                Ok page ->
                    ( { state | page = Just page, loading = False, error = Nothing, blocked = False }, Cmd.none )

                Err error ->
                    ( { state | loading = False, error = Just (ApiError.toMessage (ApiError.fromError error)) }, Cmd.none )

        GotRecord append result ->
            case result of
                Ok record ->
                    let
                        current =
                            case state.record of
                                Just previous ->
                                    if append && previous.revision == record.revision then
                                        { record | history = { items = previous.history.items ++ record.history.items, next = record.history.next } }

                                    else
                                        record

                                Nothing ->
                                    record
                    in
                    if append && Maybe.map .revision state.record /= Just record.revision then
                        ( { state | loading = True, record = Nothing }, fetchRecord apiUrl record.identifier Nothing False )

                    else
                        let
                            foundFlagTarget =
                                case state.uncertainFlag of
                                    Just hash ->
                                        (current.evidence |> Maybe.andThen .infoHash) == Just (Scalar.Hash20 hash)

                                    Nothing ->
                                        False
                        in
                        ( { state
                            | record = Just current
                            , loading = False
                            , blocked = False
                            , error = Nothing
                            , uncertainFlag =
                                if foundFlagTarget then
                                    Nothing

                                else
                                    state.uncertainFlag
                            , notice =
                                if foundFlagTarget then
                                    Just "Review record found for the requested infohash. The current decision is shown below."

                                else
                                    state.notice
                          }
                        , Cmd.none
                        )

                Err error ->
                    ( { state | loading = False, error = Just (ApiError.toMessage (ApiError.fromError error)) }, Cmd.none )

        Ask action ->
            if canAct identity state && state.record /= Nothing then
                ( { state | confirmation = Just action, notice = Nothing }, Cmd.none )

            else
                ( state, Cmd.none )

        Cancel ->
            ( { state | confirmation = Nothing }, Cmd.none )

        Confirm ->
            case ( state.confirmation, state.record ) of
                ( Just action, Just record ) ->
                    if canAct identity state then
                        ( { state | working = True, confirmation = Nothing, notice = Nothing, error = Nothing }, decide apiUrl action record )

                    else
                        ( state, Cmd.none )

                _ ->
                    ( state, Cmd.none )

        Flag ->
            if canAct identity state && validHash state.hash then
                ( { state | working = True, notice = Nothing, error = Nothing }
                , Magnes.Api.Mutation.moderation (Mutation.flag { infoHash = Scalar.Hash20 (String.toLower (String.trim state.hash)) } (recordSelection Nothing |> SelectionSet.map (\record -> ( record, False ))))
                    |> Bitmagnet.mutationRequest apiUrl
                    |> Graphql.Http.send (GotFlag (String.toLower (String.trim state.hash)))
                )

            else
                ( state, Cmd.none )

        GotFlag hash result ->
            case result of
                Ok accepted ->
                    update apiUrl identity (GotDecision (Ok accepted)) state

                Err error ->
                    let
                        failure =
                            ApiError.fromError error
                    in
                    if ApiError.mayHaveActed failure then
                        ( { state | working = False, blocked = True, notice = Nothing, error = Nothing, uncertainFlag = Just hash }, Cmd.none )

                    else
                        ( { state | working = False, blocked = True, notice = Nothing, error = Just ("The flag was not saved. " ++ ApiError.toMessage failure) }, Cmd.none )

        GotDecision result ->
            case result of
                Ok ( record, queued ) ->
                    ( { state
                        | record = Just record
                        , working = False
                        , blocked = False
                        , error = Nothing
                        , notice =
                            Just
                                ("Saved: "
                                    ++ Status.toString record.status
                                    ++ "."
                                    ++ (if queued then
                                            " Normal classification is queued."

                                        else
                                            ""
                                       )
                                )
                      }
                    , Cmd.none
                    )

                Err error ->
                    let
                        failure =
                            ApiError.fromError error

                        explanation =
                            if ApiError.mayHaveActed failure then
                                "The decision outcome is unknown. Refresh the record before another action. "

                            else
                                "The decision was not saved. Refresh the record before another action. "
                    in
                    ( { state | working = False, blocked = True, notice = Nothing, error = Just (explanation ++ ApiError.toMessage failure) }, Cmd.none )

        MoreHistory ->
            case state.record of
                Just record ->
                    if state.loading || state.working then
                        ( state, Cmd.none )

                    else
                        case record.history.next of
                            Just after ->
                                ( { state | loading = True }, fetchRecord apiUrl record.identifier (Just after) True )

                            Nothing ->
                                ( state, Cmd.none )

                Nothing ->
                    ( state, Cmd.none )


cmdNavigate : Route.ModerationParams -> Cmd Msg
cmdNavigate params =
    -- Navigation is owned by Main, including its query epoch.
    Task.perform (\_ -> Navigate params) (Task.succeed ())


canAct : Identity.Identity -> State -> Bool
canAct identity state =
    Identity.can (Identity.graphql "moderation" "mutate") identity && not state.working && not state.loading && not state.blocked && state.uncertainFlag == Nothing


validHash : String -> Bool
validHash raw =
    String.length (String.trim raw) == 40 && String.all Char.isHexDigit (String.trim raw)


decide : String -> Decision -> Record -> Cmd Msg
decide apiUrl action record =
    let
        input =
            { identifier = record.identifier, expectedRevision = record.revision, infoHash = Absent }

        selection =
            case action of
                Allow ->
                    Mutation.allow { input = input } (SelectionSet.map2 Tuple.pair (AllowResult.record (recordSelection Nothing)) AllowResult.classificationQueued)

                Ban ->
                    Mutation.ban { input = input } (recordSelection Nothing |> SelectionSet.map (\r -> ( r, False )))

                Reopen ->
                    Mutation.reopen { input = input } (recordSelection Nothing |> SelectionSet.map (\r -> ( r, False )))

                Purge ->
                    Mutation.purgeEvidence { input = input } (recordSelection Nothing |> SelectionSet.map (\r -> ( r, False )))
    in
    Magnes.Api.Mutation.moderation selection
        |> Bitmagnet.mutationRequest apiUrl
        |> Graphql.Http.send GotDecision


view : Route.BasePath -> Time.Zone -> Identity.Identity -> Route.ModerationParams -> State -> Html Msg
view mount zone identity routeParams state =
    let
        params =
            { routeParams | uncertainFlag = state.uncertainFlag }
    in
    div [ class "page moderation-page" ]
        [ h1 [] [ text "Moderation" ]
        , p [] [ text "Private metadata review. Filenames do not verify file contents." ]
        , div [ class "moderation-tabs" ]
            (List.map
                (\status ->
                    a
                        [ href (Route.toHref mount (Route.Moderation { params | status = status, after = Nothing, identifier = Nothing }))
                        , attribute "aria-current"
                            (if status == params.status then
                                "page"

                             else
                                "false"
                            )
                        ]
                        [ text (statusLabel status) ]
                )
                Status.list
            )
        , form [ onSubmit (Search params) ]
            [ label [ Html.Attributes.for "moderation-query" ] [ text "Find a moderation record" ]
            , input [ id "moderation-query", value state.query, onInput QueryEdited, maxlength 256 ] []
            , button [ type_ "submit", disabled state.working ] [ text "Search decisions" ]
            ]
        , button [ type_ "button", onClick (Refresh params), disabled (state.working || state.loading) ] [ text "Refresh review" ]
        , case state.uncertainFlag of
            Just hash ->
                div [ class "notice error", attribute "role" "alert" ]
                    [ p [] [ text ("The flag outcome is unknown. Requested infohash: " ++ hash ++ ". Refreshing another record cannot resolve this request. Search the decision lists and open the requested torrent's retained evidence to inspect its current decision.") ]
                    , p [] [ text "Dismissing this warning does not confirm whether the flag was saved." ]
                    , a [ href (Route.toHref mount (Route.Moderation { routeParams | uncertainFlag = Nothing })) ] [ text "Dismiss unknown flag warning" ]
                    ]

            Nothing ->
                text ""
        , Maybe.map (\error -> p [ class "notice error", attribute "role" "alert" ] [ text error ]) state.error |> Maybe.withDefault (text "")
        , Maybe.map (\notice -> p [ class "notice", attribute "role" "status" ] [ text notice ]) state.notice |> Maybe.withDefault (text "")
        , if state.loading then
            p [ attribute "role" "status" ] [ text "Loading moderation…" ]

          else
            text ""
        , case state.record of
            Just record ->
                recordView zone identity state record

            Nothing ->
                case state.page of
                    Just page ->
                        div []
                            [ if List.isEmpty page.items then
                                p [] [ text "No moderation records match this search." ]

                              else
                                ul [] (List.map (\item -> li [] [ a [ href (Route.toHref mount (Route.Moderation { params | identifier = Just item.identifier })) ] [ text (Maybe.withDefault "Metadata unavailable" item.name) ], pre [] [ text item.identifier ], p [] [ text (Status.toString item.status ++ " · " ++ item.origin) ] ]) page.items)
                            , a [ href (Route.toHref mount (Route.Moderation { params | after = Nothing })) ] [ text "First page" ]
                            , Maybe.map (\after -> a [ href (Route.toHref mount (Route.Moderation { params | after = Just after })) ] [ text "Next page" ]) page.next |> Maybe.withDefault (text "")
                            ]

                    Nothing ->
                        text ""
        , if Identity.can (Identity.graphql "moderation" "mutate") identity then
            form [ onSubmit Flag ]
                [ h2 [] [ text "Flag a catalogue torrent" ]
                , label [ Html.Attributes.for "moderation-hash" ] [ text "Torrent infohash" ]
                , input [ id "moderation-hash", value state.hash, onInput HashEdited, maxlength 40 ] []
                , button [ type_ "submit", disabled (not (canAct identity state) || not (validHash state.hash)) ] [ text "Flag for review" ]
                ]

          else
            text ""
        ]


statusLabel : ModerationStatus -> String
statusLabel status =
    case status of
        Status.Pending ->
            "Pending"

        Status.Allowed ->
            "Allowed"

        Status.Banned ->
            "Banned"


recordView : Time.Zone -> Identity.Identity -> State -> Record -> Html Msg
recordView zone identity state record =
    div []
        [ h2 [] [ text "Review details" ]
        , pre [] [ text record.identifier ]
        , dl []
            [ dt [] [ text "Decision" ]
            , dd [] [ text (Status.toString record.status) ]
            , dt [] [ text "Origin" ]
            , dd [] [ text record.origin ]
            , dt [] [ text "Imported origin" ]
            , dd [] [ text (Maybe.withDefault "None" record.importedOrigin) ]
            , dt [] [ text "Source" ]
            , dd [] [ text (Maybe.withDefault "None" record.source) ]
            , dt [] [ text "Updated" ]
            , dd [] [ text (Format.dateTime zone record.updatedAt) ]
            ]
        , case record.evidence of
            Nothing ->
                p [] [ text "Metadata unavailable. Evidence may not have been observed or may have been purged." ]

            Just evidence ->
                div []
                    [ h3 [] [ text "Retained name" ]
                    , p []
                        [ text
                            (if String.isEmpty evidence.name then
                                "Name unavailable"

                             else
                                evidence.name
                            )
                        ]
                    , h3 [] [ text "Complete file paths" ]
                    , case evidence.files of
                        Nothing ->
                            p [] [ text "File metadata unavailable." ]

                        Just files ->
                            ul [] (List.map (\path -> li [] [ pre [] [ text path ] ]) files)
                    , h3 [] [ text "Matching rule" ]
                    , pre []
                        [ text
                            (if String.isEmpty evidence.rule then
                                "No matching rule retained."

                             else
                                evidence.rule
                            )
                        ]
                    , ul [] (List.map (\match -> li [] [ text match ]) (Maybe.withDefault [] evidence.matches))
                    ]
        , if Identity.can (Identity.graphql "moderation" "mutate") identity then
            div []
                [ div []
                    (List.map (\( action, caption ) -> button [ type_ "button", onClick (Ask action), disabled (not (canAct identity state)) ] [ text caption ])
                        ((case record.status of
                            Status.Pending ->
                                [ ( Allow, "Allow" ), ( Ban, "Ban" ) ]

                            Status.Allowed ->
                                [ ( Reopen, "Reopen" ), ( Ban, "Ban" ) ]

                            Status.Banned ->
                                [ ( Allow, "Allow" ), ( Reopen, "Reopen" ) ]
                         )
                            ++ [ ( Purge, "Purge evidence" ) ]
                        )
                    )
                , case state.confirmation of
                    Just action ->
                        div [ class "admin-confirmation", attribute "role" "alert" ]
                            [ p []
                                [ text
                                    (if action == Purge then
                                        "Permanently clear retained metadata and prior private history? The decision remains enforced."

                                     else
                                        "Confirm " ++ decisionName action ++ " for this record?"
                                    )
                                ]
                            , button [ type_ "button", onClick Confirm ] [ text "Confirm decision" ]
                            , button [ type_ "button", onClick Cancel ] [ text "Cancel" ]
                            ]

                    Nothing ->
                        text ""
                , if state.working then
                    p [ attribute "role" "status" ] [ text "Saving decision…" ]

                  else
                    text ""
                ]

          else
            text ""
        , h3 [] [ text "Decision history" ]
        , ul [] (List.map (\event -> li [] [ text (event.action ++ " · " ++ Status.toString event.status ++ " · " ++ event.origin ++ provenance event ++ " · " ++ event.actor ++ " · " ++ Format.dateTime zone event.createdAt) ]) record.history.items)
        , case record.history.next of
            Just _ ->
                button [ type_ "button", onClick MoreHistory, disabled (state.loading || state.working) ] [ text "More history" ]

            Nothing ->
                text ""
        ]


decisionName : Decision -> String
decisionName decision =
    case decision of
        Allow ->
            "allow"

        Ban ->
            "ban"

        Reopen ->
            "reopen"

        Purge ->
            "purge"


provenance : Event -> String
provenance event =
    List.filterMap identity [ event.importedOrigin, event.source ]
        |> List.map ((++) " · ")
        |> String.concat


withFlagContext : Route.ModerationParams -> State -> State
withFlagContext params state =
    if state.uncertainFlag == params.uncertainFlag then
        state

    else
        { state | uncertainFlag = params.uncertainFlag, blocked = False }
