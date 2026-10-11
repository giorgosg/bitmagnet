module QueueAdmin exposing
    ( Batch
    , BatchTorrents(..)
    , Call
    , Edit(..)
    , Form
    , Messages
    , Options
    , Problem(..)
    , Scope
    , Sizes
    , State
    , Tally
    , Torrents(..)
    , Whereabouts
    , answered
    , askPurge
    , askReprocess
    , batch
    , cancel
    , confirm
    , countQuery
    , counted
    , defaultForm
    , document
    , edit
    , empty
    , emptyScope
    , opening
    , purgeInput
    , purgeMutation
    , recount
    , reprocessInput
    , reprocessMutation
    , send
    , settle
    , toggleQueue
    , toggleStatus
    , view
    )

{-| Administering bitmagnet's queue: purging jobs, and queueing a batch that reprocesses
torrents. Both need `queue::mutate`, which `Route.guard` checks before this page is drawn.

The page's state does not send anything. Each change to it returns the `Call` to bitmagnet it
needs, if any, as a value, and `Main` sends it with `send`; `Main` builds none of its own but
`opening`. So what the page asks bitmagnet for, and when, is a value the tests can read with
`document`. An act is only ever in a `Call` that `confirm` returned, and `confirm` returns one
only for an act that has been asked about: neither act is sent without a question that says
what it will do. Every act's answer asks for the counts again, so what the page shows
afterwards is bitmagnet's count, not a guess at it.

Each act is numbered, and its answer carries its `Call` back, so the answer to one act is never
taken for another's. What becomes of the page as the person moves, or as their Identity
changes, is decided here too (`settle`), from plain values `Main` hands over after every update:
an act on its way outlasts both, and nothing else is asked about until it is answered (the
mutation is given up on after five minutes, so it is answered one way or another); what became
of an act is kept until it has been on screen; and an answer asks for a recount only while the
page is showing and allowed.

Every control sends what it changed — a queue toggled, a box ticked, a size typed — and never
a copy of the scope or the form as it was drawn. Elm draws on the next animation frame, so a
second change made before then would carry the first one's old value and undo it.

-}

import ApiError
import Bitmagnet
import Chip
import Facet
import Format
import Graphql.Document
import Graphql.Http
import Graphql.Operation exposing (RootMutation, RootQuery)
import Graphql.OptionalArgument exposing (OptionalArgument(..))
import Graphql.SelectionSet as SelectionSet exposing (SelectionSet)
import Html exposing (Html, a, button, div, fieldset, h1, h2, input, label, legend, li, p, section, span, text, ul)
import Html.Attributes exposing (attribute, checked, class, disabled, for, href, id, name, type_, value)
import Html.Events exposing (onCheck, onClick, onInput)
import Identity
import Magnes.Api.Enum.QueueJobStatus as QueueJobStatus exposing (QueueJobStatus)
import Magnes.Api.InputObject as InputObject
import Magnes.Api.Mutation as Mutation
import Magnes.Api.Object.QueueJobsQueryResult as JobsResult
import Magnes.Api.Object.QueueMutation as QueueMutation
import Magnes.Api.Object.QueueQuery as QueueQuery
import Magnes.Api.Query as Query
import QueueJobs
import Route



-- STATE


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
leave its own filter out, as on the jobs page. `everything` is every job the queue holds.
-}
type alias Tally =
    { queues : List ( String, Int )
    , statuses : List ( QueueJobStatus, Int )
    , everything : Int
    }


{-| bitmagnet's count of the scope chosen now, or the reason there is none. While there is
none, the last count, if any, is kept only for its queues' names, so the chips stay where they
were; its numbers are no longer the answer, and are not shown.
-}
type Count
    = Counting (Maybe Tally)
    | CountFailed ApiError.Failure (Maybe Tally)
    | Counted Tally


{-| An act, as it was asked about: the purge with the number of jobs that matched, and the
batch with the number of jobs the queue held, if it had been counted: a batch that purges first
is asked about only with that number, since it deletes them all, and one that does not can do
without. The numbers are kept so the question, and what is said once bitmagnet has answered,
are about the act that was confirmed, whatever has been counted since.
-}
type Act
    = Purging Scope Int
    | Reprocessing Batch (Maybe Int)


{-| One act at a time, on the whole page: a question is answered or taken back before another
is asked, and nothing is asked while an act is on its way. An act on its way carries the
number its answer will carry back.
-}
type Step
    = Choosing
    | Asking Act
    | Sending Int Act


{-| What became of the last act. A GraphQL error is bitmagnet answering that it did not do it;
anything else — no answer, an answer that was not bitmagnet's, one that could not be read, or
none within the timeout — leaves it unknown, because the database may have committed before
the answer was lost.
-}
type Outcome
    = Done Act
    | NotDone Act ApiError.Failure
    | Unknown Act ApiError.Failure


