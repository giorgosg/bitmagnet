module Health exposing (Check, Report, State(..), Worker, fetch, indicator, query, view)

{-| Whether bitmagnet is healthy: its overall status, each check behind it, and which of
its workers are running.

Workers are asked for only by an Identity holding `workers::query`. The core `user` Role
does not hold it, and a refusal there would take the health report down with it: `workers`
is non-null at the root, so bitmagnet's refusal nulls all of `data`, and Magnes treats any
GraphQL error as a failure anyway.

-}

import ApiError
import Bitmagnet
import Format
import Graphql.Http
import Graphql.Operation exposing (RootQuery)
import Graphql.SelectionSet as SelectionSet exposing (SelectionSet)
import Html exposing (Html, a, div, h1, h2, p, span, table, tbody, td, text, th, thead, tr)
import Html.Attributes exposing (attribute, class, classList, href, scope, title)
import Identity
import Magnes.Api.Enum.HealthStatus exposing (HealthStatus(..))
import Magnes.Api.Object
import Magnes.Api.Object.HealthCheck as HealthCheck
import Magnes.Api.Object.HealthQuery as HealthQuery
import Magnes.Api.Object.Worker as ApiWorker
import Magnes.Api.Object.WorkersListAllQueryResult as WorkersListAll
import Magnes.Api.Object.WorkersQuery as WorkersQuery
import Magnes.Api.Query as Query
import Route
import Time


{-| `workers` is `Nothing` when they were not asked for, which is not the same as an
instance running none.
-}
type alias Report =
    { status : HealthStatus
    , checks : List Check
    , workers : Maybe (List Worker)
    }


{-| `checkedAt` is the timestamp as bitmagnet sends it, which is a time the check ran only
when its status says it ran: `up` or `down`. For a check that has not run yet, or one that
is switched off, bitmagnet sends the time its checker started instead, or Go's zero time
where the checker was never started, as in the test fixture
(`internal/health/check.go`, `mapStateToCheckerResult`).
-}
type alias Check =
    { key : String
    , status : HealthStatus
    , checkedAt : Time.Posix
    , error : Maybe String
    }


type alias Worker =
    { key : String
    , started : Bool
    }


{-| The last answer, shared by the status page and the header. A failed poll replaces a
report rather than leaving it standing: a report from before bitmagnet stopped answering
says nothing true about it now.
-}
type State
    = Unasked
    | Reported Report
    | Unavailable ApiError.Failure


fetch : String -> Identity.Identity -> (Result (Graphql.Http.Error Report) Report -> msg) -> Cmd msg
fetch apiUrl identity toMsg =
    query identity
        |> Bitmagnet.queryRequest apiUrl
        -- Finish before the next 30-second poll can supersede this answer.
        |> Graphql.Http.withTimeout 20000
        |> Graphql.Http.send toMsg


query : Identity.Identity -> SelectionSet Report RootQuery
query identity =
    if Identity.can (Identity.graphql "workers" "query") identity then
        SelectionSet.map2 (\( status, checks ) workers -> Report status checks (Just workers))
            health
            (Query.workers (WorkersQuery.listAll (WorkersListAll.workers workerSelection)))

    else
        SelectionSet.map (\( status, checks ) -> Report status checks Nothing) health


health : SelectionSet ( HealthStatus, List Check ) RootQuery
health =
    Query.health
        (SelectionSet.map2 Tuple.pair
            HealthQuery.status
            (HealthQuery.checks checkSelection)
        )


checkSelection : SelectionSet Check Magnes.Api.Object.HealthCheck
checkSelection =
    SelectionSet.map4 Check
        HealthCheck.key
        HealthCheck.status
        HealthCheck.timestamp
        HealthCheck.error


workerSelection : SelectionSet Worker Magnes.Api.Object.Worker
workerSelection =
    SelectionSet.map2 Worker
        ApiWorker.key
        ApiWorker.started


{-| The status page.
-}
view : Time.Zone -> State -> Html msg
view zone state =
    div [ class "panel health" ]
        (h1 [] [ text "Status" ]
            :: (case state of
                    Unasked ->
                        [ p [ class "notice" ] [ text "Checking…" ] ]

                    Unavailable failure ->
                        [ p [ class "notice error", attribute "role" "alert" ] [ text (ApiError.toMessage failure) ] ]

                    Reported report ->
                        [ p [ class "health-summary" ] [ text (meaning report.status).summary ]
                        , checkTable zone report.checks
                        , workerSection report.workers
                        ]
               )
        )


{-| What a status says wherever it is shown, in one place, so the page, its rows and the
header cannot describe it three different ways.

  - `summary` is the status as bitmagnet's overall one: the page's headline, and the
    header's name for itself. bitmagnet's overall status is its worst check's, and it ranks
    `inactive` with `up` (`aggregateStatus` in `internal/health/check.go`). So overall
    `down` means a check is down while bitmagnet itself answers, which is degraded, as the
    Angular UI also calls it. Overall `inactive` never occurs, though the enum allows it.
  - `check` is the status of one check.
  - `failing` marks a down check as a failure, and raises the header's alarm. `unknown` is
    a check that has not run yet and `inactive` one that is switched off, such as TMDB
    without an API key; neither is anything going wrong.
  - `notRun` is what a check's "Last checked" says instead of a time, when its status
    means it did not run. Its timestamp is then not a time it ran: see `Check`.

-}
type alias Meaning =
    { summary : String
    , check : String
    , tone : String
    , failing : Bool
    , notRun : Maybe String
    }


