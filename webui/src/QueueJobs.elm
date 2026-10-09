module QueueJobs exposing (Job, Listing, Messages, Page, State, empty, failed, fetch, loaded, prettyPayload, query, refreshing, toggle, view)

{-| bitmagnet's queue, a page of jobs at a time: filtered by queue and status, ordered, and
opened one by one to read what each was asked to do and how it failed.

Everything that decides which jobs are listed is in the URL (`Route.JobsParams`), so a
filtered page is a link. Which rows are open is not: it belongs to one look at one page.

-}

import ApiError
import Bitmagnet
import Chip
import Format
import Graphql.Http
import Graphql.Operation exposing (RootQuery)
import Graphql.OptionalArgument exposing (OptionalArgument(..))
import Graphql.SelectionSet as SelectionSet exposing (SelectionSet, with)
import Html exposing (Html, a, button, dd, div, dl, dt, h1, h2, option, p, pre, select, span, table, tbody, td, text, th, thead, tr)
import Html.Attributes exposing (attribute, class, classList, colspan, href, id, scope, selected, type_, value)
import Html.Events exposing (onClick, onInput)
import JobOrder exposing (JobOrder)
import Json.Decode as Decode
import Magnes.Api.Enum.QueueJobStatus as QueueJobStatus exposing (QueueJobStatus)
import Magnes.Api.Enum.QueueJobsOrderByField exposing (QueueJobsOrderByField(..))
import Magnes.Api.InputObject as InputObject
import Magnes.Api.Object
import Magnes.Api.Object.QueueJob as QueueJob
import Magnes.Api.Object.QueueJobQueueAgg as QueueAgg
import Magnes.Api.Object.QueueJobStatusAgg as StatusAgg
import Magnes.Api.Object.QueueJobsAggregations as Aggregations
import Magnes.Api.Object.QueueJobsQueryResult as JobsResult
import Magnes.Api.Object.QueueQuery as QueueQuery
import Magnes.Api.Query as Query
import Magnes.Api.Scalar as Scalar
import Route
import Set exposing (Set)
import Time


type alias Job =
    { id : String
    , queue : String
    , status : QueueJobStatus
    , payload : String
    , priority : Int
    , retries : Int
    , maxRetries : Int
    , runAfter : Time.Posix
    , ranAt : Maybe Time.Posix
    , error : Maybe String
    , createdAt : Time.Posix
    }


{-| One page of jobs, with the counts behind each facet's chips. A facet's counts ignore
that facet's own filter (bitmagnet's facets use or-logic), so they say what choosing
another value would add, not just how the current list splits.
-}
type alias Page =
    { jobs : List Job
    , totalCount : Int
    , hasNextPage : Bool
    , queues : List ( String, Int )
    , statuses : List ( QueueJobStatus, Int )
    }


type Listing
    = Loading
    | Failed ApiError.Failure
    | Loaded Page


{-| `refreshing` is set while another page of the list is being fetched over one already
shown. The old page stays on screen until then, so choosing a filter does not empty the
page and refill it.
-}
type alias State =
    { listing : Listing
    , refreshing : Bool
    , expanded : Set String
    }


empty : State
empty =
    { listing = Loading, refreshing = False, expanded = Set.empty }


loaded : Page -> State -> State
loaded jobsPage state =
    { state | listing = Loaded jobsPage, refreshing = False }


failed : ApiError.Failure -> State -> State
failed failure state =
    { state | listing = Failed failure, refreshing = False }


{-| A different page of the list has been asked for. The rows opened on the old one close:
they were a look at those jobs, and the new page may not have them.
-}
refreshing : State -> State
refreshing state =
    { state | refreshing = True, expanded = Set.empty }


toggle : String -> State -> State
toggle jobId state =
    { state
        | expanded =
            if Set.member jobId state.expanded then
                Set.remove jobId state.expanded

            else
                Set.insert jobId state.expanded
    }


