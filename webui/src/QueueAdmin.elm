module QueueAdmin exposing (Batch, Edit(..), Form, Messages, Problem(..), Request(..), Scope, Selection(..), State, Tally, Torrents(..), answered, askPurge, askReprocess, batch, confirm, countQuery, counted, defaultForm, edit, empty, emptyScope, keep, purgeInput, purgeMutation, reprocessInput, reprocessMutation, send, toggleQueue, toggleStatus, view)

{-| Administering bitmagnet's queue: purging jobs, and queueing a batch that reprocesses
torrents. Both need `queue::mutate`, which `Route.guard` checks before this page is drawn.

Neither act is sent without a confirmation that says what it will do, and neither can be
sent any other way: `confirm` is the only thing that turns an act into a request, and only
an act that has been asked about. What the page shows afterwards is bitmagnet's answer, not
a guess at it. Every act's answer, success or failure, asks for the counts again.

The page's state answers to `Request`s rather than sending them, so what it asks bitmagnet
for, and when, is a value the tests can see. `Main` sends them.

Every control sends what it changed — a queue toggled, a box ticked, a size typed — and never
a copy of the scope or the form as it was drawn. Elm draws on the next animation frame, so a
second change made before then would carry the first one's old value and undo it.

-}

import ApiError
import Bitmagnet
import Chip
import Facet
import Format
import Graphql.Http
import Graphql.Operation exposing (RootMutation, RootQuery)
import Graphql.OptionalArgument exposing (OptionalArgument(..))
import Graphql.SelectionSet as SelectionSet exposing (SelectionSet)
import Html exposing (Html, a, button, div, fieldset, h1, h2, input, label, legend, li, p, section, span, text, ul)
import Html.Attributes exposing (attribute, checked, class, disabled, for, href, id, name, type_, value)
import Html.Events exposing (onCheck, onClick, onInput)
import Magnes.Api.Enum.ContentType as ContentType exposing (ContentType)
import Magnes.Api.Enum.QueueJobStatus as QueueJobStatus exposing (QueueJobStatus)
import Magnes.Api.InputObject as InputObject
import Magnes.Api.Mutation as Mutation
import Magnes.Api.Object
import Magnes.Api.Object.QueueJobQueueAgg as QueueAgg
import Magnes.Api.Object.QueueJobStatusAgg as StatusAgg
import Magnes.Api.Object.QueueJobsAggregations as Aggregations
import Magnes.Api.Object.QueueJobsQueryResult as JobsResult
import Magnes.Api.Object.QueueMutation as QueueMutation
import Magnes.Api.Object.QueueQuery as QueueQuery
import Magnes.Api.Query as Query
import Route


{-| Which jobs a purge deletes. No queue chosen is every queue, and no status every status:
that is how bitmagnet reads an absent list (`PurgeJobs` in its `queue/manager`).
-}
type alias Scope =
    { queues : List String
    , statuses : List QueueJobStatus
    }


emptyScope : Scope
emptyScope =
    { queues = [], statuses = [] }


{-| bitmagnet's count of the queue, for one scope. `queues` is each queue's jobs within the
chosen statuses, and `statuses` each status's jobs within the chosen queues: a facet's counts
leave its own filter out, as on the jobs page. `everything` is every job the queue holds,
which a reprocessing batch that purges first deletes.
-}
type alias Tally =
    { queues : List ( String, Int )
    , statuses : List ( QueueJobStatus, Int )
    , everything : Int
    }


{-| What the page wants of bitmagnet. `Main` sends it.
-}
type Request
    = CountJobs Scope
    | SendPurge Scope
    | SendReprocess Batch


{-| An act, as it was asked about: the purge with the number of jobs that matched, and the
batch with the number of jobs the queue held, which purging first would delete. The numbers
are kept so the question, and what is said once bitmagnet has answered, are about the act
that was confirmed, whatever has been counted since.
-}
type Act
    = Purging Scope Int
    | Reprocessing Batch (Maybe Int)


{-| One act at a time, on the whole page: a question is answered or taken back before another
is asked, and nothing is asked while an act is on its way.
-}
type Step
    = Choosing
    | Asking Act
    | Sending Act


{-| What bitmagnet said to the last act.
-}
type Outcome
    = Done Act
    | Refused Act ApiError.Failure


{-| `tally` is the last count, which draws the chips. `count` says whether it is the answer
for the scope chosen now: only then is a number of matching jobs shown, or a purge offered.
-}
type alias State =
    { scope : Scope
    , form : Form
    , tally : Maybe Tally
    , count : Count
    , step : Step
    , outcome : Maybe Outcome
    }


type Count
    = Counting
    | CountFailed ApiError.Failure
    | Counted Scope


