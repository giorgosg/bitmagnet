module JobOrder exposing (JobOrder, all, default, fromKey, fromParams, key, label, toParams)

{-| How the queue's jobs are ordered: one of bitmagnet's three fields and a direction.

Every field and direction is offered, as one menu of six rather than a field menu and a
direction toggle, so the whole ordering is one choice with one name.

-}

import Magnes.Api.Enum.QueueJobsOrderByField as Field exposing (QueueJobsOrderByField(..))
import Url.Builder as Builder


type alias JobOrder =
    { field : QueueJobsOrderByField
    , descending : Bool
    }


{-| Newest first. Not the Angular UI's default of last run first: a job that has not run
has no `ran_at`, and Postgres puts nulls first when descending, so that ordering opens on
the backlog of pending jobs rather than on what just happened.
-}
default : JobOrder
default =
    { field = Created_at, descending = True }


{-| Menu order: each field in its own direction first, then reversed.
-}
all : List JobOrder
all =
    List.concatMap
        (\field -> [ { field = field, descending = ownDirection field }, { field = field, descending = not (ownDirection field) } ])
        [ Created_at, Ran_at, Priority ]


{-| The direction a field is read in when nothing says otherwise: times newest first, and
priority from the jobs that run first, which are the lowest numbers.
-}
ownDirection : QueueJobsOrderByField -> Bool
ownDirection field =
    case field of
        Created_at ->
            True

        Ran_at ->
            True

        Priority ->
            False


{-| What the menu says. Lower case, as the search's orderings are.
-}
label : JobOrder -> String
label order =
    case ( order.field, order.descending ) of
        ( Created_at, True ) ->
            "newest first"

        ( Created_at, False ) ->
            "oldest first"

        ( Ran_at, True ) ->
            "last run first"

        ( Ran_at, False ) ->
            "first run first"

        ( Priority, False ) ->
            "priority, lowest number first"

        ( Priority, True ) ->
            "priority, highest number first"



-- URL


{-| The default leaves the URL alone, and a direction is written only when it is not the
field's own, so `?order=ran_at` is how last run first is linked.
-}
toParams : JobOrder -> List Builder.QueryParameter
toParams order =
    if order == default then
        []

    else
        Builder.string "order" (Field.toString order.field)
            :: (if order.descending == ownDirection order.field then
                    []

                else
                    [ Builder.string "direction"
                        (if order.descending then
                            "desc"

                         else
                            "asc"
                        )
                    ]
               )


{-| Unknown values fall back rather than failing the page, so an old link still opens.
-}
fromParams : Maybe String -> Maybe String -> JobOrder
fromParams fieldParam directionParam =
    case fieldParam |> Maybe.andThen Field.fromString of
        Nothing ->
            default

        Just field ->
            { field = field
            , descending =
                case directionParam of
                    Just "desc" ->
                        True

                    Just "asc" ->
                        False

                    _ ->
                        ownDirection field
            }



-- MENU


{-| The menu's value for an ordering: a `select` carries strings.
-}
key : JobOrder -> String
key order =
    Field.toString order.field
        ++ (if order.descending then
                "-desc"

            else
                "-asc"
           )


fromKey : String -> JobOrder
fromKey raw =
    all
        |> List.filter (\order -> key order == raw)
        |> List.head
        |> Maybe.withDefault default
