module QueueAdminTest exposing (suite)

import ApiError
import Expect
import Facet
import Graphql.Document
import Graphql.OptionalArgument exposing (OptionalArgument(..))
import Html.Attributes
import Json.Decode as Decode
import Magnes.Api.Enum.ContentType exposing (ContentType(..))
import Magnes.Api.Enum.QueueJobStatus exposing (QueueJobStatus(..))
import Magnes.Api.InputObject as InputObject
import QueueAdmin
import Route
import Test exposing (Test, describe, test)
import Test.Html.Event as Event
import Test.Html.Query as Query
import Test.Html.Selector as Selector


suite : Test
suite =
    describe "QueueAdmin"
        [ describe "on the wire"
            [ test "counts the chosen scope through the facets, and everything, asking for no job" <|
                \_ ->
                    Graphql.Document.serializeQuery (QueueAdmin.countQuery failedInProcessTorrent)
                        |> Expect.all
                            [ String.contains "jobs(input: {limit: 0, facets: {status: {aggregate: true, filter: [failed]}, queue: {aggregate: true, filter: [\"process_torrent\"]}}})" >> Expect.equal True
                            , String.contains "jobs(input: {limit: 0, totalCount: true})" >> Expect.equal True
                            ]
            , test "reads bitmagnet's count" <|
                \_ ->
                    -- The aliases are elm-graphql's own, derived from each field's arguments.
                    Decode.decodeString (Graphql.Document.decoder (QueueAdmin.countQuery QueueAdmin.emptyScope)) """{"data":{"queue":{"jobs2343941934":{"aggregations":{"queue":[{"value3832528868":"process_torrent","count":8},{"value3832528868":"process_torrent_batch","count":8}],"status":[{"value1508768773":"failed","count":4},{"value1508768773":"pending","count":4},{"value1508768773":"processed","count":4},{"value1508768773":"retry","count":4}]}},"jobs164972494":{"totalCount":16}}}}"""
                        |> Expect.equal
                            (Ok
                                { queues = [ ( "process_torrent", 8 ), ( "process_torrent_batch", 8 ) ]
                                , statuses = [ ( Failed, 4 ), ( Pending, 4 ), ( Processed, 4 ), ( Retry, 4 ) ]
                                , everything = 16
                                }
                            )
            , test "a purge of everything is an empty input, which bitmagnet reads as every job" <|
                \_ ->
                    Graphql.Document.serializeMutation (QueueAdmin.purgeMutation QueueAdmin.emptyScope)
                        |> String.contains "queue {\n    purgeJobs(input: {})\n  }"
                        |> Expect.equal True
            , test "a purge names its queues and statuses" <|
                \_ ->
                    Graphql.Document.serializeMutation (QueueAdmin.purgeMutation failedInProcessTorrent)
                        |> String.contains "purgeJobs(input: {queues: [\"process_torrent\"], statuses: [failed]})"
                        |> Expect.equal True
            , test "a batch sends every flag the form shows, and no workflow" <|
                \_ ->
                    QueueAdmin.batch { form | torrents = QueueAdmin.OfContentTypes, contentTypes = [ Facet.Known Movie, Facet.Unclassified ] }
                        |> Result.map (QueueAdmin.reprocessMutation >> Graphql.Document.serializeMutation)
                        |> Result.map (String.contains "enqueueReprocessTorrentsBatch(input: {purge: false, batchSize: 100, chunkSize: 10000, contentTypes: [movie, null], orphans: false, classifierRematch: false, apisDisabled: true, localSearchDisabled: true})")
                        |> Expect.equal (Ok True)
            ]
        , describe "the purge sent"
            [ test "names no queue and no status when none is chosen, which bitmagnet reads as every job" <|
                \_ ->
                    QueueAdmin.purgeInput QueueAdmin.emptyScope
                        |> Expect.equal { queues = Absent, statuses = Absent }
            , test "names the queues and statuses chosen" <|
                \_ ->
                    QueueAdmin.purgeInput { queues = [ "process_torrent" ], statuses = [ Failed, Retry ] }
                        |> Expect.equal { queues = Present [ "process_torrent" ], statuses = Present [ Failed, Retry ] }
            ]
        , describe "counting"
            [ test "choosing what to purge asks bitmagnet to count it" <|
                \_ ->
                    choose failedInProcessTorrent opened
                        |> Tuple.second
                        |> Expect.equal (QueueAdmin.CountJobs failedInProcessTorrent)
            , test "says nothing matches until bitmagnet has counted the new choice" <|
                \_ ->
                    -- The chips stay, drawn from the last count, but the old number is not the
                    -- answer for the new choice.
                    opened
                        |> choose failedInProcessTorrent
                        |> Tuple.first
                        |> rendered
                        |> Expect.all
                            [ Query.hasNot [ Selector.text "16 jobs match" ]
                            , Query.has [ Selector.text "Counting the jobs that match…" ]
                            ]
            , test "the jobs matching are those in every chosen status, within the chosen queues" <|
                \_ ->
                    -- The status counts are bitmagnet's, counted within the chosen queues and
                    -- leaving out the status filter itself, so the chosen ones add up to the purge.
                    opened
                        |> choose { queues = [ "process_torrent" ], statuses = [ Failed, Retry ] }
                        |> Tuple.first
                        |> QueueAdmin.counted { queues = [ "process_torrent" ], statuses = [ Failed, Retry ] }
                            (Ok { tally | statuses = [ ( Pending, 2 ), ( Retry, 2 ), ( Failed, 3 ), ( Processed, 2 ) ] })
                        |> rendered
                        |> Query.has [ Selector.text "5 jobs match." ]
            , test "with nothing chosen, every job matches" <|
                \_ ->
                    rendered opened
                        |> Query.has [ Selector.text "16 jobs match." ]
            , test "an answer about a choice since changed is not taken for the new one" <|
                \_ ->
                    opened
                        |> choose failedInProcessTorrent
                        |> Tuple.first
                        |> QueueAdmin.counted QueueAdmin.emptyScope (Ok tally)
                        |> rendered
                        |> Query.hasNot [ Selector.text "16 jobs match." ]
            , test "a count that failed says why" <|
                \_ ->
                    QueueAdmin.empty
                        |> QueueAdmin.counted QueueAdmin.emptyScope (Err ApiError.Unreachable)
                        |> rendered
                        |> Query.has [ Selector.text (ApiError.toMessage ApiError.Unreachable) ]
            ]
        , describe "purging"
            [ test "nothing is sent without being asked about first" <|
                \_ ->
                    QueueAdmin.confirm opened
                        |> Tuple.second
                        |> Expect.equal Nothing
            , test "once asked about, confirming sends the purge of the scope asked about" <|
                \_ ->
                    scoped failedInProcessTorrent
                        |> QueueAdmin.askPurge
                        |> QueueAdmin.confirm
                        |> Tuple.second
                        |> Expect.equal (Just (QueueAdmin.SendPurge failedInProcessTorrent))
            , test "the question names the queues and statuses, and how many jobs matched" <|
                \_ ->
                    scoped failedInProcessTorrent
                        |> QueueAdmin.askPurge
                        |> rendered
                        |> Expect.all
                            [ Query.has [ Selector.text "Purge every job in process_torrent with status failed?" ]
                            , Query.has [ Selector.text "2 jobs matched when last counted." ]
                            , Query.has [ Selector.text "bitmagnet deletes whatever matches when it purges, and it cannot be undone." ]
                            , Query.hasNot [ Selector.text "will never run" ]
                            ]
            , test "a purge of everything says so, and that jobs waiting to run never will" <|
                \_ ->
                    opened
                        |> QueueAdmin.askPurge
                        |> rendered
                        |> Expect.all
                            [ Query.has [ Selector.text "Purge every job in bitmagnet's queue, in every queue and status?" ]
                            , Query.has [ Selector.text "16 jobs matched when last counted." ]
                            , Query.has [ Selector.text "Jobs still waiting to run will never run." ]
                            ]
            , test "several queues and statuses are listed in full" <|
                \_ ->
                    scoped { queues = [ "process_torrent", "process_torrent_batch" ], statuses = [ Pending, Retry ] }
                        |> QueueAdmin.askPurge
                        |> rendered
                        |> Expect.all
                            [ Query.has [ Selector.text "Purge every job in process_torrent and process_torrent_batch with status pending or retry?" ]
                            , Query.has [ Selector.text "Jobs still waiting to run will never run." ]
                            ]
            , test "a queue with no status chosen is every status in it" <|
                \_ ->
                    scoped { queues = [ "process_torrent" ], statuses = [] }
                        |> QueueAdmin.askPurge
                        |> rendered
                        |> Query.has [ Selector.text "Purge every job in process_torrent, whatever its status?" ]
            , test "a status with no queue chosen is that status in every queue" <|
                \_ ->
                    scoped { queues = [], statuses = [ Processed ] }
                        |> QueueAdmin.askPurge
                        |> rendered
                        |> Expect.all
                            [ Query.has [ Selector.text "Purge every job in every queue with status processed?" ]
                            , Query.hasNot [ Selector.text "will never run" ]
                            ]
            , test "is not asked about while the count is not of the scope chosen" <|
                \_ ->
                    opened
                        |> choose failedInProcessTorrent
                        |> Tuple.first
                        |> QueueAdmin.askPurge
                        |> QueueAdmin.confirm
                        |> Tuple.second
                        |> Expect.equal Nothing
            , test "is not asked about while nothing matches" <|
                \_ ->
                    opened
                        |> QueueAdmin.counted QueueAdmin.emptyScope (Ok { tally | statuses = [], everything = 0 })
                        |> QueueAdmin.askPurge
                        |> QueueAdmin.confirm
                        |> Tuple.second
                        |> Expect.equal Nothing
            , test "choosing another scope takes the question back" <|
                \_ ->
                    opened
                        |> QueueAdmin.askPurge
                        |> choose failedInProcessTorrent
                        |> Tuple.first
                        |> QueueAdmin.confirm
                        |> Tuple.second
                        |> Expect.equal Nothing
            , test "keeping them takes the question back" <|
                \_ ->
                    opened
                        |> QueueAdmin.askPurge
                        |> QueueAdmin.keep
                        |> Expect.all
                            [ QueueAdmin.confirm >> Tuple.second >> Expect.equal Nothing
                            , rendered >> Query.hasNot [ Selector.text "matched when last counted" ]
                            ]
            , test "the question offers to purge or to keep them" <|
                \_ ->
                    opened
                        |> QueueAdmin.askPurge
                        |> rendered
                        |> Query.find [ Selector.class "queue-admin-confirm" ]
                        |> Expect.all
                            [ Query.has [ Selector.tag "button", Selector.text "Purge" ]
                            , Query.has [ Selector.tag "button", Selector.text "Keep them" ]
                            ]
            ]
        , describe "reprocessing"
            [ test "nothing is queued without being asked about first" <|
                \_ ->
                    opened
                        |> edits [ QueueAdmin.RematchSet True ]
                        |> QueueAdmin.confirm
                        |> Tuple.second
                        |> Expect.equal Nothing
            , test "once asked about, confirming queues the batch the form makes" <|
                \_ ->
                    opened
                        |> edits [ QueueAdmin.RematchSet True, QueueAdmin.BatchSizeTyped "50" ]
                        |> QueueAdmin.askReprocess
                        |> QueueAdmin.confirm
                        |> Tuple.second
                        |> Expect.equal
                            (Just
                                (QueueAdmin.SendReprocess
                                    { torrents = QueueAdmin.AllTorrents
                                    , rematch = True
                                    , localSearch = False
                                    , apis = False
                                    , batchSize = 50
                                    , chunkSize = 10000
                                    , purge = False
                                    }
                                )
                            )
            , test "a form that cannot be sent is not asked about, and says why" <|
                \_ ->
                    opened
                        |> edits [ QueueAdmin.BatchSizeTyped "0", QueueAdmin.TorrentsChosen QueueAdmin.OfContentTypes ]
                        |> QueueAdmin.askReprocess
                        |> Expect.all
                            [ QueueAdmin.confirm >> Tuple.second >> Expect.equal Nothing
                            , rendered >> Query.has [ Selector.text "A whole number of torrents, at least 1." ]
                            , rendered >> Query.has [ Selector.text "Choose at least one content type, or another choice of torrents." ]
                            ]
            , test "the question says which torrents, how the batch walks them, and what it does to each" <|
                \_ ->
                    opened
                        |> QueueAdmin.askReprocess
                        |> rendered
                        |> Query.find [ Selector.class "queue-admin-confirm" ]
                        |> Expect.all
                            [ Query.has [ Selector.text "Queue a batch that reprocesses every torrent in the index?" ]
                            , Query.has [ Selector.text "bitmagnet queues one process_torrent_batch job, which walks the torrents last updated before it was queued and queues a process_torrent job for every 100 of them; after 10,000 torrents it hands the rest on to a new batch job." ]
                            , Query.has [ Selector.text "Each torrent is classified again, with its existing match as a hint." ]
                            , Query.has [ Selector.text "bitmagnet's own content table is not searched by title." ]
                            , Query.has [ Selector.text "No external API such as TMDB is asked." ]
                            , Query.has [ Selector.text "On a large index this is a long and expensive run." ]
                            , Query.hasNot [ Selector.text "Delete" ]
                            , Query.has [ Selector.tag "button", Selector.text "Queue the batch" ]
                            , Query.has [ Selector.tag "button", Selector.text "Don't queue it" ]
                            ]
            , test "classifying from scratch says that a match not found again is lost" <|
                \_ ->
                    opened
                        |> edits [ QueueAdmin.RematchSet True ]
                        |> QueueAdmin.askReprocess
                        |> rendered
                        |> Query.has [ Selector.text "Each torrent is classified from scratch: its existing match is ignored, and a torrent this run cannot match again loses its match." ]
            , test "looking matches up says where" <|
                \_ ->
                    opened
                        |> edits [ QueueAdmin.LocalSearchSet True, QueueAdmin.ApisSet True ]
                        |> QueueAdmin.askReprocess
                        |> rendered
                        |> Expect.all
                            [ Query.has [ Selector.text "A torrent with no match is searched for by title in bitmagnet's own content table." ]
                            , Query.has [ Selector.text "External APIs such as TMDB are asked for matches, where bitmagnet is set up for them." ]
                            ]
            , test "only orphans says what an orphan is" <|
                \_ ->
                    opened
                        |> edits [ QueueAdmin.TorrentsChosen QueueAdmin.Orphans ]
                        |> QueueAdmin.askReprocess
                        |> rendered
                        |> Query.has [ Selector.text "Queue a batch that reprocesses every torrent with no content record, which search cannot find?" ]
            , test "only some content types names them" <|
                \_ ->
                    opened
                        |> edits [ QueueAdmin.TorrentsChosen QueueAdmin.OfContentTypes, QueueAdmin.ContentTypeToggled (Facet.Known Tv_show), QueueAdmin.ContentTypeToggled Facet.Unclassified ]
                        |> QueueAdmin.askReprocess
                        |> rendered
                        |> Query.has [ Selector.text "Queue a batch that reprocesses every torrent whose content record is tv show or unclassified?" ]
            , test "purging first says so first, with every job the queue held" <|
                \_ ->
                    opened
                        |> edits [ QueueAdmin.PurgeSet True ]
                        |> QueueAdmin.askReprocess
                        |> rendered
                        |> Expect.all
                            [ Query.has [ Selector.text "Delete every job in bitmagnet's queue, then queue a batch that reprocesses every torrent in the index?" ]
                            , Query.has [ Selector.text "16 jobs were in the queue when last counted, in every queue and status. Jobs still waiting to run will never run, and the deletion cannot be undone." ]
                            ]
            , test "purging first is not asked about until the queue has been counted" <|
                \_ ->
                    opened
                        |> choose failedInProcessTorrent
                        |> Tuple.first
                        |> edits [ QueueAdmin.PurgeSet True ]
                        |> QueueAdmin.askReprocess
                        |> QueueAdmin.confirm
                        |> Tuple.second
                        |> Expect.equal Nothing
            , test "changing the form takes the question back" <|
                \_ ->
                    opened
                        |> QueueAdmin.askReprocess
                        |> edits [ QueueAdmin.ApisSet True ]
                        |> QueueAdmin.confirm
                        |> Tuple.second
                        |> Expect.equal Nothing
            , test "one question at a time: asking about a purge takes back the batch" <|
                \_ ->
                    opened
                        |> QueueAdmin.askReprocess
                        |> QueueAdmin.askPurge
                        |> QueueAdmin.confirm
                        |> Tuple.second
                        |> Expect.equal (Just (QueueAdmin.SendPurge QueueAdmin.emptyScope))
            ]
        , describe "after bitmagnet answers"
            [ test "a purge's answer asks for the counts again, and the old count is not shown as current" <|
                \_ ->
                    scoped failedInProcessTorrent
                        |> QueueAdmin.askPurge
                        |> QueueAdmin.confirm
                        |> Tuple.first
                        |> QueueAdmin.answered (Ok ())
                        |> Expect.all
                            [ Tuple.second >> Expect.equal (Just (QueueAdmin.CountJobs failedInProcessTorrent))
                            , Tuple.first >> rendered >> Query.hasNot [ Selector.text "2 jobs match." ]
                            , Tuple.first >> rendered >> Query.has [ Selector.text "Counting the jobs that match…" ]
                            ]
            , test "a purge bitmagnet accepted is said, in the words it was asked in" <|
                \_ ->
                    scoped failedInProcessTorrent
                        |> QueueAdmin.askPurge
                        |> QueueAdmin.confirm
                        |> Tuple.first
                        |> QueueAdmin.answered (Ok ())
                        |> Tuple.first
                        |> rendered
                        |> Query.find [ Selector.attribute (Html.Attributes.attribute "role" "status") ]
                        |> Query.has [ Selector.text "bitmagnet purged every job in process_torrent with status failed." ]
            , test "the jobs a purge is about can be looked at in the jobs table, before and after" <|
                \_ ->
                    scoped failedInProcessTorrent
                        |> rendered
                        |> Query.find [ Selector.tag "a", Selector.containing [ Selector.text "See them in the jobs table" ] ]
                        |> Query.has [ Selector.attribute (Html.Attributes.href "/queue/jobs?queue=process_torrent&status=failed") ]
            , test "a refused act is announced, and the counts asked for again" <|
                \_ ->
                    -- The act may have happened before the failure was reported, so the page
                    -- shows bitmagnet's count rather than the one from before it.
                    scoped failedInProcessTorrent
                        |> QueueAdmin.askPurge
                        |> QueueAdmin.confirm
                        |> Tuple.first
                        |> QueueAdmin.answered (Err ApiError.ServerFault)
                        |> Expect.all
                            [ Tuple.second >> Expect.equal (Just (QueueAdmin.CountJobs failedInProcessTorrent))
                            , Tuple.first
                                >> rendered
                                >> Query.find [ Selector.attribute (Html.Attributes.attribute "role" "alert") ]
                                >> Query.has [ Selector.text "The purge was not done: ", Selector.text (ApiError.toMessage ApiError.ServerFault) ]
                            ]
            , test "a batch bitmagnet queued is said, with where to watch it" <|
                \_ ->
                    opened
                        |> QueueAdmin.askReprocess
                        |> QueueAdmin.confirm
                        |> Tuple.first
                        |> QueueAdmin.answered (Ok ())
                        |> Expect.all
                            [ Tuple.second >> Expect.equal (Just (QueueAdmin.CountJobs QueueAdmin.emptyScope))
                            , Tuple.first
                                >> rendered
                                >> Query.find [ Selector.attribute (Html.Attributes.attribute "role" "status") ]
                                >> Expect.all
                                    [ Query.has [ Selector.text "bitmagnet queued the batch." ]
                                    , Query.find [ Selector.tag "a" ]
                                        >> Query.has
                                            [ Selector.text "Watch it in the queue's jobs"
                                            , Selector.attribute (Html.Attributes.href "/queue/jobs")
                                            ]
                                    ]
                            ]
            , test "an answer with nothing on its way is not taken for one" <|
                \_ ->
                    QueueAdmin.answered (Ok ()) opened
                        |> Expect.equal ( opened, Nothing )
            , test "while an act is on its way, nothing else is asked about" <|
                \_ ->
                    opened
                        |> QueueAdmin.askPurge
                        |> QueueAdmin.confirm
                        |> Tuple.first
                        |> QueueAdmin.askReprocess
                        |> QueueAdmin.confirm
                        |> Tuple.second
                        |> Expect.equal Nothing
            ]
        , describe "what the controls send"
            -- Each control sends what changed, never a copy of the form or the scope as it was
            -- drawn: Elm draws on the next animation frame, so a second change made before it
            -- would carry the first one's old value and undo it. A credentialed run caught that:
            -- a box ticked and a size typed at once lost the tick.
            [ test "a box sends that it was ticked" <|
                \_ ->
                    control "reprocess-rematch" opened
                        |> Event.simulate (Event.check True)
                        |> Event.expect (editSent (QueueAdmin.RematchSet True))
            , test "a size sends what was typed" <|
                \_ ->
                    control "reprocess-batch-size" opened
                        |> Event.simulate (Event.input "50")
                        |> Event.expect (editSent (QueueAdmin.BatchSizeTyped "50"))
            , test "a choice of torrents sends which" <|
                \_ ->
                    control "reprocess-orphans" opened
                        |> Event.simulate (Event.check True)
                        |> Event.expect (editSent (QueueAdmin.TorrentsChosen QueueAdmin.Orphans))
            , test "a queue's chip sends which queue" <|
                \_ ->
                    QueueAdmin.view (Route.basePath "") sending opened
                        |> Query.fromHtml
                        |> Query.find [ Selector.tag "button", Selector.containing [ Selector.text "process_torrent_batch" ] ]
                        |> Event.simulate Event.click
                        |> Event.expect ( Just "process_torrent_batch", Nothing, Nothing )
            , test "two changes in a row both land" <|
                \_ ->
                    opened
                        |> edits [ QueueAdmin.RematchSet True, QueueAdmin.BatchSizeTyped "50" ]
                        |> QueueAdmin.askReprocess
                        |> QueueAdmin.confirm
                        |> Tuple.second
                        |> Maybe.map
                            (\wanted ->
                                case wanted of
                                    QueueAdmin.SendReprocess chosen ->
                                        ( chosen.rematch, chosen.batchSize )

                                    _ ->
                                        ( False, 0 )
                            )
                        |> Expect.equal (Just ( True, 50 ))
            , test "choosing a queue and then a status keeps both" <|
                \_ ->
                    opened
                        |> QueueAdmin.toggleQueue "process_torrent"
                        |> Tuple.first
                        |> QueueAdmin.toggleStatus Failed
                        |> Tuple.second
                        |> Expect.equal (QueueAdmin.CountJobs failedInProcessTorrent)
            ]
        , describe "the reprocessing batch sent"
            [ test "as the form opens: every torrent, bitmagnet's own sizes, nothing looked up, the queue kept" <|
                \_ ->
                    -- Every flag is sent, so what the confirmation said is what was asked for
                    -- whatever bitmagnet's defaults become. The workflow is never sent: the
                    -- batch enqueue drops it (see `reprocessInput`).
                    sent QueueAdmin.defaultForm
                        |> Expect.equal
                            (Ok
                                { purge = Present False
                                , batchSize = Present 100
                                , chunkSize = Present 10000
                                , contentTypes = Absent
                                , orphans = Present False
                                , classifierRematch = Present False
                                , classifierWorkflow = Absent
                                , apisDisabled = Present True
                                , localSearchDisabled = Present True
                                }
                            )
            , test "only orphans is the orphans flag, with no content types" <|
                \_ ->
                    sent { form | torrents = QueueAdmin.Orphans, contentTypes = [ Facet.Known Movie ] }
                        |> Result.map (\input -> ( input.orphans, input.contentTypes ))
                        |> Expect.equal (Ok ( Present True, Absent ))
            , test "only some content types names them, unclassified as null" <|
                \_ ->
                    sent { form | torrents = QueueAdmin.OfContentTypes, contentTypes = [ Facet.Known Movie, Facet.Unclassified ] }
                        |> Result.map (\input -> ( input.orphans, input.contentTypes ))
                        |> Expect.equal (Ok ( Present False, Present [ Just Movie, Nothing ] ))
            , test "only some content types, with none chosen, is not sent" <|
                \_ ->
                    -- An empty list is every torrent to bitmagnet: a scope that silently widened
                    -- to the whole index is the one mistake this form must not make.
                    sent { form | torrents = QueueAdmin.OfContentTypes, contentTypes = [] }
                        |> Expect.equal (Err [ QueueAdmin.NoContentTypeChosen ])
            , test "classifying from scratch, looking up, and purging first are sent as chosen" <|
                \_ ->
                    sent { form | rematch = True, localSearch = True, apis = True, purge = True }
                        |> Result.map (\input -> [ input.classifierRematch, input.localSearchDisabled, input.apisDisabled, input.purge ])
                        |> Expect.equal (Ok [ Present True, Present False, Present False, Present True ])
            , test "sizes are whole numbers of torrents, spaces aside" <|
                \_ ->
                    sent { form | batchSize = " 250 ", chunkSize = "5000" }
                        |> Result.map (\input -> ( input.batchSize, input.chunkSize ))
                        |> Expect.equal (Ok ( Present 250, Present 5000 ))
            , test "a size that is not a whole number above nought is not sent" <|
                \_ ->
                    -- bitmagnet reads nought as its own default, which is not what was typed.
                    List.map (\typed -> sent { form | batchSize = typed, chunkSize = typed })
                        [ "0", "-5", "1.5", "lots", "" ]
                        |> Expect.equal (List.repeat 5 (Err [ QueueAdmin.BadBatchSize, QueueAdmin.BadChunkSize ]))
            , test "a size past what a GraphQL Int holds is not sent" <|
                \_ ->
                    ( sent { form | batchSize = "2147483647" } |> Result.map .batchSize
                    , sent { form | batchSize = "2147483648" } |> Result.map .batchSize
                    )
                        |> Expect.equal ( Ok (Present 2147483647), Err [ QueueAdmin.BadBatchSize ] )
            ]
        ]