{-| An outcome, and whether the page has been on screen since it came: one that has not is kept
until it has, however the person moves, because it is the answer to something destructive they
asked for.
-}
type Report
    = Untold Outcome
    | Told Outcome


{-| `acts` is how many acts have been confirmed, which numbers the next.
-}
type alias State =
    { scope : Scope
    , form : Form
    , count : Count
    , step : Step
    , acts : Int
    , report : Maybe Report
    }


empty : State
empty =
    { scope = emptyScope
    , form = defaultForm
    , count = Counting Nothing
    , step = Choosing
    , acts = 0
    , report = Nothing
    }


{-| Where the person is: the route they are on, the Identity they hold, and what `Route.guard`
says of the one for the other. `Main` hands it over after every update.
-}
type alias Whereabouts =
    { route : Route.Route
    , identity : Identity.Identity
    , access : Route.Access
    }


{-| The page is on screen.
-}
showing : Whereabouts -> Bool
showing whereabouts =
    whereabouts.route == Route.AdminQueue && whereabouts.access == Route.Allowed


{-| What an update that moved the person from `before` to `after` does to the page. Moving to
another route, or holding another Identity, leaves the page (`leave`); and once it is on
screen, what became of the last act has been told.
-}
settle : { before : Whereabouts, after : Whereabouts } -> State -> State
settle { before, after } state =
    let
        moved =
            if before == after then
                state

            else
                leave state
    in
    if showing after then
        { moved | report = Maybe.map (Told << reported) moved.report }

    else
        moved


{-| The page left, or its Identity changed: everything on it goes but an act still on its way,
whose answer is still to come, the count of acts that numbers the next, and an outcome not yet
told. Until the act is answered nothing else is asked about, so two are never on their way at
once.
-}
leave : State -> State
leave state =
    { empty
        | step =
            case state.step of
                Sending number act ->
                    Sending number act

                Choosing ->
                    Choosing

                Asking _ ->
                    Choosing
        , acts = state.acts
        , report =
            case state.report of
                Just (Untold outcome) ->
                    Just (Untold outcome)

                Just (Told _) ->
                    Nothing

                Nothing ->
                    Nothing
    }


reported : Report -> Outcome
reported report =
    case report of
        Untold outcome ->
            outcome

        Told outcome ->
            outcome


{-| A queue chosen, or unchosen, for the purge.
-}
toggleQueue : String -> State -> ( State, Call )
toggleQueue queue state =
    let
        scope =
            state.scope
    in
    withScope { scope | queues = Chip.toggle queue scope.queues } state


{-| A status chosen, or unchosen, for the purge.
-}
toggleStatus : QueueJobStatus -> State -> ( State, Call )
toggleStatus status state =
    let
        scope =
            state.scope
    in
    withScope { scope | statuses = Chip.toggle status scope.statuses } state


{-| The scope chosen now counted again, as after a count that failed.
-}
recount : State -> ( State, Call )
recount state =
    withScope state.scope state


{-| A purge is about one scope, so choosing another asks for it to be counted afresh, and takes
back a question about the old one.
-}
withScope : Scope -> State -> ( State, Call )
withScope scope state =
    ( { state | scope = scope, count = Counting (lastTally state.count), step = settled state.step }
    , CountCall scope
    )


{-| bitmagnet's count of `scope`. An answer about a scope no longer chosen is dropped: the
page is waiting for another. (`Main` drops an answer to an older request for the same scope by
its epoch.)
-}
counted : Scope -> Result ApiError.Failure Tally -> State -> State
counted scope answer state =
    if scope /= state.scope then
        state

    else
        case answer of
            Ok tally ->
                { state | count = Counted tally }

            Err failure ->
                { state | count = CountFailed failure (lastTally state.count) }


lastTally : Count -> Maybe Tally
lastTally count =
    case count of
        Counting last ->
            last

        CountFailed _ last ->
            last

        Counted tally ->
            Just tally


{-| What a change on the page leaves of the step it was at: a question is taken back, since it
was about what has just changed, and an act on its way stays on its way.
-}
settled : Step -> Step
settled step =
    case step of
        Asking _ ->
            Choosing

        Choosing ->
            Choosing

        Sending number act ->
            Sending number act


{-| The jobs in the chosen statuses, or in every status, within the chosen queues. The status
counts are counted within the chosen queues and leave the status filter itself out, and the
statuses are the schema's whole enum, so these add up to exactly what the purge deletes —
jobs in a queue bitmagnet does not count by name included, when no queue is chosen.
-}
inScope : Scope -> Tally -> Int
inScope scope tally =
    tally.statuses
        |> List.filter (\( status, _ ) -> List.isEmpty scope.statuses || List.member status scope.statuses)
        |> List.map Tuple.second
        |> List.sum