meaning : HealthStatus -> Meaning
meaning status =
    case status of
        Up ->
            { summary = "bitmagnet is up.", check = "Up", tone = "up", failing = False, notRun = Nothing }

        Down ->
            { summary = "bitmagnet is degraded: a check is down."
            , check = "Down"
            , tone = "down"
            , failing = True
            , notRun = Nothing
            }

        Unknown ->
            { summary = "Not every check has run yet."
            , check = "Pending"
            , tone = "pending"
            , failing = False
            , notRun = Just "Never"
            }

        Inactive ->
            { summary = "bitmagnet is inactive."
            , check = "Inactive"
            , tone = "inactive"
            , failing = False
            , notRun = Just "—"
            }


checkTable : Time.Zone -> List Check -> Html msg
checkTable zone list =
    div []
        [ h2 [] [ text "Checks" ]
        , table [ class "health-checks" ]
            [ thead []
                [ tr []
                    [ th [ scope "col" ] [ text "Check" ]
                    , th [ scope "col" ] [ text "Status" ]
                    , th [ scope "col" ] [ text "Last checked" ]
                    ]
                ]
            , tbody [] (List.map (checkRow zone) list)
            ]
        ]


{-| The error is shown whenever bitmagnet sends one: mostly when the check is down, but
also while a failing check is still inside its tolerance and reported as up.
-}
checkRow : Time.Zone -> Check -> Html msg
checkRow zone check =
    let
        status =
            meaning check.status
    in
    tr [ classList [ ( "health-failing", status.failing ) ] ]
        [ th [ scope "row" ] [ text (checkName check.key) ]
        , td []
            (text status.check
                :: (case check.error of
                        Just error ->
                            [ span [ class "health-error" ] [ text error ] ]

                        Nothing ->
                            []
                   )
            )
        , td [] [ text (Maybe.withDefault (Format.dateTime zone check.checkedAt) status.notRun) ]
        ]


{-| The names bitmagnet's own UI gives its checks. A check Magnes does not know keeps its
key, which is at least what bitmagnet's logs call it.
-}
checkName : String -> String
checkName key =
    case key of
        "dht" ->
            "DHT"

        "postgres" ->
            "Postgres"

        "tmdb" ->
            "TMDB"

        _ ->
            key


workerSection : Maybe (List Worker) -> Html msg
workerSection workers =
    div []
        (h2 [] [ text "Workers" ]
            :: (case workers of
                    Nothing ->
                        [ p [ class "notice" ] [ text "Your Identity may not see bitmagnet's workers." ] ]

                    Just [] ->
                        [ p [ class "notice" ] [ text "No workers are registered." ] ]

                    Just list ->
                        [ table [ class "health-workers" ]
                            [ thead []
                                [ tr []
                                    [ th [ scope "col" ] [ text "Worker" ]
                                    , th [ scope "col" ] [ text "State" ]
                                    ]
                                ]
                            , tbody [] (List.map workerRow list)
                            ]
                        ]
               )
        )


workerRow : Worker -> Html msg
workerRow worker =
    tr []
        [ th [ scope "row" ] [ text (workerName worker.key) ]
        , td []
            [ text
                (if worker.started then
                    "Started"

                 else
                    "Not started"
                )
            ]
        ]


{-| As `checkName`: bitmagnet's own names, and the key for anything else.
-}
workerName : String -> String
workerName key =
    case key of
        "dht_crawler" ->
            "DHT crawler"

        "http_server" ->
            "HTTP server"

        "queue_server" ->
            "Queue server"

        _ ->
            key


{-| The header's glance at the same answer: a dot, linking to the status page.

It speaks up only when something wants attention. While bitmagnet is up it is a quiet dot,
and a hollow one while a check is still pending; a down check or no answer at all adds a
word and the accent. A switched-off check changes nothing here, since bitmagnet counts it
as up overall. Its accessible name is always the full sentence, which also contains the
word, so what is read out and what is seen agree.

Nothing is drawn before the first answer, as the Identity menu draws nothing before the
Identity: a guess would flicker on every load.

-}
indicator : Route.BasePath -> State -> Html msg
indicator mount state =
    case reading state of
        Nothing ->
            text ""

        Just { sentence, tone, word } ->
            a
                [ class "health-indicator"
                , class ("health-" ++ tone)
                , classList [ ( "health-alarm", word /= Nothing ) ]
                , href (Route.toHref mount Route.Status)
                , attribute "aria-label" sentence
                , title sentence
                ]
                (span [ class "health-dot", attribute "aria-hidden" "true" ] []
                    :: (case word of
                            Just shown ->
                                [ span [ class "health-word", attribute "aria-hidden" "true" ] [ text shown ] ]

                            Nothing ->
                                []
                       )
                )


{-| What the indicator says for a state: its sentence, the tone the stylesheet colours by,
and the word it shows when it needs looking at.
-}
reading : State -> Maybe { sentence : String, tone : String, word : Maybe String }
reading state =
    case state of
        Unasked ->
            Nothing

        Unavailable failure ->
            Just
                { sentence = "bitmagnet's health is unavailable: " ++ ApiError.toMessage failure
                , tone = "unavailable"
                , word = Just "unavailable"
                }

        Reported report ->
            let
                { summary, tone, failing } =
                    meaning report.status
            in
            Just
                { sentence = summary
                , tone = tone
                , word =
                    if failing then
                        Just "degraded"

                    else
                        Nothing
                }