form : QueueAdmin.Form
form =
    QueueAdmin.defaultForm


sent : QueueAdmin.Form -> Result (List QueueAdmin.Problem) InputObject.QueueEnqueueReprocessTorrentsBatchInput
sent =
    QueueAdmin.batch >> Result.map QueueAdmin.reprocessInput


tally : QueueAdmin.Tally
tally =
    { queues = [ ( "process_torrent", 8 ), ( "process_torrent_batch", 8 ) ]
    , statuses = [ ( Pending, 4 ), ( Retry, 4 ), ( Failed, 4 ), ( Processed, 4 ) ]
    , everything = 16
    }


failedInProcessTorrent : QueueAdmin.Scope
failedInProcessTorrent =
    { queues = [ "process_torrent" ], statuses = [ Failed ] }


{-| The page as it opens on the fixture's queue: nothing chosen, and every job counted.
-}
opened : QueueAdmin.State
opened =
    QueueAdmin.counted QueueAdmin.emptyScope (Ok tally) QueueAdmin.empty


messages : QueueAdmin.Messages ()
messages =
    { queueToggled = always ()
    , statusToggled = always ()
    , edited = always ()
    , purgeAsked = ()
    , reprocessAsked = ()
    , kept = ()
    , confirmed = ()
    }


rendered : QueueAdmin.State -> Query.Single ()
rendered state =
    QueueAdmin.view (Route.basePath "") messages state
        |> Query.fromHtml