empty : State
empty =
    { scope = emptyScope
    , form = defaultForm
    , tally = Nothing
    , count = Counting
    , step = Choosing
    , outcome = Nothing
    }


{-| A queue chosen, or unchosen, for the purge.
-}
toggleQueue : String -> State -> ( State, Request )
toggleQueue queue state =
    let
        scope =
            state.scope
    in
    withScope { scope | queues = Chip.toggle queue scope.queues } state


{-| A status chosen, or unchosen, for the purge.
-}
toggleStatus : QueueJobStatus -> State -> ( State, Request )
toggleStatus status state =
    let
        scope =
            state.scope
    in
    withScope { scope | statuses = Chip.toggle status scope.statuses } state


{-| A purge is about one scope, so choosing another asks for it to be counted afresh, and takes
back a question about the old one.
-}
withScope : Scope -> State -> ( State, Request )
withScope scope state =
    ( { state | scope = scope, count = Counting, step = settled state.step }, CountJobs scope )


{-| What a change on the page leaves of the step it was at: a question is taken back, since it
was about what has just changed, and an act on its way stays on its way.
-}
settled : Step -> Step
settled step =
    case step of
        Asking _ ->
            Choosing

        _ ->
            step


{-| One change to the reprocessing form.
-}
type Edit
    = TorrentsChosen Torrents
    | ContentTypeToggled Facet.ContentFilter
    | RematchSet Bool
    | LocalSearchSet Bool
    | ApisSet Bool
    | BatchSizeTyped String
    | ChunkSizeTyped String
    | PurgeSet Bool


{-| A change to the reprocessing form, made to the form as it is now. It takes back a question
about the batch the form made before.
-}
edit : Edit -> State -> State
edit change state =
    let
        form =
            state.form

        edited =
            case change of
                TorrentsChosen torrents ->
                    { form | torrents = torrents }

                ContentTypeToggled filter ->
                    { form | contentTypes = Chip.toggle filter form.contentTypes }

                RematchSet on ->
                    { form | rematch = on }

                LocalSearchSet on ->
                    { form | localSearch = on }

                ApisSet on ->
                    { form | apis = on }

                BatchSizeTyped typed ->
                    { form | batchSize = typed }

                ChunkSizeTyped typed ->
                    { form | chunkSize = typed }

                PurgeSet on ->
                    { form | purge = on }
    in
    { state | form = edited, step = settled state.step }


{-| Asks about queueing the batch the form makes, if it makes one. A batch that purges first
waits for the queue to be counted, so the question can say how many jobs it deletes.
-}
askReprocess : State -> State
askReprocess state =
    case ( state.step, batch state.form, everythingCounted state ) of
        ( Sending _, _, _ ) ->
            state

        ( _, Ok chosen, held ) ->
            if chosen.purge && held == Nothing then
                state

            else
                { state | step = Asking (Reprocessing chosen held), outcome = Nothing }

        ( _, Err _, _ ) ->
            state


{-| Every job the queue held at the last count, if that count is not out of date.
-}
everythingCounted : State -> Maybe Int
everythingCounted state =
    case ( state.count, state.tally ) of
        ( Counted _, Just last ) ->
            Just last.everything

        _ ->
            Nothing


{-| Asks about purging the scope chosen now, if bitmagnet has counted it and it holds a job.
-}
askPurge : State -> State
askPurge state =
    case ( state.step, matching state ) of
        ( Sending _, _ ) ->
            state

        ( _, Just n ) ->
            if n > 0 then
                { state | step = Asking (Purging state.scope n), outcome = Nothing }

            else
                state

        ( _, Nothing ) ->
            state


{-| The question taken back. Nothing is sent.
-}
keep : State -> State
keep state =
    { state | step = settled state.step }


{-| The act asked about, confirmed: the only way either act is sent.
-}
confirm : State -> ( State, Maybe Request )
confirm state =
    case state.step of
        Asking act ->
            ( { state | step = Sending act }, Just (request act) )

        _ ->
            ( state, Nothing )


{-| bitmagnet's answer to the act on its way. Whatever it says, the counts are asked for again:
after a success they are what the act left, and after a failure the act may have happened
before the failure was reported, so the count from before it is not shown as current.

An answer with no act on its way, which the page may meet after it was left and come back
to, is dropped.

-}
answered : Result ApiError.Failure () -> State -> ( State, Maybe Request )
answered answer state =
    case state.step of
        Sending act ->
            ( { state
                | step = Choosing
                , count = Counting
                , outcome =
                    Just
                        (case answer of
                            Ok () ->
                                Done act

                            Err failure ->
                                Refused act failure
                        )
              }
            , Just (CountJobs state.scope)
            )

        _ ->
            ( state, Nothing )