-- REPROCESSING FORM


{-| The reprocessing form as it stands, sizes as typed. `contentTypes` is kept while another
choice of torrents is made, so choosing it again brings them back.
-}
type alias Form =
    { torrents : Torrents
    , contentTypes : List Facet.ContentFilter
    , options : Options
    , sizes : Sizes String
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


{-| What the form and the batch it makes share as they are.
-}
type alias Options =
    { rematch : Bool
    , localSearch : Bool
    , apis : Bool
    , purge : Bool
    }


{-| How many torrents each `process_torrent` job classifies, and how many one
`process_torrent_batch` job walks before handing the rest on: typed, then counted.
-}
type alias Sizes a =
    { batchSize : a
    , chunkSize : a
    }


{-| The form as it opens. The sizes are bitmagnet's own defaults (`batch.NewQueueJob`), and
looking matches up is off, as the Angular dialog has it: those lookups are what makes a
reprocess slow and, through TMDB, what it costs. Purging first is off, unlike the Angular
dialog, which has it on.
-}
defaultForm : Form
defaultForm =
    { torrents = EveryTorrent
    , contentTypes = []
    , options = { rematch = False, localSearch = False, apis = False, purge = False }
    , sizes = { batchSize = "100", chunkSize = "10000" }
    }


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

        options =
            form.options

        sizes =
            form.sizes

        edited =
            case change of
                TorrentsChosen torrents ->
                    { form | torrents = torrents }

                ContentTypeToggled filter ->
                    { form | contentTypes = Chip.toggle filter form.contentTypes }

                RematchSet on ->
                    { form | options = { options | rematch = on } }

                LocalSearchSet on ->
                    { form | options = { options | localSearch = on } }

                ApisSet on ->
                    { form | options = { options | apis = on } }

                BatchSizeTyped typed ->
                    { form | sizes = { sizes | batchSize = typed } }

                ChunkSizeTyped typed ->
                    { form | sizes = { sizes | chunkSize = typed } }

                PurgeSet on ->
                    { form | options = { options | purge = on } }
    in
    { state | form = edited, step = settled state.step }


{-| A form that can be sent, and so can be said in full before it is.
-}
type alias Batch =
    { torrents : BatchTorrents
    , options : Options
    , sizes : Sizes Int
    }


{-| The torrents a batch walks, with at least one content type when it is by content type.
-}
type BatchTorrents
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
        torrents =
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
    case ( size form.sizes.batchSize, size form.sizes.chunkSize, torrents ) of
        ( Just batchSize, Just chunkSize, Ok chosen ) ->
            Ok { torrents = chosen, options = form.options, sizes = { batchSize = batchSize, chunkSize = chunkSize } }

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


{-| How many torrents one batch job walks before it hands the rest on. bitmagnet's handler
counts the torrents after each processing job it queues, and hands on at the first count at or
past `chunkSize`: the chunk size rounded up to a whole number of processing jobs.
-}
handOn : Sizes Int -> Int
handOn sizes =
    ((sizes.chunkSize + sizes.batchSize - 1) // sizes.batchSize) * sizes.batchSize



-- ASKING


{-| The purge that may be asked about now: of the scope chosen now, once bitmagnet has counted
it, if it holds a job, and while no act is on its way.
-}
purgeAskable : State -> Maybe Act
purgeAskable state =
    if onItsWay state.step then
        Nothing

    else
        case state.count of
            Counted tally ->
                case inScope state.scope tally of
                    0 ->
                        Nothing

                    n ->
                        Just (Purging state.scope n)

            Counting _ ->
                Nothing

            CountFailed _ _ ->
                Nothing


{-| The batch that may be asked about now: the one the form makes, if it makes one, while no
act is on its way. One that purges first waits for a current count of the queue, so the
question can say how many jobs it deletes; one that does not needs none, and is not held up by a
count on its way or one that failed.
-}
reprocessAskable : State -> Maybe Act
reprocessAskable state =
    case ( onItsWay state.step, batch state.form ) of
        ( False, Ok chosen ) ->
            case ( chosen.options.purge, state.count ) of
                ( _, Counted tally ) ->
                    Just (Reprocessing chosen (Just tally.everything))

                ( True, Counting _ ) ->
                    Nothing

                ( True, CountFailed _ _ ) ->
                    Nothing

                ( False, Counting _ ) ->
                    Just (Reprocessing chosen Nothing)

                ( False, CountFailed _ _ ) ->
                    Just (Reprocessing chosen Nothing)

        ( False, Err _ ) ->
            Nothing

        ( True, _ ) ->
            Nothing


onItsWay : Step -> Bool
onItsWay step =
    case step of
        Sending _ _ ->
            True

        Choosing ->
            False

        Asking _ ->
            False


askPurge : State -> State
askPurge state =
    ask (purgeAskable state) state


askReprocess : State -> State
askReprocess state =
    ask (reprocessAskable state) state


ask : Maybe Act -> State -> State
ask askable state =
    case askable of
        Just act ->
            { state | step = Asking act, report = Nothing }

        Nothing ->
            state


{-| The question taken back. Nothing is sent.
-}
cancel : State -> State
cancel state =
    { state | step = settled state.step }


{-| The act asked about, confirmed and numbered: the only way either act is sent.
-}
confirm : State -> ( State, Maybe Call )
confirm state =
    let
        number =
            state.acts + 1
    in
    case state.step of
        Asking act ->
            ( { state | step = Sending number act, acts = number }
            , Just
                (case act of
                    Purging scope _ ->
                        PurgeCall number scope

                    Reprocessing chosen _ ->
                        ReprocessCall number chosen
                )
            )

        Choosing ->
            ( state, Nothing )

        Sending _ _ ->
            ( state, Nothing )


{-| bitmagnet's answer to `call`, which lands while the person is `there`. An answer to any act
but the one on its way is dropped. Whatever it says, the counts are asked for again, if the page
is showing to draw them: after a success they are what the act left, and after a failure the act
may have happened anyway, so the count from before it is not shown as current. A page that is
not showing counts afresh when it is opened, and one that is refused would only be refused the
count too.
-}
answered : Whereabouts -> Call -> Result (Graphql.Http.Error ()) () -> State -> ( State, Maybe Call )
answered there call answer state =
    case state.step of
        Sending sent act ->
            if actNumber call /= Just sent then
                ( state, Nothing )

            else
                ( { state
                    | step = Choosing
                    , count = Counting (lastTally state.count)
                    , report = Just (Untold (outcomeOf act answer))
                  }
                , if showing there then
                    Just (CountCall state.scope)

                  else
                    Nothing
                )

        Choosing ->
            ( state, Nothing )

        Asking _ ->
            ( state, Nothing )


outcomeOf : Act -> Result (Graphql.Http.Error ()) () -> Outcome
outcomeOf act answer =
    case answer of
        Ok () ->
            Done act

        Err ((Graphql.Http.GraphqlError _ _) as error) ->
            NotDone act (ApiError.fromError error)

        Err ((Graphql.Http.HttpError _) as error) ->
            Unknown act (ApiError.fromError error)



-- CALLS


{-| Something the page asks of bitmagnet: a count, or an act numbered by `Main`.
-}
type Call
    = CountCall Scope
    | PurgeCall Int Scope
    | ReprocessCall Int Batch


actNumber : Call -> Maybe Int
actNumber call =
    case call of
        CountCall _ ->
            Nothing

        PurgeCall number _ ->
            Just number

        ReprocessCall number _ ->
            Just number


{-| What the page asks for as it opens: a count of every job.
-}
opening : Call
opening =
    CountCall emptyScope


{-| A call, sent: the count as a query, and either act as a mutation through
`Bitmagnet.slowMutationRequest`, with the timeout that keeps an act from being on its way for
good. `counted` is told which scope it was asked about, and `acted` which call it answers.
-}
send :
    String
    -> { counted : Scope -> Result (Graphql.Http.Error Tally) Tally -> msg, acted : Call -> Result (Graphql.Http.Error ()) () -> msg }
    -> Call
    -> Cmd msg
send apiUrl messages call =
    case call of
        CountCall scope ->
            countQuery scope
                |> Bitmagnet.queryRequest apiUrl
                |> Graphql.Http.send (messages.counted scope)

        PurgeCall _ scope ->
            purgeMutation scope
                |> Bitmagnet.slowMutationRequest apiUrl
                |> Graphql.Http.send (messages.acted call)

        ReprocessCall _ chosen ->
            reprocessMutation chosen
                |> Bitmagnet.slowMutationRequest apiUrl
                |> Graphql.Http.send (messages.acted call)


{-| The GraphQL document a call sends.
-}
document : Call -> String
document call =
    case call of
        CountCall scope ->
            Graphql.Document.serializeQuery (countQuery scope)

        PurgeCall _ scope ->
            Graphql.Document.serializeMutation (purgeMutation scope)

        ReprocessCall _ chosen ->
            Graphql.Document.serializeMutation (reprocessMutation chosen)


{-| Two looks at the jobs in one query, neither asking for a single job: the scope's counts,
by the jobs page's facets, and the count of everything. elm-graphql aliases the second `jobs`
itself.
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
                                        { status = Present { aggregate = Present True, filter = Bitmagnet.optionalList scope.statuses }
                                        , queue = Present { aggregate = Present True, filter = Bitmagnet.optionalList scope.queues }
                                        }
                            }
                        )
                }
                (JobsResult.aggregations QueueJobs.facetCounts)
            )
            (QueueQuery.jobs
                { input =
                    InputObject.buildQueueJobsQueryInput
                        (\optionals -> { optionals | limit = Present 0, totalCount = Present True })
                }
                JobsResult.totalCount
            )
        )


