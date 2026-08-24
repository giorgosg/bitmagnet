module Identity exposing (Identity(..), ObjectAction, User, can, fetch, graphql, isUnauthorized, permissions)

{-| The canonical browser Identity comes from `self.identity`; the browser credential is
an implementation detail owned by bitmagnet and never appears in this module.
-}

import Bitmagnet
import Graphql.Http
import Graphql.Operation exposing (RootQuery)
import Graphql.SelectionSet as SelectionSet exposing (SelectionSet)
import Magnes.Api.Object
import Magnes.Api.Object.APIKey as APIKey
import Magnes.Api.Object.AuthObjectAction as AuthObjectAction
import Magnes.Api.Object.Self as Self
import Magnes.Api.Object.SelfQuery as SelfQuery
import Magnes.Api.Object.User as ApiUser
import Magnes.Api.Query as Query
import Time


type Identity
    = Unknown
    | Failed String
    | Anonymous (List ObjectAction)
    | UserAuthenticated User (List ObjectAction)
    | APIKeyAuthenticated User String (List ObjectAction)


type alias User =
    { id : Int
    , username : String
    , role : String
    , email : Maybe String
    , lastLoginAt : Maybe Time.Posix
    , createdAt : Time.Posix
    , updatedAt : Time.Posix
    }


type alias ObjectAction =
    { namespace : String
    , object : String
    , action : String
    }


type alias Response =
    { user : Maybe User
    , apiKeyName : Maybe String
    , permissions : List ObjectAction
    }


fetch : String -> (Result (Graphql.Http.Error Identity) Identity -> msg) -> Cmd msg
fetch apiUrl toMsg =
    selection
        |> Bitmagnet.queryRequest apiUrl
        |> Graphql.Http.send toMsg


selection : SelectionSet Identity RootQuery
selection =
    Query.self
        (SelfQuery.identity
            (SelectionSet.map3 Response
                (Self.user userSelection)
                (Self.apiKey APIKey.name)
                (Self.permissions objectActionSelection)
                |> SelectionSet.map fromResponse
            )
        )


userSelection : SelectionSet User Magnes.Api.Object.User
userSelection =
    SelectionSet.map7 User
        ApiUser.id
        ApiUser.username
        ApiUser.role
        ApiUser.email
        ApiUser.lastLoginAt
        ApiUser.createdAt
        ApiUser.updatedAt


objectActionSelection : SelectionSet ObjectAction Magnes.Api.Object.AuthObjectAction
objectActionSelection =
    SelectionSet.map3 ObjectAction
        AuthObjectAction.namespace
        AuthObjectAction.object
        AuthObjectAction.action


fromResponse : Response -> Identity
fromResponse response =
    case ( response.user, response.apiKeyName ) of
        ( Nothing, Nothing ) ->
            Anonymous response.permissions

        ( Just user, Nothing ) ->
            UserAuthenticated user response.permissions

        ( Just user, Just apiKeyName ) ->
            APIKeyAuthenticated user apiKeyName response.permissions

        ( Nothing, Just _ ) ->
            Failed "bitmagnet returned an API-key Identity without its owning User."


permissions : Identity -> List ObjectAction
permissions identity =
    case identity of
        Anonymous actions ->
            actions

        UserAuthenticated _ actions ->
            actions

        APIKeyAuthenticated _ _ actions ->
            actions

        Unknown ->
            []

        Failed _ ->
            []


{-| Construct the Object action used by browser GraphQL call sites. Other namespaces are
kept intact when they arrive from the server because API-key workflows may present them.
-}
graphql : String -> String -> ObjectAction
graphql object action =
    { namespace = "graphql", object = object, action = action }


{-| Permission checks control presentation only; bitmagnet remains authoritative. The
server currently emits either a literal or `**` for each component. This deliberately
does not implement a larger glob language until the server emits one: equality alone
would reject the `admin` Role's `**/**/**`, while speculative wildcard syntax could grant
more than the server does.
-}
can : ObjectAction -> Identity -> Bool
can requested identity =
    permissions identity
        |> List.any
            (\granted ->
                componentMatches granted.namespace requested.namespace
                    && componentMatches granted.object requested.object
                    && componentMatches granted.action requested.action
            )


componentMatches : String -> String -> Bool
componentMatches granted requested =
    granted == "**" || granted == requested


{-| Temporary compatibility with bitmagnet's string-only errors. Issue 08 replaces this
single substring check with stable `extensions.code` handling after the server contract
lands.
-}
isUnauthorized : Graphql.Http.Error a -> Bool
isUnauthorized error =
    case error of
        Graphql.Http.GraphqlError _ errors ->
            List.any (\item -> String.contains "unauthorized" (String.toLower item.message)) errors

        Graphql.Http.HttpError _ ->
            False