request : Act -> Request
request act =
    case act of
        Purging scope _ ->
            SendPurge scope

        Reprocessing chosen _ ->
            SendReprocess chosen


{-| bitmagnet's count of `scope`. An answer about a scope no longer chosen is dropped: the
page is waiting for another.
-}
counted : Scope -> Result ApiError.Failure Tally -> State -> State
counted scope answer state =
    if scope /= state.scope then
        state

    else
        case answer of
            Ok tally ->
                { state | tally = Just tally, count = Counted scope }

            Err failure ->
                { state | count = CountFailed failure }


{-| How many jobs a purge would delete now, if the last count is of the scope chosen now.
-}
matching : State -> Maybe Int
matching state =
    case ( state.count, state.tally ) of
        ( Counted scope, Just last ) ->
            if scope == state.scope then
                Just (inScope scope last)

            else
                Nothing

        _ ->
            Nothing


{-| The jobs in the chosen statuses, or in every status, within the chosen queues. The status
counts are counted within the chosen queues and leave the status filter itself out, and the
statuses are the schema's whole enum, so these add up to exactly what the purge deletes —
jobs in a queue bitmagnet does not count by name included, when no queue is chosen.
-}
inScope : Scope -> Tally -> Int
inScope scope last =
    last.statuses
        |> List.filter (\( status, _ ) -> List.isEmpty scope.statuses || List.member status scope.statuses)
        |> List.map Tuple.second
        |> List.sum



-- REQUESTS


{-| A request, sent: the count as a query, and either act as a mutation through
`Bitmagnet.mutationRequest`, the one request boundary. `counted` is told which scope it was
asked about, so an answer about one no longer chosen is not taken for the new one.
-}
send :
    String
    -> { counted : Scope -> Result (Graphql.Http.Error Tally) Tally -> msg, acted : Result (Graphql.Http.Error ()) () -> msg }
    -> Request
    -> Cmd msg
send apiUrl messages wanted =
    case wanted of
        CountJobs scope ->
            countQuery scope
                |> Bitmagnet.queryRequest apiUrl
                |> Graphql.Http.send (messages.counted scope)

        SendPurge scope ->
            purgeMutation scope
                |> Bitmagnet.mutationRequest apiUrl
                |> Graphql.Http.send messages.acted

        SendReprocess chosen ->
            reprocessMutation chosen
                |> Bitmagnet.mutationRequest apiUrl
                |> Graphql.Http.send messages.acted


{-| Both mutations answer `Void`, a null, so there is nothing to read but whether there was an
error, and any error is taken as the act failing.
-}
purgeMutation : Scope -> SelectionSet () RootMutation
purgeMutation scope =
    Mutation.queue (QueueMutation.purgeJobs { input = purgeInput scope } |> SelectionSet.map (always ()))


reprocessMutation : Batch -> SelectionSet () RootMutation
reprocessMutation chosen =
    Mutation.queue
        (QueueMutation.enqueueReprocessTorrentsBatch (\optionals -> { optionals | input = Present (reprocessInput chosen) })
            |> SelectionSet.map (always ())
        )


{-| Two looks at the jobs in one query, neither asking for a single job: the scope's counts,
and the count of everything. elm-graphql aliases the second `jobs` itself.
-}
countQuery : Scope -> SelectionSet Tally RootQuery
countQuery scope =
    Query.queue
        (SelectionSet.map2
            (\( queues, statuses ) everything -> { queues = queues, statuses = statuses, everything = everything })
            (QueueQuery.jobs
                { input =
                    InputObject.buildQueueJobsQueryInput
                        (\optionals ->
                            { optionals
                                | limit = Present 0
                                , facets =
                                    Present
                                        { status = Present { aggregate = Present True, filter = presentList scope.statuses }
                                        , queue = Present { aggregate = Present True, filter = presentList scope.queues }
                                        }
                            }
                        )
                }
                (JobsResult.aggregations aggregationsSelection)
            )
            (QueueQuery.jobs
                { input =
                    InputObject.buildQueueJobsQueryInput
                        (\optionals -> { optionals | limit = Present 0, totalCount = Present True })
                }
                JobsResult.totalCount
            )
        )


aggregationsSelection : SelectionSet ( List ( String, Int ), List ( QueueJobStatus, Int ) ) Magnes.Api.Object.QueueJobsAggregations
aggregationsSelection =
    SelectionSet.map2
        (\queues statuses -> ( Maybe.withDefault [] queues, Maybe.withDefault [] statuses ))
        (Aggregations.queue (SelectionSet.map2 Tuple.pair QueueAgg.value QueueAgg.count))
        (Aggregations.status (SelectionSet.map2 Tuple.pair StatusAgg.value StatusAgg.count))