{-| Both mutations answer `Void`, a null, so there is nothing to read but whether there was an
error.
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


purgeInput : Scope -> InputObject.QueuePurgeJobsInput
purgeInput scope =
    { queues = Bitmagnet.optionalList scope.queues
    , statuses = Bitmagnet.optionalList scope.statuses
    }


{-| Every flag is sent, so what the confirmation said is what was asked for, whatever
bitmagnet's defaults become.

`classifierWorkflow` is never sent. The batch enqueue drops it: `EnqueueReprocessTorrentsBatch`
in bitmagnet's `queue/manager` builds the batch job without it, so every torrent is classified
with the default workflow whatever is asked for (checked against `trunk` at `e76818643`).
Offering it would offer a choice that does nothing.

-}
reprocessInput : Batch -> InputObject.QueueEnqueueReprocessTorrentsBatchInput
reprocessInput chosen =
    { purge = Present chosen.options.purge
    , batchSize = Present chosen.sizes.batchSize
    , chunkSize = Present chosen.sizes.chunkSize
    , contentTypes =
        case chosen.torrents of
            TorrentsOf first rest ->
                Present (List.map Bitmagnet.toContentType (first :: rest))

            AllTorrents ->
                Absent

            OrphanedTorrents ->
                Absent
    , orphans = Present (chosen.torrents == OrphanedTorrents)
    , classifierRematch = Present chosen.options.rematch
    , classifierWorkflow = Absent
    , apisDisabled = Present (not chosen.options.apis)
    , localSearchDisabled = Present (not chosen.options.localSearch)
    }



-- VIEW


type alias Messages msg =
    { queueToggled : String -> msg
    , statusToggled : QueueJobStatus -> msg
    , edited : Edit -> msg
    , recountAsked : msg
    , purgeAsked : msg
    , reprocessAsked : msg
    , cancelled : msg
    , confirmed : msg
    }


view : Route.BasePath -> Messages msg -> State -> Html msg
view mount messages state =
    div [ class "page queue-admin" ]
        [ h1 [] [ text "Queue administration" ]
        , purgePanel mount messages state
        , reprocessPanel mount messages state
        ]


{-| A question, what answering it does one fact to a line, and the two answers: the act, and
taking the question back. A reprocessing batch takes eight sentences to say, which read as a
wall in one paragraph.
-}
type alias Question =
    { question : String
    , facts : List String
    , confirm : String
    , cancel : String
    }


confirmation : Messages msg -> Question -> Html msg
confirmation messages asked =
    div [ class "queue-admin-confirm", attribute "role" "group", attribute "aria-label" "Confirm" ]
        [ p [ class "queue-admin-question" ] [ text asked.question ]
        , ul [ class "queue-admin-facts" ] (List.map (\fact -> li [] [ text fact ]) asked.facts)
        , div [ class "queue-admin-answers" ]
            [ button [ type_ "button", class "danger", onClick messages.confirmed ] [ text asked.confirm ]
            , button [ type_ "button", onClick messages.cancelled ] [ text asked.cancel ]
            ]
        ]


purgePanel : Route.BasePath -> Messages msg -> State -> Html msg
purgePanel mount messages state =
    section [ class "panel queue-admin-panel", attribute "aria-labelledby" "purge-heading" ]
        [ h2 [ id "purge-heading" ] [ text "Purge jobs" ]
        , p [ class "queue-admin-intro" ]
            [ text "Delete jobs from bitmagnet's queue for good. Choose queues and statuses to narrow it; choosing none of either means all of them." ]
        , scopeChips messages state
        , matchLine messages state
        , a [ class "queue-admin-review", href (Route.toHref mount (jobsOf state.scope)) ] [ text "See them in the jobs table" ]
        , case state.step of
            Asking (Purging scope n) ->
                confirmation messages (purgeQuestion scope n)

            Sending _ (Purging scope _) ->
                p [ class "notice", attribute "role" "status" ] [ text ("Purging " ++ scopeSaid scope ++ "…") ]

            Asking (Reprocessing _ _) ->
                purgeButton messages state

            Sending _ (Reprocessing _ _) ->
                purgeButton messages state

            Choosing ->
                purgeButton messages state
        , case Maybe.map reported state.report of
            Just (Done (Purging scope _)) ->
                p [ class "queue-admin-outcome", attribute "role" "status" ]
                    [ text ("bitmagnet purged " ++ scopeSaid scope ++ ".")
                    , case state.count of
                        Counted _ ->
                            text " The counts above are what it holds now."

                        Counting _ ->
                            text ""

                        CountFailed _ _ ->
                            text ""
                    ]

            Just (NotDone (Purging _ _) failure) ->
                p [ class "notice error", attribute "role" "alert" ]
                    [ text "bitmagnet did not purge the jobs: ", text (ApiError.toMessage failure) ]

            Just (Unknown (Purging scope _) failure) ->
                p [ class "notice error", attribute "role" "alert" ]
                    [ text ("No usable answer to the purge came back. " ++ ApiError.toMessage failure ++ " Whether bitmagnet purged them is not known. ")
                    , a [ href (Route.toHref mount (jobsOf scope)) ] [ text "Check the jobs table" ]
                    , text " before purging again."
                    ]

            Just (Done (Reprocessing _ _)) ->
                text ""

            Just (NotDone (Reprocessing _ _) _) ->
                text ""

            Just (Unknown (Reprocessing _ _) _) ->
                text ""

            Nothing ->
                text ""
        ]


purgeButton : Messages msg -> State -> Html msg
purgeButton messages state =
    button
        [ type_ "button"
        , class "danger"
        , disabled (purgeAskable state == Nothing)
        , onClick messages.purgeAsked
        ]
        [ text "Purge…" ]


{-| The jobs table, filtered to the jobs `scope` purges.
-}
jobsOf : Scope -> Route.Route
jobsOf scope =
    let
        jobs =
            Route.emptyJobs
    in
    Route.QueueJobs { jobs | queues = scope.queues, statuses = scope.statuses }


{-| What purging `scope` deletes, in bitmagnet's words for queues and statuses. The number is
what matched when last counted: the queue is live, so bitmagnet deletes whatever matches when
it runs the purge, which is said too. Pending and retrying jobs are work not yet done, and
deleting them means it is not done.
-}
purgeQuestion : Scope -> Int -> Question
purgeQuestion scope n =
    { question = "Purge " ++ scopeSaid scope ++ "?"
    , facts =
        List.filterMap identity
            [ Just (Format.count n ++ Format.forCount n { one = " job", many = " jobs" } ++ " matched when last counted.")
            , if List.isEmpty scope.statuses || List.any (\status -> List.member status scope.statuses) [ QueueJobStatus.Pending, QueueJobStatus.Retry ] then
                Just "Jobs still waiting to run will never run."

              else
                Nothing
            , Just "bitmagnet deletes whatever matches when it purges, and it cannot be undone."
            ]
    , confirm = "Purge"
    , cancel = "Keep them"
    }


{-| The jobs a purge of `scope` deletes, in bitmagnet's names for queues and statuses.
-}
scopeSaid : Scope -> String
scopeSaid scope =
    case ( scope.queues, scope.statuses ) of
        ( [], [] ) ->
            "every job in bitmagnet's queue, in every queue and status"

        ( queues, [] ) ->
            "every job in " ++ Format.series "and" queues ++ ", whatever its status"

        ( [], statuses ) ->
            "every job in every queue with status " ++ statusesSaid statuses

        ( queues, statuses ) ->
            "every job in " ++ Format.series "and" queues ++ " with status " ++ statusesSaid statuses


statusesSaid : List QueueJobStatus -> String
statusesSaid statuses =
    Format.series "or" (List.map QueueJobStatus.toString statuses)


{-| The queues bitmagnet counted, and every status, in the order of a job's life. A status
with no jobs is still offered, at nought, so the choice does not hang on what is there now.
A chip shows a count only while it is bitmagnet's answer for the scope chosen now.
-}
scopeChips : Messages msg -> State -> Html msg
scopeChips messages state =
    let
        scope =
            state.scope

        current =
            case state.count of
                Counted tally ->
                    Just tally

                Counting _ ->
                    Nothing

                CountFailed _ _ ->
                    Nothing

        queues =
            lastTally state.count |> Maybe.map .queues |> Maybe.withDefault []

        countOf status =
            current
                |> Maybe.map
                    (.statuses
                        >> List.filter (Tuple.first >> (==) status)
                        >> List.head
                        >> Maybe.map Tuple.second
                        >> Maybe.withDefault 0
                    )
    in
    div [ class "facets" ]
        [ Chip.facet "queue"
            (List.map
                (\( queue, n ) ->
                    Chip.view
                        { label = queue
                        , count = Maybe.map (always n) current
                        , selected = List.member queue scope.queues
                        , onToggle = messages.queueToggled queue
                        }
                )
                (QueueJobs.withChosen scope.queues queues)
            )
        , Chip.facet "status"
            (List.map
                (\status ->
                    Chip.view
                        { label = QueueJobStatus.toString status
                        , count = countOf status
                        , selected = List.member status scope.statuses
                        , onToggle = messages.statusToggled status
                        }
                )
                QueueJobs.statuses
            )
        ]


matchLine : Messages msg -> State -> Html msg
matchLine messages state =
    case state.count of
        CountFailed failure _ ->
            div [ class "queue-admin-count-failed" ]
                [ p [ class "notice error", attribute "role" "alert" ] [ text (ApiError.toMessage failure) ]
                , button [ type_ "button", onClick messages.recountAsked ] [ text "Count again" ]
                ]

        Counted tally ->
            let
                n =
                    inScope state.scope tally
            in
            p [ class "queue-admin-match", attribute "aria-live" "polite" ]
                [ text (Format.count n ++ Format.forCount n { one = " job matches.", many = " jobs match." }) ]

        Counting _ ->
            p [ class "queue-admin-match notice", attribute "aria-live" "polite" ] [ text "Counting the jobs that match…" ]



-- REPROCESSING VIEW


{-| Every option says what it does, in the terms of bitmagnet's own source: the batch handler
in `processor/batch/queue`, the processor's `ClassifyMode`, the classifier workflow's flags and
delete steps in `classifier.core.yml`, and the queue server's order. The Angular dialog's
labels name the flags rather than what they do.
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
        , p [ class "queue-admin-intro", id "reprocess-intro" ]
            [ text ("Queue a batch that classifies torrents again. " ++ deletionSaid ++ " On a large index the batch runs for a long time, and asking external APIs costs requests to them.") ]
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
            , case form.torrents of
                OfContentTypes ->
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
                            Facet.contentFilters
                        )

                EveryTorrent ->
                    text ""

                Orphans ->
                    text ""
            , problemSaid problems NoContentTypeChosen "Choose at least one content type, or another choice of torrents."
            ]
        , option
            { id = "reprocess-rematch"
            , on = form.options.rematch
            , onCheck = change << RematchSet
            , label = "Classify from scratch"
            , hint = "Ignore each torrent's existing match instead of using it as a hint. A torrent this run cannot match again loses its match, and one it now gives a content type this instance deletes is deleted."
            }
        , option
            { id = "reprocess-local-search"
            , on = form.options.localSearch
            , onCheck = change << LocalSearchSet
            , label = "Search bitmagnet's own content table"
            , hint = "For a torrent with no match, search the content bitmagnet already holds by the title parsed from its name, where bitmagnet is set up for it."
            }
        , option
            { id = "reprocess-apis"
            , on = form.options.apis
            , onCheck = change << ApisSet
            , label = "Ask external APIs such as TMDB"
            , hint = "Look matches up through TMDB, by ID and by title, where bitmagnet is set up for it. bitmagnet runs the lowest priority first: with this off, the batch's processing jobs run at priority 4, behind newly crawled torrents (0) and ahead of batches that ask APIs (10) and imports (20); with it on, at 10."
            }
        , sizeField
            { id = "reprocess-batch-size"
            , typed = form.sizes.batchSize
            , onInput = change << BatchSizeTyped
            , label = "Torrents per processing job"
            , hint = "How many torrents each process_torrent job classifies. bitmagnet's default is 100."
            , problem = problemSaid problems BadBatchSize "A whole number of torrents, at least 1."
            }
        , sizeField
            { id = "reprocess-chunk-size"
            , typed = form.sizes.chunkSize
            , onInput = change << ChunkSizeTyped
            , label = "Torrents per batch job"
            , hint = "How many torrents one process_torrent_batch job queues jobs for before it hands the rest on to a new batch job, rounded up to a whole number of processing jobs. bitmagnet's default is 10,000."
            , problem = problemSaid problems BadChunkSize "A whole number of torrents, at least 1."
            }
        , option
            { id = "reprocess-purge"
            , on = form.options.purge
            , onCheck = change << PurgeSet
            , label = "Delete every job in the queue first"
            , hint = "Empties bitmagnet's whole queue, every queue and status, in the same transaction that queues the batch."
            }
        , case state.step of
            Asking (Reprocessing chosen held) ->
                confirmation messages (reprocessQuestion chosen held)

            Sending _ (Reprocessing _ _) ->
                p [ class "notice", attribute "role" "status" ] [ text "Queueing the batch…" ]

            Asking (Purging _ _) ->
                reprocessButton messages state

            Sending _ (Purging _ _) ->
                reprocessButton messages state

            Choosing ->
                reprocessButton messages state
        , case Maybe.map reported state.report of
            Just (Done (Reprocessing chosen _)) ->
                p [ class "queue-admin-outcome", attribute "role" "status" ]
                    [ text
                        (if chosen.options.purge then
                            "bitmagnet emptied the queue and queued the batch. "

                         else
                            "bitmagnet queued the batch. "
                        )
                    , a [ href (Route.toHref mount (Route.QueueJobs Route.emptyJobs)) ] [ text "Watch it in the queue's jobs" ]
                    , text ", newest first."
                    ]

            Just (NotDone (Reprocessing _ _) failure) ->
                p [ class "notice error", attribute "role" "alert" ]
                    [ text "bitmagnet did not queue the batch: ", text (ApiError.toMessage failure) ]

            Just (Unknown (Reprocessing _ _) failure) ->
                p [ class "notice error", attribute "role" "alert" ]
                    [ text ("No usable answer to the batch came back. " ++ ApiError.toMessage failure ++ " Whether bitmagnet queued it is not known. ")
                    , a [ href (Route.toHref mount (Route.QueueJobs Route.emptyJobs)) ] [ text "Check the queue's jobs, newest first," ]
                    , text " before queueing another."
                    ]

            Just (Done (Purging _ _)) ->
                text ""

            Just (NotDone (Purging _ _) _) ->
                text ""

            Just (Unknown (Purging _ _) _) ->
                text ""

            Nothing ->
                text ""
        ]