{-| The page with `scope` chosen and counted, two jobs in each of its statuses.
-}
scoped : QueueAdmin.Scope -> QueueAdmin.State
scoped scope =
    opened
        |> choose scope
        |> Tuple.first
        |> QueueAdmin.counted scope (Ok { tally | statuses = [ ( Pending, 2 ), ( Retry, 2 ), ( Failed, 2 ), ( Processed, 2 ) ] })


{-| `scope` chosen chip by chip, from nothing chosen, as a person would: the state, and what
the last choice asked bitmagnet for.
-}
choose : QueueAdmin.Scope -> QueueAdmin.State -> ( QueueAdmin.State, QueueAdmin.Request )
choose scope state =
    let
        ( queued, asked ) =
            List.foldl (\queue ( current, _ ) -> QueueAdmin.toggleQueue queue current) ( state, QueueAdmin.CountJobs state.scope ) scope.queues
    in
    List.foldl (\status ( current, _ ) -> QueueAdmin.toggleStatus status current) ( queued, asked ) scope.statuses


edits : List QueueAdmin.Edit -> QueueAdmin.State -> QueueAdmin.State
edits changes state =
    List.foldl QueueAdmin.edit state changes


{-| What a control sends, for the tests that ask which: a queue toggled, a status toggled, or
an edit to the form, and nothing for every other message.
-}
type alias Sent =
    ( Maybe String, Maybe QueueJobStatus, Maybe QueueAdmin.Edit )


sending : QueueAdmin.Messages Sent
sending =
    { queueToggled = \queue -> ( Just queue, Nothing, Nothing )
    , statusToggled = \status -> ( Nothing, Just status, Nothing )
    , edited = \change -> ( Nothing, Nothing, Just change )
    , purgeAsked = nothingSent
    , reprocessAsked = nothingSent
    , kept = nothingSent
    , confirmed = nothingSent
    }


nothingSent : Sent
nothingSent =
    ( Nothing, Nothing, Nothing )


editSent : QueueAdmin.Edit -> Sent
editSent change =
    ( Nothing, Nothing, Just change )


control : String -> QueueAdmin.State -> Query.Single Sent
control controlId state =
    QueueAdmin.view (Route.basePath "") sending state
        |> Query.fromHtml
        |> Query.find [ Selector.id controlId ]