purgeInput : Scope -> InputObject.QueuePurgeJobsInput
purgeInput scope =
    { queues = presentList scope.queues
    , statuses = presentList scope.statuses
    }


{-| An empty list is sent as no list: bitmagnet reads both the same, and absent says so.
-}
presentList : List a -> OptionalArgument (List a)
presentList values =
    if List.isEmpty values then
        Absent

    else
        Present values



-- REPROCESSING


{-| The reprocessing form as it stands, sizes as typed. `contentTypes` is kept while another
choice of torrents is made, so choosing it again brings them back.
-}
type alias Form =
    { torrents : Torrents
    , contentTypes : List Facet.ContentFilter
    , rematch : Bool
    , localSearch : Bool
    , apis : Bool
    , batchSize : String
    , chunkSize : String
    , purge : Bool
    }


{-| Which torrents the batch walks. bitmagnet applies orphans and content types together, as
two conditions on the same torrent, and no torrent can meet both: an orphan has no content
record, and a content type is read off one. So they are one choice here, as they are in the
Angular dialog.
-}
type Torrents
    = EveryTorrent
    | Orphans
    | OfContentTypes


{-| The form as it opens. The sizes are bitmagnet's own defaults (`batch.NewQueueJob`), and
looking matches up is off, as the Angular dialog has it: those lookups are what makes a
reprocess slow and, through TMDB, what it costs. Purging first is off, unlike the Angular
dialog, which has it on.
-}
defaultForm : Form
defaultForm =
    { torrents = EveryTorrent
    , contentTypes = []
    , rematch = False
    , localSearch = False
    , apis = False
    , batchSize = "100"
    , chunkSize = "10000"
    , purge = False
    }


{-| A form that can be sent, and so can be said in full before it is.
-}
type alias Batch =
    { torrents : Selection
    , rematch : Bool
    , localSearch : Bool
    , apis : Bool
    , batchSize : Int
    , chunkSize : Int
    , purge : Bool
    }


{-| The torrents a batch walks, with at least one content type when it is by content type.
-}
type Selection
    = AllTorrents
    | OrphanedTorrents
    | TorrentsOf Facet.ContentFilter (List Facet.ContentFilter)


type Problem
    = BadBatchSize
    | BadChunkSize
    | NoContentTypeChosen


batch : Form -> Result (List Problem) Batch
batch form =
    let
        selection =
            case form.torrents of
                EveryTorrent ->
                    Ok AllTorrents

                Orphans ->
                    Ok OrphanedTorrents

                OfContentTypes ->
                    case form.contentTypes of
                        first :: rest ->
                            Ok (TorrentsOf first rest)

                        [] ->
                            Err NoContentTypeChosen
    in
    case ( size form.batchSize, size form.chunkSize, selection ) of
        ( Just batchSize, Just chunkSize, Ok torrents ) ->
            Ok
                { torrents = torrents
                , rematch = form.rematch
                , localSearch = form.localSearch
                , apis = form.apis
                , batchSize = batchSize
                , chunkSize = chunkSize
                , purge = form.purge
                }

        ( batchSize, chunkSize, chosen ) ->
            Err
                (List.filterMap identity
                    [ problemIf (batchSize == Nothing) BadBatchSize
                    , problemIf (chunkSize == Nothing) BadChunkSize
                    , case chosen of
                        Err problem ->
                            Just problem

                        Ok _ ->
                            Nothing
                    ]
                )


problemIf : Bool -> Problem -> Maybe Problem
problemIf failed problem =
    if failed then
        Just problem

    else
        Nothing


{-| A number of torrents: whole, at least one, and no more than a GraphQL `Int` holds.
bitmagnet reads nought as its own default, which is not what was typed.
-}
size : String -> Maybe Int
size typed =
    String.toInt (String.trim typed)
        |> Maybe.andThen
            (\n ->
                if n >= 1 && n <= 2147483647 then
                    Just n

                else
                    Nothing
            )


{-| Every flag is sent, so what the confirmation said is what was asked for, whatever
bitmagnet's defaults become.

`classifierWorkflow` is never sent. The batch enqueue drops it: `EnqueueReprocessTorrentsBatch`
in bitmagnet's `queue/manager` builds the batch job without it, so every torrent is classified
with the default workflow whatever is asked for (checked against `trunk` at `e76818643`).
Offering it would offer a choice that does nothing.

-}
reprocessInput : Batch -> InputObject.QueueEnqueueReprocessTorrentsBatchInput
reprocessInput chosen =
    { purge = Present chosen.purge
    , batchSize = Present chosen.batchSize
    , chunkSize = Present chosen.chunkSize
    , contentTypes =
        case chosen.torrents of
            TorrentsOf first rest ->
                Present (List.map contentType (first :: rest))

            _ ->
                Absent
    , orphans = Present (chosen.torrents == OrphanedTorrents)
    , classifierRematch = Present chosen.rematch
    , classifierWorkflow = Absent
    , apisDisabled = Present (not chosen.apis)
    , localSearchDisabled = Present (not chosen.localSearch)
    }


