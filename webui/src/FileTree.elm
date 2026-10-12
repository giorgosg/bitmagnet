module FileTree exposing (Entry, Node, flatten, root)

{-| A torrent's file list as a directory tree.

bitmagnet returns flat, `/`-separated paths relative to the torrent's root directory —
`05 Conclusion/018 Commands.html` — so the tree has to be reconstructed here. No Elm tree
package does that part, and the rendering constraints are unusual enough (every row a
known height, folders individually collapsible, custom row content) that borrowing one
would have meant fighting its markup and its model for the easy half of the problem.

`flatten` is the only way to look at a tree. Rendering and height both read off that one
list, so the number of rows drawn and the number of rows the virtualizer was promised
cannot drift apart.

-}

import Set exposing (Set)


type Node
    = Folder { name : String, size : Int, children : List Node }
    | Leaf { name : String, size : Int }


{-| One rendered line: a folder or a file, already indented and already resolved as open
or closed.
-}
type alias Entry =
    { depth : Int
    , key : String
    , name : String
    , size : Int
    , isFolder : Bool
    , collapsed : Bool
    }


{-| The whole torrent as one node. A multi-file torrent's paths are relative to a root
directory named after the torrent itself, which bitmagnet does not repeat in each path —
so the caller supplies it.
-}
root : String -> List { r | path : String, size : Int } -> Node
root name files =
    Folder
        { name = name
        , size = List.foldl (\file total -> total + file.size) 0 files
        , children = build (List.map segments files)
        }


segments : { r | path : String, size : Int } -> ( List String, Int )
segments file =
    ( String.split "/" file.path |> List.filter (not << String.isEmpty), file.size )


build : List ( List String, Int ) -> List Node
build entries =
    let
        ( folders, leaves ) =
            names entries
                |> List.map (nodeFor entries)
                |> List.partition isFolder
    in
    -- Folders first, then files, each in the order the torrent lists them. Keeping the
    -- torrent's own order matters: for anything episodic it is the running order.
    folders ++ leaves


{-| First path segments, de-duplicated, in order of first appearance.
-}
names : List ( List String, Int ) -> List String
names entries =
    List.foldl
        (\( parts, _ ) seen ->
            case parts of
                name :: _ ->
                    if List.member name seen then
                        seen

                    else
                        seen ++ [ name ]

                [] ->
                    seen
        )
        []
        entries


nodeFor : List ( List String, Int ) -> String -> Node
nodeFor entries name =
    let
        rest =
            entries
                |> List.filter (\( parts, _ ) -> List.head parts == Just name)
                |> List.map (\( parts, bytes ) -> ( List.drop 1 parts, bytes ))

        size =
            List.foldl (\( _, bytes ) total -> total + bytes) 0 rest

        deeper =
            List.filter (\( parts, _ ) -> not (List.isEmpty parts)) rest
    in
    if List.isEmpty deeper then
        Leaf { name = name, size = size }

    else
        Folder { name = name, size = size, children = build deeper }


isFolder : Node -> Bool
isFolder node =
    case node of
        Folder _ ->
            True

        Leaf _ ->
            False


{-| The rows to draw, given the set of folder keys that are closed. A closed folder
contributes itself and nothing beneath it.

Keys are full paths, so they are unique within a tree and stable across renders — a folder
stays closed while its siblings open and shut around it.

-}
flatten : Set String -> Node -> List Entry
flatten collapsed node =
    walk collapsed 0 "" node


walk : Set String -> Int -> String -> Node -> List Entry
walk collapsed depth prefix node =
    case node of
        Leaf leaf ->
            [ { depth = depth
              , key = prefix ++ leaf.name
              , name = leaf.name
              , size = leaf.size
              , isFolder = False
              , collapsed = False
              }
            ]

        Folder folder ->
            let
                key =
                    prefix ++ folder.name

                closed =
                    Set.member key collapsed

                self =
                    { depth = depth
                    , key = key
                    , name = folder.name
                    , size = folder.size
                    , isFolder = True
                    , collapsed = closed
                    }
            in
            if closed then
                [ self ]

            else
                self :: List.concatMap (walk collapsed (depth + 1) (key ++ "/")) folder.children