{-| The Angular UI's page size.
-}
pageSize : Int
pageSize =
    20



-- REQUESTS


fetch : String -> Route.JobsParams -> (Result (Graphql.Http.Error Page) Page -> msg) -> Cmd msg
fetch apiUrl params toMsg =
    query params
        |> Bitmagnet.queryRequest apiUrl
        |> Graphql.Http.send toMsg


{-| Filters travel as facet filters rather than as `queues` and `statuses`. Both narrow the
list, but only a facet's own filter is left out of its counts; criteria would narrow the
counts too, and a chosen status would leave every other status counted at nothing.

The ordering is followed by creation in the same direction, as the Angular UI's is, so jobs
that tie on priority or that have not run keep one order from page to page.

-}
query : Route.JobsParams -> SelectionSet Page RootQuery
query params =
    let
        input =
            InputObject.buildQueueJobsQueryInput
                (\optionals ->
                    { optionals
                        | limit = Present pageSize
                        , page = Present params.page
                        , totalCount = Present True
                        , hasNextPage = Present True
                        , facets =
                            Present
                                { status = Present { aggregate = Present True, filter = presentList params.statuses }
                                , queue = Present { aggregate = Present True, filter = presentList params.queues }
                                }
                        , orderBy = Present (orderBy params.order)
                    }
                )
    in
    Query.queue (QueueQuery.jobs { input = input } pageSelection)


presentList : List a -> OptionalArgument (List a)
presentList values =
    if List.isEmpty values then
        Absent

    else
        Present values


orderBy : JobOrder -> List InputObject.QueueJobsOrderByInput
orderBy order =
    { field = order.field, descending = Present order.descending }
        :: (if order.field == Created_at then
                []

            else
                [ { field = Created_at, descending = Present order.descending } ]
           )


pageSelection : SelectionSet Page Magnes.Api.Object.QueueJobsQueryResult
pageSelection =
    SelectionSet.succeed
        (\jobs totalCount hasNextPage ( queues, statuses ) ->
            { jobs = jobs
            , totalCount = totalCount
            , hasNextPage = Maybe.withDefault False hasNextPage
            , queues = queues
            , statuses = statuses
            }
        )
        |> with (JobsResult.items jobSelection)
        |> with JobsResult.totalCount
        |> with JobsResult.hasNextPage
        |> with (JobsResult.aggregations aggregationsSelection)


jobSelection : SelectionSet Job Magnes.Api.Object.QueueJob
jobSelection =
    SelectionSet.succeed Job
        |> with (QueueJob.id |> SelectionSet.map (\(Scalar.Id raw) -> raw))
        |> with QueueJob.queue
        |> with QueueJob.status
        |> with QueueJob.payload
        |> with QueueJob.priority
        |> with QueueJob.retries
        |> with QueueJob.maxRetries
        |> with QueueJob.runAfter
        |> with QueueJob.ranAt
        |> with QueueJob.error
        |> with QueueJob.createdAt


{-| bitmagnet builds its aggregations from a Go map, so they arrive in no particular order.
Queues are put in name order and statuses in the schema's, so a chip stays where it was
between one page and the next.
-}
aggregationsSelection : SelectionSet ( List ( String, Int ), List ( QueueJobStatus, Int ) ) Magnes.Api.Object.QueueJobsAggregations
aggregationsSelection =
    SelectionSet.map2
        (\queues statuses ->
            ( queues |> Maybe.withDefault [] |> List.sortBy Tuple.first
            , statuses |> Maybe.withDefault [] |> List.sortBy (Tuple.first >> statusRank)
            )
        )
        (Aggregations.queue (SelectionSet.map2 Tuple.pair QueueAgg.value QueueAgg.count))
        (Aggregations.status (SelectionSet.map2 Tuple.pair StatusAgg.value StatusAgg.count))


