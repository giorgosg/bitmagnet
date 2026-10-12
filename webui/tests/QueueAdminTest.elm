module QueueAdminTest exposing (suite)

import ApiError
import Dict
import Expect
import Facet
import Graphql.Document
import Graphql.Http
import Graphql.Http.GraphqlError as GraphqlError
import Graphql.OptionalArgument exposing (OptionalArgument(..))
import Html.Attributes
import Identity
import Json.Decode as Decode
import Json.Encode as Encode
import Magnes.Api.Enum.ContentType exposing (ContentType(..))
import Magnes.Api.Enum.QueueJobStatus as QueueJobStatus exposing (QueueJobStatus(..))
import Magnes.Api.InputObject as InputObject
import QueueAdmin
import Route
import Test exposing (Test, describe, test)
import Test.Html.Event as Event
import Test.Html.Query as Query
import Test.Html.Selector as Selector
import Time


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
            , test "reads bitmagnet's count, statuses in the order of a job's life" <|
                \_ ->
                    -- The aliases are elm-graphql's own, derived from each field's arguments.
                    Decode.decodeString (Graphql.Document.decoder (QueueAdmin.countQuery QueueAdmin.emptyScope)) """{"data":{"queue":{"jobs2343941934":{"aggregations":{"queue":[{"value3832528868":"process_torrent","count":8},{"value3832528868":"process_torrent_batch","count":8}],"status":[{"value1508768773":"failed","count":4},{"value1508768773":"pending","count":4},{"value1508768773":"processed","count":4},{"value1508768773":"retry","count":4}]}},"jobs164972494":{"totalCount":16}}}}"""
                        |> Expect.equal (Ok tally)
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
            , test "the page opens by counting everything" <|
                \_ ->
                    QueueAdmin.document QueueAdmin.opening
                        |> Expect.equal (countDocument QueueAdmin.emptyScope)
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
                        |> QueueAdmin.document
                        |> Expect.equal (countDocument failedInProcessTorrent)
            , test "says nothing matches until bitmagnet has counted the new choice" <|
                \_ ->
                    opened
                        |> choose failedInProcessTorrent
                        |> Tuple.first
                        |> rendered
                        |> Expect.all
                            [ Query.hasNot [ Selector.text "16 jobs match" ]
                            , Query.has [ Selector.text "Counting the jobs that match…" ]
                            ]
            , test "the chips keep their labels but show no count that is not the current one" <|
                \_ ->
                    opened
                        |> choose failedInProcessTorrent
                        |> Tuple.first
                        |> rendered
                        |> Expect.all
                            [ Query.has [ Selector.tag "button", Selector.containing [ Selector.text "process_torrent_batch" ] ]
                            , Query.findAll [ Selector.class "chip-count" ] >> Query.count (Expect.equal 0)
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
            , test "every status the schema has is offered for a purge, and counted" <|
                \_ ->
                    QueueAdmin.empty
                        |> QueueAdmin.counted QueueAdmin.emptyScope
                            (Ok { tally | statuses = List.map (\status -> ( status, 1 )) QueueJobStatus.list, everything = List.length QueueJobStatus.list })
                        |> rendered
                        |> Expect.all
                            [ Query.has [ Selector.text (String.fromInt (List.length QueueJobStatus.list) ++ " jobs match.") ]
                            , Query.findAll [ Selector.class "chip", Selector.containing [ Selector.class "chip-count" ] ]
                                >> Query.count (Expect.equal (List.length QueueJobStatus.list + 2))
                            ]
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
            , test "a count that failed says why, and shows no old count as current" <|
                \_ ->
                    opened
                        |> choose failedInProcessTorrent
                        |> Tuple.first
                        |> QueueAdmin.counted failedInProcessTorrent (Err ApiError.Unreachable)
                        |> rendered
                        |> Expect.all
                            [ Query.has [ Selector.text (ApiError.toMessage ApiError.Unreachable) ]
                            , Query.findAll [ Selector.class "chip-count" ] >> Query.count (Expect.equal 0)
                            ]
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
                        |> Maybe.map QueueAdmin.document
                        |> Expect.equal (Just (purgeDocument failedInProcessTorrent))
            , test "the question names the queues and statuses, and how many jobs matched" <|
                \_ ->
                    scoped failedInProcessTorrent
                        |> QueueAdmin.askPurge
                        |> rendered
                        |> Expect.all
                            [ Query.find [ Selector.class "queue-admin-question" ] >> Query.has [ Selector.text "Purge every job in process_torrent with status failed?" ]
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
                        |> Expect.all
                            [ QueueAdmin.askPurge >> QueueAdmin.confirm >> Tuple.second >> Expect.equal Nothing
                            , rendered >> Query.find [ Selector.tag "button", Selector.containing [ Selector.text "Purge…" ] ] >> Query.has [ Selector.disabled True ]
                            ]
            , test "is not asked about while nothing matches" <|
                \_ ->
                    opened
                        |> QueueAdmin.counted QueueAdmin.emptyScope (Ok { tally | statuses = [], everything = 0 })
                        |> Expect.all
                            [ QueueAdmin.askPurge >> QueueAdmin.confirm >> Tuple.second >> Expect.equal Nothing
                            , rendered >> Query.find [ Selector.tag "button", Selector.containing [ Selector.text "Purge…" ] ] >> Query.has [ Selector.disabled True ]
                            ]
            , test "choosing another scope takes the question back" <|
                \_ ->
                    opened
                        |> QueueAdmin.askPurge
                        |> choose failedInProcessTorrent
                        |> Tuple.first
                        |> QueueAdmin.confirm
                        |> Tuple.second
                        |> Expect.equal Nothing
            , test "cancelling takes the question back" <|
                \_ ->
                    opened
                        |> QueueAdmin.askPurge
                        |> QueueAdmin.cancel
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
                        |> Maybe.map QueueAdmin.document
                        |> Expect.equal
                            (Just
                                (batchDocument
                                    { torrents = QueueAdmin.AllTorrents
                                    , options = { rematch = True, localSearch = False, apis = False, purge = False }
                                    , sizes = { batchSize = 50, chunkSize = 10000 }
                                    }
                                )
                            )
            , test "a form that cannot be sent is not asked about, and says why" <|
                \_ ->
                    opened
                        |> edits [ QueueAdmin.BatchSizeTyped "0", QueueAdmin.TorrentsChosen QueueAdmin.OfContentTypes ]
                        |> Expect.all
                            [ QueueAdmin.askReprocess >> QueueAdmin.confirm >> Tuple.second >> Expect.equal Nothing
                            , rendered >> Query.has [ Selector.text "A whole number of torrents, at least 1." ]
                            , rendered >> Query.has [ Selector.text "Choose at least one content type, or another choice of torrents." ]
                            , rendered >> Query.find [ Selector.tag "button", Selector.containing [ Selector.text "Reprocess…" ] ] >> Query.has [ Selector.disabled True ]
                            ]
            , test "the question says which torrents, how the batch walks them, and what it does to each" <|
                \_ ->
                    opened
                        |> QueueAdmin.askReprocess
                        |> rendered
                        |> Query.find [ Selector.class "queue-admin-confirm" ]
                        |> Expect.all
                            [ Query.find [ Selector.class "queue-admin-question" ] >> Query.has [ Selector.text "Queue a batch that reprocesses every torrent in the index?" ]
                            , Query.has [ Selector.text "It adds one job to the queue, which held 16 when last counted." ]
                            , Query.has [ Selector.text "bitmagnet queues one process_torrent_batch job, which walks the torrents last updated before it was queued and queues a process_torrent job for every 100 of them; after 10,000 torrents it hands the rest on to a new batch job." ]
                            , Query.has [ Selector.text "Each torrent is classified again, with its existing match as a hint." ]
                            , Query.has [ Selector.text "bitmagnet's own content table is not searched by title." ]
                            , Query.has [ Selector.text "No external API such as TMDB is asked." ]
                            , Query.has [ Selector.text "On a large index this is a long and expensive run." ]
                            , Query.hasNot [ Selector.text "Delete every job" ]
                            , Query.has [ Selector.tag "button", Selector.text "Queue the batch" ]
                            , Query.has [ Selector.tag "button", Selector.text "Don't queue it" ]
                            ]
            , test "the question and the panel say a torrent may be deleted" <|
                \_ ->
                    -- The classifier's delete steps (banned keywords, and the content types an
                    -- instance deletes) run on every reprocess, and a deleted torrent is blocked.
                    opened
                        |> QueueAdmin.askReprocess
                        |> rendered
                        |> Expect.all
                            [ Query.find [ Selector.class "queue-admin-confirm" ]
                                >> Query.has [ Selector.text "A torrent in which the classifier now finds a banned keyword, or to which it now gives a content type this instance is set to delete, is deleted from the index and blocked from being crawled again." ]
                            , Query.find [ Selector.id "reprocess-intro" ]
                                >> Query.has [ Selector.text "Classifying a torrent again can delete it:" ]
                            ]
            , test "a batch's hand-on is rounded up to a whole number of processing jobs" <|
                \_ ->
                    -- bitmagnet's batch handler counts the torrents after each processing job, so
                    -- it hands on at the first whole job at or past the chunk size.
                    List.map
                        (\( perJob, perBatch ) ->
                            opened
                                |> edits [ QueueAdmin.BatchSizeTyped perJob, QueueAdmin.ChunkSizeTyped perBatch ]
                                |> QueueAdmin.askReprocess
                                |> rendered
                                |> Query.find [ Selector.class "queue-admin-confirm" ]
                        )
                        [ ( "10000", "100" ), ( "30", "100" ), ( "100", "10000" ), ( "1", "1" ) ]
                        |> List.map2 (\said found -> Query.has [ Selector.text said ] found)
                            [ "for every 10,000 of them; after 10,000 torrents it hands"
                            , "for every 30 of them; after 120 torrents it hands"
                            , "for every 100 of them; after 10,000 torrents it hands"
                            , "for each of them; after 1 torrent it hands"
                            ]
                        |> List.map always
                        |> (\checks -> Expect.all checks ())
            , test "classifying from scratch says that a match not found again is lost" <|
                \_ ->
                    opened
                        |> edits [ QueueAdmin.RematchSet True ]
                        |> QueueAdmin.askReprocess
                        |> rendered
                        |> Query.has [ Selector.text "Each torrent is classified from scratch: its existing match is ignored, and a torrent this run cannot match again loses its match." ]
            , test "looking matches up says where, and only where bitmagnet is set up for it" <|
                \_ ->
                    opened
                        |> edits [ QueueAdmin.LocalSearchSet True, QueueAdmin.ApisSet True ]
                        |> QueueAdmin.askReprocess
                        |> rendered
                        |> Expect.all
                            [ Query.has [ Selector.text "A torrent with no match is searched for by title in bitmagnet's own content table, where bitmagnet is set up for it." ]
                            , Query.has [ Selector.text "External APIs such as TMDB are asked for matches, where bitmagnet is set up for them." ]
                            ]
            , test "the hints say where each lookup depends on how bitmagnet is set up, and where the jobs queue" <|
                \_ ->
                    rendered opened
                        |> Expect.all
                            [ Query.find [ Selector.id "reprocess-local-search-hint" ]
                                >> Query.has [ Selector.text "where bitmagnet is set up for it." ]
                            , Query.find [ Selector.id "reprocess-apis-hint" ]
                                >> Query.has [ Selector.text "bitmagnet runs the lowest priority first: with this off, the batch's processing jobs run at priority 4, behind newly crawled torrents (0) and ahead of batches that ask APIs (10) and imports (20); with it on, at 10." ]
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
                            , Query.hasNot [ Selector.text "It adds one job" ]
                            ]
            , test "a batch that purges first is not asked about until the queue has been counted" <|
                \_ ->
                    opened
                        |> choose failedInProcessTorrent
                        |> Tuple.first
                        |> edits [ QueueAdmin.PurgeSet True ]
                        |> Expect.all
                            [ QueueAdmin.askReprocess >> QueueAdmin.confirm >> Tuple.second >> Expect.equal Nothing
                            , rendered >> Query.find [ Selector.tag "button", Selector.containing [ Selector.text "Reprocess…" ] ] >> Query.has [ Selector.disabled True ]
                            ]
            , test "a batch that does not purge first may be asked about while the queue is being counted, or after a count failed" <|
                \_ ->
                    -- It needs no count: the first build allowed it, and a count that cannot be
                    -- had must not stop it.
                    [ opened |> choose failedInProcessTorrent |> Tuple.first
                    , QueueAdmin.empty |> QueueAdmin.counted QueueAdmin.emptyScope (Err (ApiError.BadStatus 504))
                    ]
                        |> List.map (QueueAdmin.askReprocess >> QueueAdmin.confirm >> Tuple.second >> Maybe.map QueueAdmin.document)
                        |> Expect.equal (List.repeat 2 (Just (batchDocument defaultBatch)))
            , test "its question then says it adds one job, without a count it does not have" <|
                \_ ->
                    QueueAdmin.empty
                        |> QueueAdmin.counted QueueAdmin.emptyScope (Err (ApiError.BadStatus 504))
                        |> QueueAdmin.askReprocess
                        |> rendered
                        |> Query.find [ Selector.class "queue-admin-confirm" ]
                        |> Expect.all
                            [ Query.has [ Selector.text "It adds one job to the queue." ]
                            , Query.hasNot [ Selector.text "when last counted" ]
                            ]
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
                        |> Maybe.map QueueAdmin.document
                        |> Expect.equal (Just (purgeDocument QueueAdmin.emptyScope))
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
                        |> Event.toResult
                        |> Result.map editOf
                        |> Expect.equal (Ok (Just (QueueAdmin.RematchSet True)))
            , test "a size sends what was typed" <|
                \_ ->
                    control "reprocess-batch-size" opened
                        |> Event.simulate (Event.input "50")
                        |> Event.toResult
                        |> Result.map editOf
                        |> Expect.equal (Ok (Just (QueueAdmin.BatchSizeTyped "50")))
            , test "a choice of torrents sends which" <|
                \_ ->
                    control "reprocess-orphans" opened
                        |> Event.simulate (Event.check True)
                        |> Event.toResult
                        |> Result.map editOf
                        |> Expect.equal (Ok (Just (QueueAdmin.TorrentsChosen QueueAdmin.Orphans)))
            , test "a queue's chip sends which queue" <|
                \_ ->
                    chipOf "process_torrent_batch" opened
                        |> Event.simulate Event.click
                        |> Event.toResult
                        |> Result.map queueOf
                        |> Expect.equal (Ok (Just "process_torrent_batch"))
            , test "a status's chip sends which status" <|
                \_ ->
                    chipOf "failed" opened
                        |> Event.simulate Event.click
                        |> Event.toResult
                        |> Result.map statusOf
                        |> Expect.equal (Ok (Just Failed))
            , test "two changes in a row both land" <|
                \_ ->
                    opened
                        |> edits [ QueueAdmin.RematchSet True, QueueAdmin.BatchSizeTyped "50" ]
                        |> QueueAdmin.askReprocess
                        |> QueueAdmin.confirm
                        |> Tuple.second
                        |> Maybe.map QueueAdmin.document
                        |> Maybe.map (\mutation -> ( String.contains "classifierRematch: true" mutation, String.contains "batchSize: 50" mutation ))
                        |> Expect.equal (Just ( True, True ))
            , test "choosing a queue and then a status keeps both" <|
                \_ ->
                    opened
                        |> QueueAdmin.toggleQueue "process_torrent"
                        |> Tuple.first
                        |> QueueAdmin.toggleStatus Failed
                        |> Tuple.second
                        |> QueueAdmin.document
                        |> Expect.equal (countDocument failedInProcessTorrent)
            ]
        , describe "after bitmagnet answers"
            [ test "an act's answer asks for the counts again, and the old count is not shown as current" <|
                \_ ->
                    scoped failedInProcessTorrent
                        |> QueueAdmin.askPurge
                        |> QueueAdmin.confirm
                        |> answer onPage (Ok ())
                        |> Expect.all
                            [ Tuple.second >> Maybe.map QueueAdmin.document >> Expect.equal (Just (countDocument failedInProcessTorrent))
                            , Tuple.first >> rendered >> Query.hasNot [ Selector.text "2 jobs match." ]
                            , Tuple.first >> rendered >> Query.has [ Selector.text "Counting the jobs that match…" ]
                            ]
            , test "a purge bitmagnet accepted is said in the words it was asked in, the counts claimed current only once counted" <|
                \_ ->
                    let
                        purged =
                            scoped failedInProcessTorrent
                                |> QueueAdmin.askPurge
                                |> QueueAdmin.confirm
                                |> answer onPage (Ok ())
                                |> Tuple.first
                    in
                    Expect.all
                        [ \_ ->
                            rendered purged
                                |> Query.find [ Selector.class "queue-admin-outcome" ]
                                |> Expect.all
                                    [ Query.has [ Selector.text "bitmagnet purged every job in process_torrent with status failed." ]
                                    , Query.hasNot [ Selector.text "The counts above are what it holds now." ]
                                    ]
                        , \_ ->
                            purged
                                |> QueueAdmin.counted failedInProcessTorrent (Ok { tally | statuses = [ ( Pending, 2 ), ( Retry, 2 ), ( Processed, 2 ) ] })
                                |> rendered
                                |> Query.find [ Selector.class "queue-admin-outcome" ]
                                |> Query.has [ Selector.text "The counts above are what it holds now." ]
                        , \_ ->
                            purged
                                |> QueueAdmin.counted failedInProcessTorrent (Err ApiError.Unreachable)
                                |> rendered
                                |> Query.find [ Selector.class "queue-admin-outcome" ]
                                |> Query.hasNot [ Selector.text "The counts above are what it holds now." ]
                        ]
                        ()
            , test "the jobs a purge is about can be looked at in the jobs table, before and after" <|
                \_ ->
                    let
                        linksToThem state =
                            rendered state
                                |> Query.find [ Selector.tag "a", Selector.containing [ Selector.text "See them in the jobs table" ] ]
                                |> Query.has [ Selector.attribute (Html.Attributes.href "/queue/jobs?queue=process_torrent&status=failed") ]
                    in
                    scoped failedInProcessTorrent
                        |> Expect.all
                            [ linksToThem
                            , QueueAdmin.askPurge >> QueueAdmin.confirm >> answer onPage (Ok ()) >> Tuple.first >> linksToThem
                            ]
            , test "a refusal from bitmagnet says the act was not done, and the counts are asked for again" <|
                \_ ->
                    scoped failedInProcessTorrent
                        |> QueueAdmin.askPurge
                        |> QueueAdmin.confirm
                        |> answer onPage (Err refusedByBitmagnet)
                        |> Expect.all
                            [ Tuple.second >> Maybe.map QueueAdmin.document >> Expect.equal (Just (countDocument failedInProcessTorrent))
                            , Tuple.first
                                >> rendered
                                >> Query.find [ Selector.attribute (Html.Attributes.attribute "role" "alert") ]
                                >> Query.has [ Selector.text "bitmagnet did not purge the jobs: ", Selector.text (ApiError.toMessage (ApiError.Forbidden "browser session request must be same-origin")) ]
                            ]
            , test "a request bitmagnet could not run as written was not done either" <|
                \_ ->
                    scoped failedInProcessTorrent
                        |> QueueAdmin.askPurge
                        |> QueueAdmin.confirm
                        |> answer onPage (Err invalidRequest)
                        |> Tuple.first
                        |> rendered
                        |> Query.find [ Selector.attribute (Html.Attributes.attribute "role" "alert") ]
                        |> Expect.all
                            [ Query.has [ Selector.text "bitmagnet did not purge the jobs: " ]
                            , Query.hasNot [ Selector.text "is not known" ]
                            ]
            , test "a purge with no usable answer is not known to be done or not, said so for every cause, and points to the jobs to check" <|
                \_ ->
                    -- The database may have committed before the answer was lost, so saying
                    -- it was not done would invite doing it twice. "No answer came back" would be
                    -- untrue of a proxy's status or an answer that is not the schema.
                    List.map
                        (\lost ->
                            scoped failedInProcessTorrent
                                |> QueueAdmin.askPurge
                                |> QueueAdmin.confirm
                                |> answer onPage (Err lost)
                                |> Tuple.first
                                |> rendered
                                |> Query.find [ Selector.attribute (Html.Attributes.attribute "role" "alert") ]
                        )
                        unanswered
                        |> List.map
                            (\alert () ->
                                alert
                                    |> Expect.all
                                        [ Query.has [ Selector.text "No usable answer to the purge came back." ]
                                        , Query.hasNot [ Selector.text "No answer" ]
                                        , Query.has [ Selector.text "Whether bitmagnet purged them is not known." ]
                                        , Query.hasNot [ Selector.text "did not purge" ]
                                        , Query.find [ Selector.tag "a" ] >> Query.has [ Selector.attribute (Html.Attributes.href "/queue/jobs?queue=process_torrent&status=failed") ]
                                        ]
                            )
                        |> (\checks -> Expect.all checks ())
            , test "a batch with no usable answer is not known to be queued or not, and points to the jobs to check" <|
                \_ ->
                    opened
                        |> QueueAdmin.askReprocess
                        |> QueueAdmin.confirm
                        |> answer onPage (Err (Graphql.Http.HttpError (Graphql.Http.BadStatus { url = "/graphql", statusCode = 504, statusText = "Gateway Timeout", headers = Dict.empty } "")))
                        |> Tuple.first
                        |> rendered
                        |> Query.find [ Selector.attribute (Html.Attributes.attribute "role" "alert") ]
                        |> Expect.all
                            [ Query.has [ Selector.text "No usable answer to the batch came back." ]
                            , Query.has [ Selector.text (ApiError.toMessage (ApiError.BadStatus 504)) ]
                            , Query.has [ Selector.text "Whether bitmagnet queued it is not known." ]
                            , Query.find [ Selector.tag "a" ] >> Query.has [ Selector.attribute (Html.Attributes.href "/queue/jobs") ]
                            ]
            , test "a batch bitmagnet queued is said, with where to watch it" <|
                \_ ->
                    opened
                        |> QueueAdmin.askReprocess
                        |> QueueAdmin.confirm
                        |> answer onPage (Ok ())
                        |> Tuple.first
                        |> rendered
                        |> Query.find [ Selector.class "queue-admin-outcome" ]
                        |> Expect.all
                            [ Query.has [ Selector.text "bitmagnet queued the batch." ]
                            , Query.find [ Selector.tag "a" ]
                                >> Query.has
                                    [ Selector.text "Watch it in the queue's jobs"
                                    , Selector.attribute (Html.Attributes.href "/queue/jobs")
                                    ]
                            ]
            , test "an answer with nothing on its way is not taken for one" <|
                \_ ->
                    case opened |> QueueAdmin.askPurge |> QueueAdmin.confirm |> Tuple.second of
                        Just call ->
                            QueueAdmin.answered onPage call (Ok ()) opened
                                |> Expect.equal ( opened, Nothing )

                        Nothing ->
                            Expect.fail "the purge was not sent"
            , test "an answer to an earlier act is not taken for the one on its way" <|
                \_ ->
                    -- A probe of the first build: an act confirmed, the page left and come back
                    -- to, another confirmed, and the first one's answer shown as the second's.
                    -- Each act is numbered, the numbers outlast leaving the page, and the same
                    -- purge asked twice is two acts.
                    let
                        ( first, firstCall ) =
                            scoped failedInProcessTorrent
                                |> QueueAdmin.askPurge
                                |> QueueAdmin.confirm

                        purgingAgain =
                            ( first, firstCall )
                                |> answer onPage (Ok ())
                                |> Tuple.first
                                |> move onPage elsewhere
                                |> move elsewhere onPage
                                |> choose failedInProcessTorrent
                                |> Tuple.first
                                |> QueueAdmin.counted failedInProcessTorrent (Ok { tally | statuses = [ ( Failed, 2 ) ] })
                                |> QueueAdmin.askPurge
                                |> QueueAdmin.confirm
                                |> Tuple.first
                    in
                    case firstCall of
                        Just call ->
                            QueueAdmin.answered onPage call (Err refusedByBitmagnet) purgingAgain
                                |> Expect.equal ( purgingAgain, Nothing )

                        Nothing ->
                            Expect.fail "the first purge was not sent"
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
        , describe "where the person is"
            -- What happens to the page as the person moves, or their Identity changes, is
            -- decided here, from plain values `Main` hands over after every update.
            [ test "leaving the page drops what was asked and chosen" <|
                \_ ->
                    scoped failedInProcessTorrent
                        |> edits [ QueueAdmin.RematchSet True ]
                        |> QueueAdmin.askPurge
                        |> move onPage elsewhere
                        |> move elsewhere onPage
                        |> QueueAdmin.counted QueueAdmin.emptyScope (Ok tally)
                        |> Expect.all
                            [ QueueAdmin.confirm >> Tuple.second >> Expect.equal Nothing
                            , rendered >> Query.has [ Selector.text "16 jobs match." ]
                            , rendered >> Query.find [ Selector.id "reprocess-rematch" ] >> Query.has [ Selector.checked False ]
                            ]
            , test "staying on the page, with the same Identity, leaves it as it was" <|
                \_ ->
                    let
                        asked =
                            scoped failedInProcessTorrent |> QueueAdmin.askPurge
                    in
                    move onPage onPage asked
                        |> QueueAdmin.confirm
                        |> Tuple.second
                        |> Maybe.map QueueAdmin.document
                        |> Expect.equal (Just (purgeDocument failedInProcessTorrent))
            , test "an act on its way outlasts leaving the page, and nothing else is asked about until it is answered" <|
                \_ ->
                    let
                        ( reprocessing, call ) =
                            opened
                                |> QueueAdmin.askReprocess
                                |> QueueAdmin.confirm

                        backAgain =
                            reprocessing
                                |> move onPage elsewhere
                                |> move elsewhere onPage
                                |> QueueAdmin.counted QueueAdmin.emptyScope (Ok tally)
                    in
                    backAgain
                        |> Expect.all
                            [ QueueAdmin.askPurge >> QueueAdmin.confirm >> Tuple.second >> Expect.equal Nothing
                            , rendered >> Query.has [ Selector.text "Queueing the batch…" ]
                            , \state ->
                                answer onPage (Ok ()) ( state, call )
                                    |> Tuple.first
                                    |> rendered
                                    |> Query.has [ Selector.text "bitmagnet queued the batch." ]
                            ]
            , test "an act on its way outlasts a change of Identity too" <|
                \_ ->
                    opened
                        |> QueueAdmin.askPurge
                        |> QueueAdmin.confirm
                        |> Tuple.first
                        |> move onPage resolving
                        |> move resolving onPage
                        |> QueueAdmin.counted QueueAdmin.emptyScope (Ok tally)
                        |> Expect.all
                            [ QueueAdmin.askReprocess >> QueueAdmin.confirm >> Tuple.second >> Expect.equal Nothing
                            , rendered >> Query.has [ Selector.text "Purging every job in bitmagnet's queue, in every queue and status…" ]
                            ]
            , test "an answer that lands while another page is showing asks for no count" <|
                \_ ->
                    opened
                        |> QueueAdmin.askPurge
                        |> QueueAdmin.confirm
                        |> Tuple.mapFirst (move onPage elsewhere)
                        |> answer elsewhere (Ok ())
                        |> Tuple.second
                        |> Expect.equal Nothing
            , test "an answer that lands while the page is refused asks for no count" <|
                \_ ->
                    -- The Identity was refetched mid-act and lost queue::mutate: the page shows
                    -- the refusal, and a count would only be refused too.
                    opened
                        |> QueueAdmin.askPurge
                        |> QueueAdmin.confirm
                        |> Tuple.mapFirst (move onPage refusedHere)
                        |> answer refusedHere (Ok ())
                        |> Tuple.second
                        |> Expect.equal Nothing
            , test "what became of an act answered while the page was not showing is shown on coming back, once" <|
                \_ ->
                    let
                        cameBack =
                            scoped failedInProcessTorrent
                                |> QueueAdmin.askPurge
                                |> QueueAdmin.confirm
                                |> Tuple.mapFirst (move onPage elsewhere)
                                |> answer elsewhere (Err (Graphql.Http.HttpError Graphql.Http.NetworkError))
                                |> Tuple.first
                                |> move elsewhere elsewhere
                                |> move elsewhere onPage
                    in
                    cameBack
                        |> Expect.all
                            [ rendered >> Query.has [ Selector.text "Whether bitmagnet purged them is not known." ]
                            , move onPage elsewhere >> move elsewhere onPage >> rendered >> Query.hasNot [ Selector.text "Whether bitmagnet purged them is not known." ]
                            ]
            , test "an act refused because the Identity had gone stale is told once the Identity is resolved" <|
                \_ ->
                    -- `Main` refetches the Identity on the refusal, which leaves the page; the
                    -- refusal is still the answer to the person's act.
                    scoped failedInProcessTorrent
                        |> QueueAdmin.askPurge
                        |> QueueAdmin.confirm
                        |> answer onPage (Err refusedAsStale)
                        |> Tuple.first
                        |> move onPage resolving
                        |> move resolving onPage
                        |> rendered
                        |> Query.has [ Selector.text "bitmagnet did not purge the jobs: ", Selector.text "You are not allowed to mutate queue." ]
            , test "what was shown on the page is not shown again on coming back" <|
                \_ ->
                    scoped failedInProcessTorrent
                        |> QueueAdmin.askPurge
                        |> QueueAdmin.confirm
                        |> answer onPage (Ok ())
                        |> Tuple.first
                        |> move onPage onPage
                        |> move onPage elsewhere
                        |> move elsewhere onPage
                        |> rendered
                        |> Query.hasNot [ Selector.text "bitmagnet purged" ]
            ]
        , describe "counting again"
            [ test "a count that failed can be asked for again" <|
                \_ ->
                    opened
                        |> choose failedInProcessTorrent
                        |> Tuple.first
                        |> QueueAdmin.counted failedInProcessTorrent (Err (ApiError.BadStatus 504))
                        |> Expect.all
                            [ rendered >> Query.has [ Selector.tag "button", Selector.text "Count again" ]
                            , QueueAdmin.recount >> Tuple.second >> QueueAdmin.document >> Expect.equal (countDocument failedInProcessTorrent)
                            , QueueAdmin.recount >> Tuple.first >> rendered >> Query.has [ Selector.text "Counting the jobs that match…" ]
                            , viewSending
                                >> Query.find [ Selector.tag "button", Selector.containing [ Selector.text "Count again" ] ]
                                >> Event.simulate Event.click
                                >> Event.expect RecountSent
                            ]
            , test "is not offered while a count is on its way or in hand" <|
                \_ ->
                    Expect.all
                        [ rendered >> Query.hasNot [ Selector.text "Count again" ]
                        , choose failedInProcessTorrent >> Tuple.first >> rendered >> Query.hasNot [ Selector.text "Count again" ]
                        ]
                        opened
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
                    sent { form | options = { rematch = True, localSearch = True, apis = True, purge = True } }
                        |> Result.map (\input -> [ input.classifierRematch, input.localSearchDisabled, input.apisDisabled, input.purge ])
                        |> Expect.equal (Ok [ Present True, Present False, Present False, Present True ])
            , test "sizes are whole numbers of torrents, spaces aside" <|
                \_ ->
                    sent { form | sizes = { batchSize = " 250 ", chunkSize = "5000" } }
                        |> Result.map (\input -> ( input.batchSize, input.chunkSize ))
                        |> Expect.equal (Ok ( Present 250, Present 5000 ))
            , test "a size that is not a whole number above nought is not sent" <|
                \_ ->
                    -- bitmagnet reads nought as its own default, which is not what was typed.
                    List.map (\typed -> sent { form | sizes = { batchSize = typed, chunkSize = typed } })
                        [ "0", "-5", "1.5", "lots", "" ]
                        |> Expect.equal (List.repeat 5 (Err [ QueueAdmin.BadBatchSize, QueueAdmin.BadChunkSize ]))
            , test "a size past what a GraphQL Int holds is not sent" <|
                \_ ->
                    ( sent { form | sizes = { batchSize = "2147483647", chunkSize = "10000" } } |> Result.map .batchSize
                    , sent { form | sizes = { batchSize = "2147483648", chunkSize = "10000" } } |> Result.map .batchSize
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


countDocument : QueueAdmin.Scope -> String
countDocument scope =
    Graphql.Document.serializeQuery (QueueAdmin.countQuery scope)


purgeDocument : QueueAdmin.Scope -> String
purgeDocument scope =
    Graphql.Document.serializeMutation (QueueAdmin.purgeMutation scope)


batchDocument : QueueAdmin.Batch -> String
batchDocument chosen =
    Graphql.Document.serializeMutation (QueueAdmin.reprocessMutation chosen)


{-| bitmagnet turning the act away before running any of it: its answer to a cookie-authenticated
mutation from another origin, as the fixture server gave it on 2026-10-11. The act was not
done.
-}
refusedByBitmagnet : Graphql.Http.Error ()
refusedByBitmagnet =
    Graphql.Http.HttpError
        (Graphql.Http.BadStatus { url = "/graphql", statusCode = 403, statusText = "Forbidden", headers = Dict.empty }
            """{"errors":[{"message":"browser session request must be same-origin"}],"data":null}"""
        )


{-| gqlgen turning away a document it could not validate, with 422: nothing in it ran.
-}
invalidRequest : Graphql.Http.Error ()
invalidRequest =
    Graphql.Http.HttpError
        (Graphql.Http.BadStatus { url = "/graphql", statusCode = 422, statusText = "Unprocessable Entity", headers = Dict.empty }
            """{"errors":[{"message":"Cannot query field \\"purgeJobs\\" on type \\"QueueMutation\\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}],"data":null}"""
        )


{-| No answer that says whether the act was done: it may have been. A fault inside bitmagnet is
among them, since a connection lost during COMMIT gives INTERNAL\_SERVER\_ERROR though Postgres
may have committed.
-}
unanswered : List (Graphql.Http.Error ())
unanswered =
    [ Graphql.Http.GraphqlError (GraphqlError.UnparsedData Encode.null)
        [ { message = "internal server error"
          , locations = Nothing
          , details = Dict.fromList [ ( "extensions", Encode.object [ ( "code", Encode.string "INTERNAL_SERVER_ERROR" ) ] ) ]
          }
        ]
    , Graphql.Http.HttpError Graphql.Http.NetworkError
    , Graphql.Http.HttpError Graphql.Http.Timeout
    , Graphql.Http.HttpError (Graphql.Http.BadStatus { url = "/graphql", statusCode = 504, statusText = "Gateway Timeout", headers = Dict.empty } "")
    , Graphql.Http.HttpError (Graphql.Http.BadPayload (Decode.Failure "not the schema" Encode.null))
    ]


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
    , recountAsked = ()
    , purgeAsked = ()
    , reprocessAsked = ()
    , cancelled = ()
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
choose : QueueAdmin.Scope -> QueueAdmin.State -> ( QueueAdmin.State, QueueAdmin.Call )
choose scope state =
    let
        ( queued, asked ) =
            List.foldl (\queue ( current, _ ) -> QueueAdmin.toggleQueue queue current) ( state, QueueAdmin.opening ) scope.queues
    in
    List.foldl (\status ( current, _ ) -> QueueAdmin.toggleStatus status current) ( queued, asked ) scope.statuses


edits : List QueueAdmin.Edit -> QueueAdmin.State -> QueueAdmin.State
edits changes state =
    List.foldl QueueAdmin.edit state changes


{-| What a control sends, for the tests that ask which.
-}
type Sent
    = QueueSent String
    | StatusSent QueueJobStatus
    | EditSent QueueAdmin.Edit
    | RecountSent
    | SomethingElse


editOf : Sent -> Maybe QueueAdmin.Edit
editOf message =
    case message of
        EditSent change ->
            Just change

        _ ->
            Nothing


queueOf : Sent -> Maybe String
queueOf message =
    case message of
        QueueSent queue ->
            Just queue

        _ ->
            Nothing


statusOf : Sent -> Maybe QueueJobStatus
statusOf message =
    case message of
        StatusSent status ->
            Just status

        _ ->
            Nothing


sending : QueueAdmin.Messages Sent
sending =
    { queueToggled = QueueSent
    , statusToggled = StatusSent
    , edited = EditSent
    , recountAsked = RecountSent
    , purgeAsked = SomethingElse
    , reprocessAsked = SomethingElse
    , cancelled = SomethingElse
    , confirmed = SomethingElse
    }


viewSending : QueueAdmin.State -> Query.Single Sent
viewSending state =
    QueueAdmin.view (Route.basePath "") sending state
        |> Query.fromHtml


control : String -> QueueAdmin.State -> Query.Single Sent
control controlId state =
    viewSending state
        |> Query.find [ Selector.id controlId ]


chipOf : String -> QueueAdmin.State -> Query.Single Sent
chipOf label state =
    viewSending state
        |> Query.find [ Selector.class "chip", Selector.containing [ Selector.text label ] ]


{-| The batch the form makes as it opens.
-}
defaultBatch : QueueAdmin.Batch
defaultBatch =
    { torrents = QueueAdmin.AllTorrents
    , options = { rematch = False, localSearch = False, apis = False, purge = False }
    , sizes = { batchSize = 100, chunkSize = 10000 }
    }


{-| bitmagnet's answer to an act, if one was sent.
-}
answer : QueueAdmin.Whereabouts -> Result (Graphql.Http.Error ()) () -> ( QueueAdmin.State, Maybe QueueAdmin.Call ) -> ( QueueAdmin.State, Maybe QueueAdmin.Call )
answer whereabouts result ( state, call ) =
    case call of
        Just sent_ ->
            QueueAdmin.answered whereabouts sent_ result state

        Nothing ->
            ( state, Nothing )


{-| The person moved, or their Identity changed, from `before` to `after`.
-}
move : QueueAdmin.Whereabouts -> QueueAdmin.Whereabouts -> QueueAdmin.State -> QueueAdmin.State
move before after =
    QueueAdmin.settle { before = before, after = after }


administrator : Identity.Identity
administrator =
    Identity.UserAuthenticated user [ Identity.graphql "queue" "query", Identity.graphql "queue" "mutate" ]


reader : Identity.Identity
reader =
    Identity.UserAuthenticated user [ Identity.graphql "queue" "query" ]


user : Identity.User
user =
    { id = 1
    , username = "ada"
    , role = "admin"
    , email = Nothing
    , lastLoginAt = Nothing
    , createdAt = Time.millisToPosix 0
    , updatedAt = Time.millisToPosix 0
    }


onPage : QueueAdmin.Whereabouts
onPage =
    { route = Route.AdminQueue, identity = administrator, access = Route.Allowed }


elsewhere : QueueAdmin.Whereabouts
elsewhere =
    { route = Route.Status, identity = administrator, access = Route.Allowed }


refusedHere : QueueAdmin.Whereabouts
refusedHere =
    { route = Route.AdminQueue, identity = reader, access = Route.Refused "Your Identity does not permit changing bitmagnet's queue." }


{-| The Identity being fetched again, as `Main` does on a refusal that says it has gone stale.
-}
resolving : QueueAdmin.Whereabouts
resolving =
    { route = Route.AdminQueue, identity = Identity.Unknown, access = Route.PendingIdentity }


{-| bitmagnet refusing an act to an Identity that has lost queue::mutate.
-}
refusedAsStale : Graphql.Http.Error ()
refusedAsStale =
    Graphql.Http.GraphqlError (GraphqlError.UnparsedData Encode.null)
        [ { message = "unauthorized"
          , locations = Nothing
          , details =
                Dict.fromList
                    [ ( "extensions"
                      , Encode.object
                            [ ( "code", Encode.string "UNAUTHORIZED" )
                            , ( "namespace", Encode.string "graphql" )
                            , ( "object", Encode.string "queue" )
                            , ( "action", Encode.string "mutate" )
                            ]
                      )
                    ]
          }
        ]