reprocessButton : Messages msg -> State -> Html msg
reprocessButton messages state =
    button
        [ type_ "button"
        , class "danger"
        , disabled (reprocessAskable state == Nothing)
        , onClick messages.reprocessAsked
        ]
        [ text "Reprocess…" ]


{-| What reprocessing can do to a torrent without being asked to: bitmagnet's classifier
workflow deletes a torrent whose name or files hold a banned keyword, and one whose content
type the instance is set to delete (`delete_content_types`, `delete_xxx`), and the processor
blocks it so the crawler does not add it again (`classifier.core.yml`, `processor/persist.go`).
-}
deletionSaid : String
deletionSaid =
    "Classifying a torrent again can delete it: the classifier deletes, and blocks from being crawled again, a torrent whose name or files hold a banned keyword, or whose content type this instance is set to delete."


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
batch was queued, in info-hash order, `batchSize` at a time, and queues a successor at the
first whole processing job past `chunkSize` (`handOn`).
-}
reprocessQuestion : Batch -> Maybe Int -> Question
reprocessQuestion chosen held =
    let
        options =
            chosen.options

        perJob =
            Format.forCount chosen.sizes.batchSize
                { one = "for each of them"
                , many = "for every " ++ Format.count chosen.sizes.batchSize ++ " of them"
                }

        handedOn =
            handOn chosen.sizes
    in
    { question =
        if options.purge then
            "Delete every job in bitmagnet's queue, then queue a batch that reprocesses " ++ torrentsSaid chosen.torrents ++ "?"

        else
            "Queue a batch that reprocesses " ++ torrentsSaid chosen.torrents ++ "?"
    , facts =
        [ case ( options.purge, held ) of
            ( True, Just n ) ->
                Format.count n
                    ++ Format.forCount n { one = " job was", many = " jobs were" }
                    ++ " in the queue when last counted, in every queue and status. Jobs still waiting to run will never run, and the deletion cannot be undone."

            ( True, Nothing ) ->
                "Every job in the queue is deleted first, in every queue and status. Jobs still waiting to run will never run, and the deletion cannot be undone."

            ( False, Just n ) ->
                "It adds one job to the queue, which held " ++ Format.count n ++ " when last counted."

            ( False, Nothing ) ->
                "It adds one job to the queue."
        , "bitmagnet queues one process_torrent_batch job, which walks the torrents last updated before it was queued and queues a process_torrent job "
            ++ perJob
            ++ "; after "
            ++ Format.count handedOn
            ++ Format.forCount handedOn { one = " torrent", many = " torrents" }
            ++ " it hands the rest on to a new batch job."
        , if options.rematch then
            "Each torrent is classified from scratch: its existing match is ignored, and a torrent this run cannot match again loses its match."

          else
            "Each torrent is classified again, with its existing match as a hint."
        , "A torrent in which the classifier now finds a banned keyword, or to which it now gives a content type this instance is set to delete, is deleted from the index and blocked from being crawled again."
        , if options.localSearch then
            "A torrent with no match is searched for by title in bitmagnet's own content table, where bitmagnet is set up for it."

          else
            "bitmagnet's own content table is not searched by title."
        , if options.apis then
            "External APIs such as TMDB are asked for matches, where bitmagnet is set up for them."

          else
            "No external API such as TMDB is asked."
        , "On a large index this is a long and expensive run."
        ]
    , confirm = "Queue the batch"
    , cancel = "Don't queue it"
    }


torrentsSaid : BatchTorrents -> String
torrentsSaid torrents =
    case torrents of
        AllTorrents ->
            "every torrent in the index"

        OrphanedTorrents ->
            "every torrent with no content record, which search cannot find"

        TorrentsOf first rest ->
            "every torrent whose content record is " ++ Format.series "or" (List.map Facet.contentLabel (first :: rest))