{-| Unclassified is null, as it is in the search's filter, and bitmagnet reads it the same
way: torrents whose content record has no type (`contentTypeScope` in its batch handler).
-}
contentType : Facet.ContentFilter -> Maybe ContentType
contentType filter =
    case filter of
        Facet.Known known ->
            Just known

        Facet.Unclassified ->
            Nothing



-- VIEW


type alias Messages msg =
    { queueToggled : String -> msg
    , statusToggled : QueueJobStatus -> msg
    , edited : Edit -> msg
    , purgeAsked : msg
    , reprocessAsked : msg
    , kept : msg
    , confirmed : msg
    }


view : Route.BasePath -> Messages msg -> State -> Html msg
view mount messages state =
    div [ class "page queue-admin" ]
        [ h1 [] [ text "Queue administration" ]
        , purgePanel mount messages state
        , reprocessPanel mount messages state
        ]


purgePanel : Route.BasePath -> Messages msg -> State -> Html msg
purgePanel mount messages state =
    section [ class "panel queue-admin-panel", attribute "aria-labelledby" "purge-heading" ]
        [ h2 [ id "purge-heading" ] [ text "Purge jobs" ]
        , p [ class "queue-admin-intro" ]
            [ text "Delete jobs from bitmagnet's queue for good. Choose queues and statuses to narrow it; choosing none of either means all of them." ]
        , scopeChips messages state
        , matchLine state
        , a [ class "queue-admin-review", href (Route.toHref mount (jobsOf state.scope)) ] [ text "See them in the jobs table" ]
        , case state.step of
            Asking (Purging scope n) ->
                confirmation messages
                    { warning = purgeWarning scope n
                    , confirm = "Purge"
                    , keep = "Keep them"
                    }

            Sending (Purging _ _) ->
                p [ class "notice", attribute "role" "status" ] [ text "Purging…" ]

            _ ->
                button
                    [ type_ "button"
                    , class "danger"
                    , disabled (not (mayAskPurge state))
                    , onClick messages.purgeAsked
                    ]
                    [ text "Purge…" ]
        , case state.outcome of
            Just (Done (Purging scope _)) ->
                p [ class "queue-admin-outcome", attribute "role" "status" ]
                    [ text ("bitmagnet purged " ++ scopeSaid scope ++ ". The counts above are what it holds now.") ]

            Just (Refused (Purging _ _) failure) ->
                p [ class "notice error", attribute "role" "alert" ]
                    [ text "The purge was not done: ", text (ApiError.toMessage failure) ]

            _ ->
                text ""
        ]


{-| The jobs table, filtered to the jobs `scope` purges.
-}
jobsOf : Scope -> Route.Route
jobsOf scope =
    let
        jobs =
            Route.emptyJobs
    in
    Route.QueueJobs { jobs | queues = scope.queues, statuses = scope.statuses }


mayAskPurge : State -> Bool
mayAskPurge state =
    askPurge state /= state


{-| The question, what answering it does one fact to a line, and the two answers: the act, and
taking the question back. A reprocessing batch takes seven sentences to say, which read as a
wall in one paragraph.
-}
confirmation : Messages msg -> { warning : List String, confirm : String, keep : String } -> Html msg
confirmation messages wording =
    div [ class "queue-admin-confirm", attribute "role" "group", attribute "aria-label" "Confirm" ]
        [ case wording.warning of
            question :: facts ->
                div []
                    [ p [ class "queue-admin-question" ] [ text question ]
                    , ul [ class "queue-admin-facts" ] (List.map (\fact -> li [] [ text fact ]) facts)
                    ]

            [] ->
                text ""
        , div [ class "queue-admin-answers" ]
            [ button [ type_ "button", class "danger", onClick messages.confirmed ] [ text wording.confirm ]
            , button [ type_ "button", onClick messages.kept ] [ text wording.keep ]
            ]
        ]


{-| What purging `scope` deletes, in bitmagnet's words for queues and statuses. The number is
what matched when last counted: the queue is live, so bitmagnet deletes whatever matches when
it runs the purge, which is said too. Pending and retrying jobs are work not yet done, and
deleting them means it is not done.
-}
purgeWarning : Scope -> Int -> List String
purgeWarning scope n =
    [ "Purge " ++ scopeSaid scope ++ "?"
    , Format.count n ++ Format.forCount n { one = " job", many = " jobs" } ++ " matched when last counted."
    , if List.isEmpty scope.statuses || List.any (\status -> List.member status scope.statuses) [ QueueJobStatus.Pending, QueueJobStatus.Retry ] then
        "Jobs still waiting to run will never run."

      else
        ""
    , "bitmagnet deletes whatever matches when it purges, and it cannot be undone."
    ]
        |> List.filter (not << String.isEmpty)