statusRank : QueueJobStatus -> Int
statusRank status =
    case status of
        QueueJobStatus.Pending ->
            0

        QueueJobStatus.Retry ->
            1

        QueueJobStatus.Failed ->
            2

        QueueJobStatus.Processed ->
            3



-- PAYLOAD


{-| The payload re-indented when it parses as JSON, and as it was sent when it does not.

It is re-indented as text rather than decoded and encoded again. Decoding would read every
number as a double, so an identifier past 2^53 would be shown as a different number, and the
point of opening a job is to read exactly what it was given. The text is decoded only to
check that it is JSON: whitespace outside strings is dropped and line breaks are put back.

-}
prettyPayload : String -> String
prettyPayload raw =
    case Decode.decodeString Decode.value raw of
        Ok _ ->
            reindent 0 (String.toList raw) []
                |> List.reverse
                |> String.concat

        Err _ ->
            raw


{-| `written` is the output so far, most recent piece first.
-}
reindent : Int -> List Char -> List String -> List String
reindent depth chars written =
    case chars of
        [] ->
            written

        '"' :: rest ->
            let
                ( literal, after ) =
                    stringLiteral rest [ '"' ]
            in
            reindent depth after (literal :: written)

        c :: rest ->
            if c == '{' || c == '[' then
                case dropSpaces rest of
                    close :: after ->
                        if close == closing c then
                            reindent depth after (String.fromList [ c, close ] :: written)

                        else
                            reindent (depth + 1) rest (newline (depth + 1) :: String.fromChar c :: written)

                    [] ->
                        reindent (depth + 1) rest (newline (depth + 1) :: String.fromChar c :: written)

            else if c == '}' || c == ']' then
                reindent (depth - 1) rest (String.fromChar c :: newline (depth - 1) :: written)

            else if c == ',' then
                reindent depth rest (newline depth :: "," :: written)

            else if c == ':' then
                reindent depth rest (": " :: written)

            else if isSpace c then
                reindent depth rest written

            else
                reindent depth rest (String.fromChar c :: written)


{-| A string literal up to and including its closing quote, escapes kept as they are.
`taken` is what has been read of it so far, last character first.
-}
stringLiteral : List Char -> List Char -> ( String, List Char )
stringLiteral chars taken =
    case chars of
        [] ->
            ( String.fromList (List.reverse taken), [] )

        '\\' :: escaped :: rest ->
            stringLiteral rest (escaped :: '\\' :: taken)

        '"' :: rest ->
            ( String.fromList (List.reverse ('"' :: taken)), rest )

        c :: rest ->
            stringLiteral rest (c :: taken)


closing : Char -> Char
closing open =
    if open == '{' then
        '}'

    else
        ']'


dropSpaces : List Char -> List Char
dropSpaces chars =
    case chars of
        c :: rest ->
            if isSpace c then
                dropSpaces rest

            else
                chars

        [] ->
            []


isSpace : Char -> Bool
isSpace c =
    c == ' ' || c == '\n' || c == '\t' || c == '\u{000D}'


newline : Int -> String
newline depth =
    "\n" ++ String.repeat depth "  "



-- VIEW


type alias Messages msg =
    { navigate : Route.JobsParams -> msg
    , toggled : String -> msg
    }


view : Route.BasePath -> Time.Zone -> Messages msg -> Route.JobsParams -> State -> Html msg
view mount zone messages params state =
    div [ class "page queue-jobs" ]
        (h1 [] [ text "Queue jobs" ]
            :: (case state.listing of
                    Loading ->
                        [ p [ class "notice" ] [ text "Loading jobs…" ] ]

                    Failed failure ->
                        [ p [ class "notice error", attribute "role" "alert" ] [ text (ApiError.toMessage failure) ] ]

                    Loaded jobsPage ->
                        [ controls messages params jobsPage
                        , listing mount zone messages params state jobsPage
                        ]
               )
        )


