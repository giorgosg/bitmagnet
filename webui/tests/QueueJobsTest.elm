module QueueJobsTest exposing (suite)

import ApiError
import Expect
import Graphql.Document
import Html.Attributes
import Json.Decode as Decode
import Magnes.Api.Enum.QueueJobStatus exposing (QueueJobStatus(..))
import Magnes.Api.Enum.QueueJobsOrderByField exposing (QueueJobsOrderByField(..))
import QueueJobs
import Route
import Test exposing (Test, describe, test)
import Test.Html.Event as Event
import Test.Html.Query as Query
import Test.Html.Selector as Selector
import Time


suite : Test
suite =
    describe "QueueJobs"
        [ describe "query"
            [ test "filters through the facets, so each facet's counts ignore its own filter" <|
                \_ ->
                    Graphql.Document.serializeQuery (QueueJobs.query failedPriority)
                        |> Expect.all
                            [ String.contains "queue: {aggregate: true, filter: [\"process_torrent\"]}" >> Expect.equal True
                            , String.contains "status: {aggregate: true, filter: [failed]}" >> Expect.equal True
                            , String.contains "queues:" >> Expect.equal False
                            , String.contains "statuses:" >> Expect.equal False
                            ]
            , test "orders by the chosen field, then by creation in the same direction" <|
                \_ ->
                    Graphql.Document.serializeQuery (QueueJobs.query failedPriority)
                        |> String.contains "orderBy: [{field: priority, descending: true}, {field: created_at, descending: true}]"
                        |> Expect.equal True
            , test "does not repeat creation when it is the ordering" <|
                \_ ->
                    Graphql.Document.serializeQuery (QueueJobs.query Route.emptyJobs)
                        |> String.contains "orderBy: [{field: created_at, descending: true}]"
                        |> Expect.equal True
            , test "asks for its page by number, with a count of every page" <|
                \_ ->
                    Graphql.Document.serializeQuery (QueueJobs.query failedPriority)
                        |> String.contains "limit: 20, page: 3, totalCount: true, hasNextPage: true"
                        |> Expect.equal True
            , test "an unfiltered facet sends no filter, which is no filter at all" <|
                \_ ->
                    Graphql.Document.serializeQuery (QueueJobs.query Route.emptyJobs)
                        |> String.contains "facets: {status: {aggregate: true}, queue: {aggregate: true}}"
                        |> Expect.equal True
            , test "reads bitmagnet's answer, putting statuses in the order of a job's life" <|
                \_ ->
                    Decode.decodeString (Graphql.Document.decoder (QueueJobs.query Route.emptyJobs)) answer
                        |> Expect.equal (Ok page)
            ]
        , describe "payload"
            [ test "is pretty-printed when it parses as JSON" <|
                \_ ->
                    QueueJobs.prettyPayload """{"infoHashes":["ab","cd"],"opts":{"rematch":true,"none":null},"empty":{},"list":[]}"""
                        |> Expect.equal
                            (String.join "\n"
                                [ "{"
                                , "  \"infoHashes\": ["
                                , "    \"ab\","
                                , "    \"cd\""
                                , "  ],"
                                , "  \"opts\": {"
                                , "    \"rematch\": true,"
                                , "    \"none\": null"
                                , "  },"
                                , "  \"empty\": {},"
                                , "  \"list\": []"
                                , "}"
                                ]
                            )
            , test "keeps every literal as it was sent, numbers past a double's precision included" <|
                \_ ->
                    QueueJobs.prettyPayload """{"id":12345678901234567891,"text":"a, {b}: [c] \\"d, e\\"","f":1.50}"""
                        |> Expect.equal
                            (String.join "\n"
                                [ "{"
                                , "  \"id\": 12345678901234567891,"
                                , "  \"text\": \"a, {b}: [c] \\\"d, e\\\"\","
                                , "  \"f\": 1.50"
                                , "}"
                                ]
                            )
            , test "re-indents JSON that came with whitespace of its own" <|
                \_ ->
                    QueueJobs.prettyPayload "{ \"a\" :\n [ 1 ,2 ] }"
                        |> Expect.equal "{\n  \"a\": [\n    1,\n    2\n  ]\n}"
            , test "is shown raw when it does not parse" <|
                \_ ->
                    QueueJobs.prettyPayload "{\"a\": 1,"
                        |> Expect.equal "{\"a\": 1,"
            ]
        , describe "view"
            [ test "shows each facet's counts as bitmagnet aggregated them" <|
                \_ ->
                    viewed Route.emptyJobs (QueueJobs.loaded page QueueJobs.empty)
                        |> Query.findAll [ Selector.class "chip" ]
                        |> Query.index 2
                        |> Query.has [ Selector.text "pending", Selector.containing [ Selector.class "chip-count", Selector.text "4" ] ]
            , test "a chosen queue bitmagnet does not count is still offered, so it can be unchosen" <|
                \_ ->
                    viewed { emptyJobs | queues = [ "reindex" ] } (QueueJobs.loaded page QueueJobs.empty)
                        |> Query.find [ Selector.class "chip", Selector.attribute (Html.Attributes.attribute "aria-pressed" "true") ]
                        |> Query.has [ Selector.text "reindex", Selector.text "0" ]
            , test "choosing a value asks for the first page with it added" <|
                \_ ->
                    viewed { emptyJobs | page = 2 } (QueueJobs.loaded page QueueJobs.empty)
                        |> Query.find [ Selector.class "chip", Selector.containing [ Selector.text "failed" ] ]
                        |> Event.simulate Event.click
                        |> Event.expect (Just { emptyJobs | statuses = [ Failed ] })
            , test "choosing an ordering asks for the first page in it" <|
                \_ ->
                    viewed { emptyJobs | page = 2 } (QueueJobs.loaded page QueueJobs.empty)
                        |> Query.find [ Selector.tag "select" ]
                        |> Event.simulate (Event.input "ran_at-desc")
                        |> Event.expect (Just { emptyJobs | order = { field = Ran_at, descending = True } })
            , test "a row is collapsed until opened, and then shows the payload and the whole error" <|
                \_ ->
                    ( viewed Route.emptyJobs (QueueJobs.loaded page QueueJobs.empty)
                        |> Query.findAll [ Selector.class "job-details" ]
                        |> Query.count (Expect.equal 0)
                    , viewed Route.emptyJobs (QueueJobs.loaded page QueueJobs.empty |> QueueJobs.toggle "job-2")
                        |> Query.find [ Selector.class "job-details" ]
                        |> Query.has
                            [ Selector.text "{\n  \"infoHashes\": [\n    \"ab\"\n  ]\n}"
                            , Selector.text longError
                            ]
                    )
                        |> (\( collapsed, opened ) -> Expect.all [ always collapsed, always opened ] ())
            , test "opening a row is announced on its button" <|
                \_ ->
                    viewed Route.emptyJobs (QueueJobs.loaded page QueueJobs.empty |> QueueJobs.toggle "job-2")
                        |> Query.findAll [ Selector.attribute (Html.Attributes.attribute "aria-expanded" "true") ]
                        |> Query.count (Expect.equal 1)
            , test "pages are links that keep the filters and ordering" <|
                \_ ->
                    viewed { failedPriority | page = 2 } (QueueJobs.loaded { page | totalCount = 61, hasNextPage = True } QueueJobs.empty)
                        |> Expect.all
                            [ Query.find [ Selector.tag "a", Selector.containing [ Selector.text "Next" ] ]
                                >> Query.has [ Selector.attribute (Html.Attributes.href "/magnes/queue/jobs?queue=process_torrent&status=failed&order=priority&direction=desc&page=3") ]
                            , Query.find [ Selector.tag "a", Selector.containing [ Selector.text "Previous" ] ]
                                >> Query.has [ Selector.attribute (Html.Attributes.href "/magnes/queue/jobs?queue=process_torrent&status=failed&order=priority&direction=desc") ]
                            , Query.has [ Selector.text "Page 2 of 4" ]
                            ]
            , test "an empty queue says so, and a filter that matches nothing says that instead" <|
                \_ ->
                    ( viewed Route.emptyJobs (QueueJobs.loaded { page | jobs = [], totalCount = 0 } QueueJobs.empty)
                        |> Query.has [ Selector.text "The queue holds no jobs." ]
                    , viewed failedPriority (QueueJobs.loaded { page | jobs = [], totalCount = 0 } QueueJobs.empty)
                        |> Query.has [ Selector.text "No jobs match these filters." ]
                    )
                        |> (\( unfiltered, filtered ) -> Expect.all [ always unfiltered, always filtered ] ())
            , test "a page past the last one says how many there are" <|
                \_ ->
                    viewed { emptyJobs | page = 9 } (QueueJobs.loaded { page | jobs = [], totalCount = 3 } QueueJobs.empty)
                        |> Query.has [ Selector.text "There is only 1 page of jobs." ]
            , test "a failed request says why" <|
                \_ ->
                    viewed Route.emptyJobs (QueueJobs.failed ApiError.ServiceUnavailable QueueJobs.empty)
                        |> Query.find [ Selector.attribute (Html.Attributes.attribute "role" "alert") ]
                        |> Query.has [ Selector.text (ApiError.toMessage ApiError.ServiceUnavailable) ]
            ]
        ]