{-| The jobs a purge of `scope` deletes, in bitmagnet's names for queues and statuses.
-}
scopeSaid : Scope -> String
scopeSaid scope =
    case ( scope.queues, scope.statuses ) of
        ( [], [] ) ->
            "every job in bitmagnet's queue, in every queue and status"

        ( queues, [] ) ->
            "every job in " ++ series "and" queues ++ ", whatever its status"

        ( [], statuses ) ->
            "every job in every queue with status " ++ statusesSaid statuses

        ( queues, statuses ) ->
            "every job in " ++ series "and" queues ++ " with status " ++ statusesSaid statuses


statusesSaid : List QueueJobStatus -> String
statusesSaid statuses =
    series "or" (List.map QueueJobStatus.toString statuses)


{-| "a", "a and b", "a, b and c".
-}
series : String -> List String -> String
series conjunction items =
    case List.reverse items of
        [] ->
            ""

        [ only ] ->
            only

        last :: rest ->
            String.join ", " (List.reverse rest) ++ " " ++ conjunction ++ " " ++ last


{-| The queues bitmagnet counted, and every status, in the order of a job's life. A status
with no jobs is still offered, at nought, so the choice does not hang on what is there now.
-}
scopeChips : Messages msg -> State -> Html msg
scopeChips messages state =
    let
        scope =
            state.scope

        queues =
            state.tally |> Maybe.map .queues |> Maybe.withDefault []

        statuses =
            state.tally |> Maybe.map .statuses |> Maybe.withDefault []

        countOf status =
            statuses |> List.filter (Tuple.first >> (==) status) |> List.head |> Maybe.map Tuple.second
    in
    div [ class "facets" ]
        [ Chip.facet "queue"
            (List.map
                (\( queue, n ) ->
                    Chip.view
                        { label = queue
                        , count = Just n
                        , selected = List.member queue scope.queues
                        , onToggle = messages.queueToggled queue
                        }
                )
                (withChosen scope.queues queues)
            )
        , Chip.facet "status"
            (List.map
                (\status ->
                    Chip.view
                        { label = QueueJobStatus.toString status
                        , count = Just (countOf status |> Maybe.withDefault 0)
                        , selected = List.member status scope.statuses
                        , onToggle = messages.statusToggled status
                        }
                )
                lifeOrder
            )
        ]


lifeOrder : List QueueJobStatus
lifeOrder =
    [ QueueJobStatus.Pending, QueueJobStatus.Retry, QueueJobStatus.Failed, QueueJobStatus.Processed ]


{-| A chosen queue stays offered when the count leaves it out, so it can be unchosen.
-}
withChosen : List String -> List ( String, Int ) -> List ( String, Int )
withChosen chosen countedQueues =
    countedQueues
        ++ List.filterMap
            (\queue ->
                if List.any (Tuple.first >> (==) queue) countedQueues then
                    Nothing

                else
                    Just ( queue, 0 )
            )
            chosen


matchLine : State -> Html msg
matchLine state =
    case ( state.count, matching state ) of
        ( CountFailed failure, _ ) ->
            p [ class "notice error", attribute "role" "alert" ] [ text (ApiError.toMessage failure) ]

        ( _, Just n ) ->
            p [ class "queue-admin-match", attribute "aria-live" "polite" ]
                [ text (Format.count n ++ Format.forCount n { one = " job matches.", many = " jobs match." }) ]

        ( _, Nothing ) ->
            p [ class "queue-admin-match notice", attribute "aria-live" "polite" ] [ text "Counting the jobs that match…" ]



-- REPROCESSING VIEW