controls : Messages msg -> Route.JobsParams -> Page -> Html msg
controls messages params jobsPage =
    let
        -- Choosing anything starts the list over: page 3 of the old list says nothing
        -- about the new one.
        narrowed changed =
            messages.navigate { changed | page = 1 }
    in
    div [ class "facets" ]
        [ Chip.facet "queue"
            (List.map
                (\( queue, n ) ->
                    Chip.view
                        { label = queue
                        , count = Just n
                        , selected = List.member queue params.queues
                        , onToggle = narrowed { params | queues = toggled queue params.queues }
                        }
                )
                (withChosen params.queues jobsPage.queues |> List.sortBy Tuple.first)
            )
        , Chip.facet "status"
            (List.map
                (\( status, n ) ->
                    Chip.view
                        { label = QueueJobStatus.toString status
                        , count = Just n
                        , selected = List.member status params.statuses
                        , onToggle = narrowed { params | statuses = toggled status params.statuses }
                        }
                )
                (withChosen params.statuses jobsPage.statuses |> List.sortBy (Tuple.first >> statusRank))
            )
        , div [ class "facet" ]
            [ span [ class "facet-label" ] [ text "order" ]
            , select
                [ class "job-order"
                , attribute "aria-label" "Order jobs"
                , onInput (\key -> narrowed { params | order = JobOrder.fromKey key })
                ]
                (List.map
                    (\order ->
                        option [ value (JobOrder.key order), selected (order == params.order) ]
                            [ text (JobOrder.label order) ]
                    )
                    JobOrder.all
                )
            ]
        , if List.isEmpty params.queues && List.isEmpty params.statuses then
            text ""

          else
            button
                [ class "clear", type_ "button", onClick (narrowed { params | queues = [], statuses = [] }) ]
                [ text "clear filters" ]
        ]


{-| A chosen value stays offered when the aggregation leaves it out, which bitmagnet does
for a value with no jobs. Otherwise a filter that emptied the list would take its own chip
with it, and leave no way to unchoose it.
-}
withChosen : List a -> List ( a, Int ) -> List ( a, Int )
withChosen chosen counted =
    counted
        ++ List.filterMap
            (\value ->
                if List.any (Tuple.first >> (==) value) counted then
                    Nothing

                else
                    Just ( value, 0 )
            )
            chosen


toggled : a -> List a -> List a
toggled value values =
    if List.member value values then
        List.filter ((/=) value) values

    else
        values ++ [ value ]


listing : Route.BasePath -> Time.Zone -> Messages msg -> Route.JobsParams -> State -> Page -> Html msg
listing mount zone messages params state jobsPage =
    let
        lastPage =
            max 1 (ceiling (toFloat jobsPage.totalCount / toFloat pageSize))
    in
    div [ classList [ ( "jobs", True ), ( "jobs-refreshing", state.refreshing ) ], attribute "aria-busy" (boolString state.refreshing) ]
        (case jobsPage.jobs of
            [] ->
                [ p [ class "notice" ]
                    [ text
                        (if jobsPage.totalCount > 0 then
                            "There " ++ Format.forCount lastPage { one = "is only 1 page", many = "are only " ++ String.fromInt lastPage ++ " pages" } ++ " of jobs."

                         else if List.isEmpty params.queues && List.isEmpty params.statuses then
                            "The queue holds no jobs."

                         else
                            "No jobs match these filters."
                        )
                    ]
                , paging mount params jobsPage lastPage
                ]

            jobs ->
                [ div [ class "jobs-scroll" ]
                    [ table [ class "jobs-table" ]
                        [ thead []
                            [ tr []
                                [ th [ scope "col" ] [ text "Status" ]
                                , th [ scope "col" ] [ text "Queue" ]
                                , th [ scope "col" ] [ text "Created" ]
                                , th [ scope "col" ] [ text "Ran" ]
                                , th [ scope "col" ] [ text "Retries" ]
                                , th [ scope "col" ] [ text "Priority" ]
                                , th [ scope "col" ] [ span [ class "visually-hidden" ] [ text "Details" ] ]
                                ]
                            ]
                        , tbody [] (List.concatMap (jobRows zone messages state.expanded) jobs)
                        ]
                    ]
                , paging mount params jobsPage lastPage
                ]
        )


