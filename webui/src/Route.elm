module Route exposing (Access(..), BasePath, LoginParams, RegisterParams, Route(..), SearchParams, basePath, emptySearch, fromUrl, guard, toHref)

{-| Routes are real paths, not fragments — see the README.

`toHref` is the inverse of the parser and is the only way links are built. Adding a
variant breaks it at compile time rather than producing a dead link.

-}

import Facet exposing (Filters)
import Identity
import Sort exposing (Sort)
import Url exposing (Url)
import Url.Builder as Builder
import Url.Parser as Parser exposing ((</>), (<?>), Parser, oneOf, s, top)
import Url.Parser.Query as Query


type Route
    = Search SearchParams
    | Torrent String
    | Login LoginParams
    | Register RegisterParams
    | UserOverview
    | APIKeys
    | AdminUsers
    | AdminRoles
    | AdminInvitations
    | NotFound


type alias LoginParams =
    { returnUrl : Maybe String }


type alias RegisterParams =
    { code : Maybe String }


type Access
    = PendingIdentity
    | Allowed
    | RedirectTo Route
    | Refused String


{-| The URL prefix where Magnes is mounted. An empty value means the origin root.
Normalising it once keeps parsing and link building exact inverses.
-}
type BasePath
    = BasePath String


basePath : String -> BasePath
basePath raw =
    raw
        |> String.split "/"
        |> List.filter (not << String.isEmpty)
        |> String.join "/"
        |> (\path ->
                if String.isEmpty path then
                    BasePath ""

                else
                    BasePath ("/" ++ path)
           )


{-| Everything the URL says about a search. The model derives from this, never the
reverse, so a search is always a link — the ordering included.
-}
type alias SearchParams =
    { q : Maybe String
    , sort : Sort
    , filters : Filters
    }


emptySearch : SearchParams
emptySearch =
    { q = Nothing, sort = Sort.default, filters = Facet.empty }


parser : Parser (Route -> a) a
parser =
    oneOf
        [ Parser.map (searchWith Nothing Nothing [] []) top
        , Parser.map searchWith
            (s "search"
                <?> Query.string "q"
                <?> Query.string "sort"
                <?> Query.custom Facet.contentParam identity
                <?> Query.custom Facet.fileParam identity
            )
        , Parser.map Torrent (s "torrent" </> infoHash)
        , Parser.map (Login << LoginParams) (s "login" <?> Query.string "returnUrl")
        , Parser.map (Register << RegisterParams) (s "register" <?> Query.string "code")
        , Parser.map UserOverview (s "account")
        , Parser.map APIKeys (s "account" </> s "api-keys")
        , Parser.map AdminUsers (s "admin" </> s "users")
        , Parser.map AdminRoles (s "admin" </> s "roles")
        , Parser.map AdminInvitations (s "admin" </> s "invitations")
        ]


{-| Only a real info hash matches, so `/torrent/abc` falls through to `NotFound` instead
of being sent to bitmagnet, which answers a malformed hash with a raw decoding error.
Matching lowercases it too, so a link and a lookup always agree.
-}
infoHash : Parser (String -> a) a
infoHash =
    Parser.custom "INFO_HASH" <|
        \segment ->
            let
                lowered =
                    String.toLower segment
            in
            if String.length lowered == 40 && String.all Char.isHexDigit lowered then
                Just lowered

            else
                Nothing


searchWith : Maybe String -> Maybe String -> List String -> List String -> Route
searchWith q sort contentValues fileValues =
    Search
        { q = q |> Maybe.andThen nonBlank
        , sort = sort |> Maybe.map Sort.fromParam |> Maybe.withDefault Sort.default
        , filters = Facet.fromQuery contentValues fileValues
        }


nonBlank : String -> Maybe String
nonBlank raw =
    case String.trim raw of
        "" ->
            Nothing

        trimmed ->
            Just trimmed


fromUrl : BasePath -> Url -> Route
fromUrl (BasePath prefix) url =
    pathWithin prefix url.path
        |> Maybe.andThen
            (\path ->
                Parser.parse parser
                    { url
                        | path =
                            if String.isEmpty path then
                                "/"

                            else
                                path
                    }
            )
        |> Maybe.withDefault NotFound


pathWithin : String -> String -> Maybe String
pathWithin prefix path =
    if String.isEmpty prefix then
        Just path

    else if path == prefix then
        Just "/"

    else if String.startsWith (prefix ++ "/") path then
        Just (String.dropLeft (String.length prefix) path)

    else
        Nothing


toHref : BasePath -> Route -> String
toHref (BasePath prefix) route =
    prefix
        ++ (case route of
                Search params ->
                    -- The default sort is left out, so an ordinary search is still a bare ?q=.
                    Builder.absolute [ "search" ]
                        (List.filterMap identity
                            [ Maybe.map (Builder.string "q") params.q
                            , if params.sort == Sort.default then
                                Nothing

                              else
                                Just (Builder.string "sort" (Sort.toParam params.sort))
                            ]
                            ++ Facet.toQueryParams params.filters
                        )

                Torrent hash ->
                    Builder.absolute [ "torrent", hash ] []

                Login params ->
                    Builder.absolute [ "login" ]
                        (Maybe.map (Builder.string "returnUrl") params.returnUrl
                            |> Maybe.map List.singleton
                            |> Maybe.withDefault []
                        )

                Register params ->
                    Builder.absolute [ "register" ]
                        (Maybe.map (Builder.string "code") params.code
                            |> Maybe.map List.singleton
                            |> Maybe.withDefault []
                        )

                UserOverview ->
                    Builder.absolute [ "account" ] []

                APIKeys ->
                    Builder.absolute [ "account", "api-keys" ] []

                AdminUsers ->
                    Builder.absolute [ "admin", "users" ] []

                AdminRoles ->
                    Builder.absolute [ "admin", "roles" ] []

                AdminInvitations ->
                    Builder.absolute [ "admin", "invitations" ] []

                NotFound ->
                    Builder.absolute [] []
           )


guard : BasePath -> Identity.Identity -> Route -> Access
guard mount identity route =
    case identity of
        Identity.Unknown ->
            PendingIdentity

        Identity.Failed message ->
            Refused message

        Identity.Anonymous _ ->
            anonymousAccess mount route

        Identity.APIKeyAuthenticated _ _ _ ->
            anonymousAccess mount route

        Identity.UserAuthenticated _ _ ->
            userAccess identity route


anonymousAccess : BasePath -> Route -> Access
anonymousAccess mount route =
    case route of
        UserOverview ->
            loginRedirect mount route

        APIKeys ->
            loginRedirect mount route

        AdminUsers ->
            loginRedirect mount route

        AdminRoles ->
            loginRedirect mount route

        AdminInvitations ->
            loginRedirect mount route

        _ ->
            Allowed


loginRedirect : BasePath -> Route -> Access
loginRedirect mount route =
    RedirectTo (Login { returnUrl = Just (toHref mount route) })


userAccess : Identity.Identity -> Route -> Access
userAccess identity route =
    case route of
        Login _ ->
            RedirectTo UserOverview

        Register _ ->
            RedirectTo UserOverview

        AdminUsers ->
            requireAdministration identity

        AdminRoles ->
            requireAdministration identity

        AdminInvitations ->
            requireAdministration identity

        _ ->
            Allowed


requireAdministration : Identity.Identity -> Access
requireAdministration identity =
    if Identity.can (Identity.graphql "auth" "query") identity then
        Allowed

    else
        Refused "Your Identity does not permit administration."