{-| Every option says what it does, in the terms of bitmagnet's own source: the batch handler
in `processor/batch/queue`, the processor's `ClassifyMode`, and the classifier workflow's
flags in `classifier.core.yml`. The Angular dialog's labels name the flags rather than what
they do.
-}
reprocessPanel : Route.BasePath -> Messages msg -> State -> Html msg
reprocessPanel mount messages state =
    let
        form =
            state.form

        problems =
            case batch form of
                Ok _ ->
                    []

                Err found ->
                    found

        change =
            messages.edited
    in
    section [ class "panel queue-admin-panel", attribute "aria-labelledby" "reprocess-heading" ]
        [ h2 [ id "reprocess-heading" ] [ text "Reprocess torrents" ]
        , p [ class "queue-admin-intro" ]
            [ text "Queue a batch that classifies torrents again. On a large index it runs for a long time, and asking external APIs costs requests to them." ]
        , fieldset [ class "queue-admin-choice" ]
            [ legend [] [ text "Torrents" ]
            , radio
                { id = "reprocess-every"
                , chosen = form.torrents == EveryTorrent
                , onChoose = change (TorrentsChosen EveryTorrent)
                , label = "Every torrent in the index"
                }
            , radio
                { id = "reprocess-orphans"
                , chosen = form.torrents == Orphans
                , onChoose = change (TorrentsChosen Orphans)
                , label = "Only orphans: torrents with no content record, which search cannot find"
                }
            , radio
                { id = "reprocess-types"
                , chosen = form.torrents == OfContentTypes
                , onChoose = change (TorrentsChosen OfContentTypes)
                , label = "Only torrents whose content record is of these types"
                }
            , if form.torrents == OfContentTypes then
                div [ class "chips queue-admin-types" ]
                    (List.map
                        (\filter ->
                            Chip.view
                                { label = Facet.contentLabel filter
                                , count = Nothing
                                , selected = List.member filter form.contentTypes
                                , onToggle = change (ContentTypeToggled filter)
                                }
                        )
                        contentFilters
                    )

              else
                text ""
            , problemSaid problems NoContentTypeChosen "Choose at least one content type, or another choice of torrents."
            ]
        , option
            { id = "reprocess-rematch"
            , on = form.rematch
            , onCheck = change << RematchSet
            , label = "Classify from scratch"
            , hint = "Ignore each torrent's existing match instead of using it as a hint. A torrent this run cannot match again loses its match."
            }
        , option
            { id = "reprocess-local-search"
            , on = form.localSearch
            , onCheck = change << LocalSearchSet
            , label = "Search bitmagnet's own content table"
            , hint = "For a torrent with no match, search the content bitmagnet already holds by the title parsed from its name."
            }
        , option
            { id = "reprocess-apis"
            , on = form.apis
            , onCheck = change << ApisSet
            , label = "Ask external APIs such as TMDB"
            , hint = "Look matches up through TMDB, by ID and by title, where bitmagnet is set up for it. Left off, the jobs this batch queues run ahead of the rest of the queue."
            }
        , sizeField
            { id = "reprocess-batch-size"
            , typed = form.batchSize
            , onInput = change << BatchSizeTyped
            , label = "Torrents per processing job"
            , hint = "How many torrents each process_torrent job classifies. bitmagnet's default is 100."
            , problem = problemSaid problems BadBatchSize "A whole number of torrents, at least 1."
            }
        , sizeField
            { id = "reprocess-chunk-size"
            , typed = form.chunkSize
            , onInput = change << ChunkSizeTyped
            , label = "Torrents per batch job"
            , hint = "How many torrents one process_torrent_batch job queues jobs for before it hands the rest on to a new batch job. bitmagnet's default is 10,000."
            , problem = problemSaid problems BadChunkSize "A whole number of torrents, at least 1."
            }
        , option
            { id = "reprocess-purge"
            , on = form.purge
            , onCheck = change << PurgeSet
            , label = "Delete every job in the queue first"
            , hint = "Empties bitmagnet's whole queue, every queue and status, in the same transaction that queues the batch."
            }
        , case state.step of
            Asking (Reprocessing chosen held) ->
                confirmation messages
                    { warning = reprocessWarning chosen held
                    , confirm = "Queue the batch"
                    , keep = "Don't queue it"
                    }

            Sending (Reprocessing _ _) ->
                p [ class "notice", attribute "role" "status" ] [ text "Queueing the batch…" ]

            _ ->
                button
                    [ type_ "button"
                    , class "danger"
                    , disabled (askReprocess state == state)
                    , onClick messages.reprocessAsked
                    ]
                    [ text "Reprocess…" ]
        , case state.outcome of
            Just (Done (Reprocessing chosen _)) ->
                p [ class "queue-admin-outcome", attribute "role" "status" ]
                    [ text
                        (if chosen.purge then
                            "bitmagnet emptied the queue and queued the batch. "

                         else
                            "bitmagnet queued the batch. "
                        )
                    , a [ href (Route.toHref mount (Route.QueueJobs Route.emptyJobs)) ] [ text "Watch it in the queue's jobs" ]
                    , text ", newest first."
                    ]

            Just (Refused (Reprocessing _ _) failure) ->
                p [ class "notice error", attribute "role" "alert" ]
                    [ text "The batch was not queued: ", text (ApiError.toMessage failure) ]

            _ ->
                text ""
        ]