jobRows : Time.Zone -> Messages msg -> Set String -> Job -> List (Html msg)
jobRows zone messages expanded job =
    let
        open =
            Set.member job.id expanded

        detailsId =
            "job-" ++ job.id
    in
    tr [ classList [ ( "job", True ), ( "job-failing", job.status == QueueJobStatus.Failed ) ] ]
        [ td []
            (text (QueueJobStatus.toString job.status)
                :: (case job.error of
                        Just error ->
                            [ span [ class "job-error-line" ] [ text error ] ]

                        Nothing ->
                            []
                   )
            )
        , td [] [ text job.queue ]
        , td [ class "job-time" ] [ text (Format.dateTime zone job.createdAt) ]
        , td [ class "job-time" ] [ text (job.ranAt |> Maybe.map (Format.dateTime zone) |> Maybe.withDefault "Not yet") ]
        , td [ class "job-number" ] [ text (String.fromInt job.retries) ]
        , td [ class "job-number" ] [ text (String.fromInt job.priority) ]
        , td []
            [ button
                [ type_ "button"
                , attribute "aria-expanded" (boolString open)
                , attribute "aria-controls" detailsId
                , onClick (messages.toggled job.id)
                ]
                [ text "Details" ]
            ]
        ]
        :: (if open then
                [ details zone detailsId job ]

            else
                []
           )


{-| The whole job, in a row under its own. Nothing here is cut short: the payload and the
error are why a job is opened.
-}
details : Time.Zone -> String -> Job -> Html msg
details zone detailsId job =
    tr [ class "job-details", id detailsId ]
        [ td [ colspan 7 ]
            [ dl [ class "job-facts" ]
                [ div [] [ dt [] [ text "Job" ], dd [ class "job-id" ] [ text job.id ] ]
                , div [] [ dt [] [ text "Runs after" ], dd [] [ text (Format.dateTime zone job.runAfter) ] ]
                , div [] [ dt [] [ text "Retries allowed" ], dd [] [ text (String.fromInt job.maxRetries) ] ]
                ]
            , h2 [] [ text "Payload" ]
            , pre [ class "job-payload" ] [ text (prettyPayload job.payload) ]
            , case job.error of
                Just error ->
                    div []
                        [ h2 [] [ text "Error" ]
                        , pre [ class "job-error" ] [ text error ]
                        ]

                Nothing ->
                    text ""
            ]
        ]


{-| Links rather than buttons: a page of the list is a place, so it can be opened in another
tab or sent to someone. One that does not exist is drawn without an `href`, which leaves it
in place but out of the tab order.
-}
paging : Route.BasePath -> Route.JobsParams -> Page -> Int -> Html msg
paging mount params jobsPage lastPage =
    let
        to target =
            Route.toHref mount (Route.QueueJobs { params | page = target })

        pageLink label_ target available =
            if available then
                a [ class "paging-link", href (to target) ] [ text label_ ]

            else
                a [ class "paging-link", attribute "aria-disabled" "true" ] [ text label_ ]
    in
    div [ class "paging" ]
        [ pageLink "Previous" (min lastPage (params.page - 1)) (params.page > 1)
        , span [ class "paging-count" ]
            [ text
                ("Page "
                    ++ String.fromInt params.page
                    ++ " of "
                    ++ String.fromInt lastPage
                    ++ " · "
                    ++ Format.count jobsPage.totalCount
                    ++ Format.forCount jobsPage.totalCount { one = " job", many = " jobs" }
                )
            ]
        , pageLink "Next" (params.page + 1) jobsPage.hasNextPage
        ]


boolString : Bool -> String
boolString flag =
    if flag then
        "true"

    else
        "false"
