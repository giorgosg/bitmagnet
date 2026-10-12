module Sort exposing (Sort(..), all, default, fromParam, label, toParam)

{-| The orderings offered in the UI.

bitmagnet's `orderBy` is a list of field/direction pairs and so composes arbitrarily, but
a menu of composed sorts is a menu nobody reads. These are the single orderings worth
naming; `Sort` is the vocabulary shared by the URL, the query and the menu, so adding one
is a compile error in all three places at once.

-}


type Sort
    = Relevance
    | Newest
    | Oldest
    | Largest
    | Smallest
    | MostSeeders
    | ByName


{-| Menu order: the default first, then time, then size, then the rest.
-}
all : List Sort
all =
    [ Relevance, Newest, Oldest, Largest, Smallest, MostSeeders, ByName ]


default : Sort
default =
    Relevance


{-| What the menu says. Lower case, because these read as the tail of "sorted by…".
-}
label : Sort -> String
label sort =
    case sort of
        Relevance ->
            "relevance"

        Newest ->
            "newest"

        Oldest ->
            "oldest"

        Largest ->
            "largest"

        Smallest ->
            "smallest"

        MostSeeders ->
            "most seeders"

        ByName ->
            "name"


toParam : Sort -> String
toParam sort =
    case sort of
        Relevance ->
            "relevance"

        Newest ->
            "newest"

        Oldest ->
            "oldest"

        Largest ->
            "largest"

        Smallest ->
            "smallest"

        MostSeeders ->
            "seeders"

        ByName ->
            "name"


{-| Unknown values fall back to the default rather than failing the route, so a link that
predates a renamed sort still opens a search.
-}
fromParam : String -> Sort
fromParam raw =
    all
        |> List.filter (\sort -> toParam sort == raw)
        |> List.head
        |> Maybe.withDefault default