{-| Every content type, then unclassified, which is a content record with no type: bitmagnet
reads a null in the list as that (`contentTypeScope` in its batch handler).
-}
contentFilters : List Facet.ContentFilter
contentFilters =
    List.map Facet.Known ContentType.list ++ [ Facet.Unclassified ]


radio : { id : String, chosen : Bool, onChoose : msg, label : String } -> Html msg
radio config =
    label [ class "queue-admin-option", for config.id ]
        [ input
            [ type_ "radio"
            , id config.id
            , name "reprocess-torrents"
            , checked config.chosen
            , onCheck (\_ -> config.onChoose)
            ]
            []
        , span [] [ text config.label ]
        ]


option : { id : String, on : Bool, onCheck : Bool -> msg, label : String, hint : String } -> Html msg
option config =
    div [ class "queue-admin-field" ]
        [ label [ class "queue-admin-option", for config.id ]
            [ input
                [ type_ "checkbox"
                , id config.id
                , checked config.on
                , onCheck config.onCheck
                , attribute "aria-describedby" (config.id ++ "-hint")
                ]
                []
            , span [] [ text config.label ]
            ]
        , p [ class "queue-admin-hint", id (config.id ++ "-hint") ] [ text config.hint ]
        ]


{-| Typed as text, with a numeric keyboard, so what was typed stays as it was typed and the
problem with it can be said; a number field would hand back nothing for "1.5".
-}
sizeField : { id : String, typed : String, onInput : String -> msg, label : String, hint : String, problem : Html msg } -> Html msg
sizeField config =
    div [ class "queue-admin-field" ]
        [ label [ for config.id ] [ text config.label ]
        , input
            [ type_ "text"
            , id config.id
            , attribute "inputmode" "numeric"
            , value config.typed
            , onInput config.onInput
            , attribute "aria-describedby" (config.id ++ "-hint")
            ]
            []
        , p [ class "queue-admin-hint", id (config.id ++ "-hint") ] [ text config.hint ]
        , config.problem
        ]


problemSaid : List Problem -> Problem -> String -> Html msg
problemSaid found which message =
    if List.member which found then
        p [ class "notice error field-error" ] [ text message ]

    else
        text ""


{-| What the batch will do, in the order a person needs it: what it deletes, if it purges
first, then which torrents, how they are walked, and what is done to each. Read from
bitmagnet's batch handler (`processor/batch/queue`): it walks torrents last updated before the
batch was queued, in info-hash order, `batchSize` at a time, and queues a successor after
`chunkSize`.
-}
reprocessWarning : Batch -> Maybe Int -> List String
reprocessWarning chosen held =
    let
        perJob =
            Format.forCount chosen.batchSize
                { one = "for each of them"
                , many = "for every " ++ Format.count chosen.batchSize ++ " of them"
                }
    in
    [ if chosen.purge then
        "Delete every job in bitmagnet's queue, then queue a batch that reprocesses " ++ torrentsSaid chosen.torrents ++ "?"

      else
        "Queue a batch that reprocesses " ++ torrentsSaid chosen.torrents ++ "?"
    , case ( chosen.purge, held ) of
        ( True, Just n ) ->
            Format.count n
                ++ Format.forCount n { one = " job was", many = " jobs were" }
                ++ " in the queue when last counted, in every queue and status. Jobs still waiting to run will never run, and the deletion cannot be undone."

        _ ->
            ""
    , "bitmagnet queues one process_torrent_batch job, which walks the torrents last updated before it was queued and queues a process_torrent job "
        ++ perJob
        ++ "; after "
        ++ Format.count chosen.chunkSize
        ++ Format.forCount chosen.chunkSize { one = " torrent", many = " torrents" }
        ++ " it hands the rest on to a new batch job."
    , if chosen.rematch then
        "Each torrent is classified from scratch: its existing match is ignored, and a torrent this run cannot match again loses its match."

      else
        "Each torrent is classified again, with its existing match as a hint."
    , if chosen.localSearch then
        "A torrent with no match is searched for by title in bitmagnet's own content table."

      else
        "bitmagnet's own content table is not searched by title."
    , if chosen.apis then
        "External APIs such as TMDB are asked for matches, where bitmagnet is set up for them."

      else
        "No external API such as TMDB is asked."
    , "On a large index this is a long and expensive run."
    ]
        |> List.filter (not << String.isEmpty)


torrentsSaid : Selection -> String
torrentsSaid selection =
    case selection of
        AllTorrents ->
            "every torrent in the index"

        OrphanedTorrents ->
            "every torrent with no content record, which search cannot find"

        TorrentsOf first rest ->
            "every torrent whose content record is " ++ series "or" (List.map Facet.contentLabel (first :: rest))