{-| What a view's event asks for: the list it navigates to, if any.
-}
messages : QueueJobs.Messages (Maybe Route.JobsParams)
messages =
    { navigate = Just, toggled = always Nothing }


viewed : Route.JobsParams -> QueueJobs.State -> Query.Single (Maybe Route.JobsParams)
viewed params state =
    QueueJobs.view (Route.basePath "/magnes") Time.utc messages params state
        |> Query.fromHtml


emptyJobs : Route.JobsParams
emptyJobs =
    Route.emptyJobs


failedPriority : Route.JobsParams
failedPriority =
    { queues = [ "process_torrent" ]
    , statuses = [ Failed ]
    , order = { field = Priority, descending = True }
    , page = 3
    }


longError : String
longError =
    "tmdb: request failed after 3 attempts: Get \"https://api.themoviedb.org/3/search/movie\": context deadline exceeded"


{-| Shaped as bitmagnet answers: each facet's counts sorted by label, and a value with no
jobs left out unless it was chosen.
-}
answer : String
answer =
    """
    {"data": {"queue": {"jobs": {
      "items": [
        {"id": "job-1", "queue": "process_torrent", "status": "processed", "payload": "{\\"seed\\":1}",
         "priority": 1, "retries": 0, "maxRetries": 2, "runAfter": "2026-10-09T08:10:00Z",
         "ranAt": "2026-10-09T08:10:30Z", "error": null, "createdAt": "2026-10-09T08:10:00Z"},
        {"id": "job-2", "queue": "process_torrent_batch", "status": "failed", "payload": "{\\"infoHashes\\":[\\"ab\\"]}",
         "priority": 10, "retries": 2, "maxRetries": 2, "runAfter": "2026-10-09T07:00:00Z",
         "ranAt": null, "error": "tmdb: request failed after 3 attempts: Get \\"https://api.themoviedb.org/3/search/movie\\": context deadline exceeded",
         "createdAt": "2026-10-09T07:00:00Z"}
      ],
      "totalCount": 2,
      "hasNextPage": false,
      "aggregations": {
        "queue": [
          {"value": "process_torrent", "label": "process_torrent", "count": 8},
          {"value": "process_torrent_batch", "label": "process_torrent_batch", "count": 8}
        ],
        "status": [
          {"value": "failed", "label": "failed", "count": 4},
          {"value": "pending", "label": "pending", "count": 4},
          {"value": "retry", "label": "retry", "count": 4}
        ]
      }
    }}}}
    """


page : QueueJobs.Page
page =
    { jobs =
        [ { id = "job-1"
          , queue = "process_torrent"
          , status = Processed
          , payload = "{\"seed\":1}"
          , priority = 1
          , retries = 0
          , maxRetries = 2

          -- 2026-10-09T08:10:00Z
          , runAfter = Time.millisToPosix 1791533400000
          , ranAt = Just (Time.millisToPosix 1791533430000)
          , error = Nothing
          , createdAt = Time.millisToPosix 1791533400000
          }
        , { id = "job-2"
          , queue = "process_torrent_batch"
          , status = Failed
          , payload = "{\"infoHashes\":[\"ab\"]}"
          , priority = 10
          , retries = 2
          , maxRetries = 2

          -- 2026-10-09T07:00:00Z
          , runAfter = Time.millisToPosix 1791529200000
          , ranAt = Nothing
          , error = Just longError
          , createdAt = Time.millisToPosix 1791529200000
          }
        ]
    , totalCount = 2
    , hasNextPage = False
    , queues = [ ( "process_torrent", 8 ), ( "process_torrent_batch", 8 ) ]
    , statuses = [ ( Pending, 4 ), ( Retry, 4 ), ( Failed, 4 ) ]
    }
