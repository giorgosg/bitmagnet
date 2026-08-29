module InvitationsTest exposing (suite)

import ApiError
import Expect
import Html.Attributes
import Identity
import Invitations
import Route
import Test exposing (Test, describe, test)
import Test.Html.Query as Query
import Test.Html.Selector as Selector
import Time


mount : Route.BasePath
mount =
    Route.basePath ""


unclaimed : Invitations.Invitation
unclaimed =
    { code = "abc123"
    , role = "user"
    , createdBy = Just "ada"
    , claimedBy = Nothing
    , expiresAt = Nothing
    , createdAt = Time.millisToPosix 1787666587000
    }


claimed : Invitations.Invitation
claimed =
    { unclaimed | code = "def456", claimedBy = Just "grace" }


administrator : Identity.Identity
administrator =
    Identity.UserAuthenticated
        { id = 1
        , username = "ada"
        , role = "admin"
        , email = Nothing
        , lastLoginAt = Nothing
        , createdAt = Time.millisToPosix 0
        , updatedAt = Time.millisToPosix 0
        }
        [ Identity.graphql "auth" "query", Identity.graphql "auth" "mutate" ]


observer : Identity.Identity
observer =
    Identity.UserAuthenticated
        { id = 2
        , username = "linus"
        , role = "editor"
        , email = Nothing
        , lastLoginAt = Nothing
        , createdAt = Time.millisToPosix 0
        , updatedAt = Time.millisToPosix 0
        }
        [ Identity.graphql "auth" "query" ]


messages : Invitations.Messages ()
messages =
    { roleChosen = always ()
    , expiryChosen = always ()
    , submitted = ()
    , withdrawRequested = always ()
    , withdrawConfirmed = always ()
    , withdrawCancelled = ()
    , pageRequested = always ()
    }


loaded : List Invitations.Invitation -> Invitations.State
loaded invitations =
    Invitations.empty
        |> Invitations.withListing
            (Invitations.Loaded
                { invitations = invitations
                , totalCount = List.length invitations
                , roles = [ "admin", "editor", "user" ]
                }
            )


rendered : Identity.Identity -> Invitations.State -> Query.Single ()
rendered identity state =
    Invitations.view mount Time.utc messages identity state
        |> Query.fromHtml


suite : Test
suite =
    describe "Invitations"
        [ describe "the registration link"
            [ test "is where the Invitation is meant to be used" <|
                \_ ->
                    Invitations.registrationLink mount unclaimed
                        |> Expect.equal "/register?code=abc123"
            , test "carries the mount, so a link from a subpath still works" <|
                \_ ->
                    Invitations.registrationLink (Route.basePath "/magnes") unclaimed
                        |> Expect.equal "/magnes/register?code=abc123"
            , test "is drawn as a real link, not just text to copy by hand" <|
                \_ ->
                    rendered administrator (loaded [ unclaimed ])
                        |> Query.has
                            [ Selector.attribute (Html.Attributes.href "/register?code=abc123") ]
            ]
        , describe "what an Invitation is worth"
            [ test "a claimed one says who claimed it" <|
                \_ ->
                    rendered administrator (loaded [ claimed ])
                        |> Query.has [ Selector.text "grace" ]
            , test "a claimed one is not offered for withdrawal" <|
                \_ ->
                    -- Withdrawing it would take nothing back: the User exists.
                    rendered administrator (loaded [ claimed ])
                        |> Query.hasNot [ Selector.text "Withdraw" ]
            , test "an unclaimed one is" <|
                \_ ->
                    rendered administrator (loaded [ unclaimed ])
                        |> Query.has [ Selector.text "Withdraw" ]
            , test "an Invitation without an expiry says so rather than looking blank" <|
                \_ ->
                    rendered administrator (loaded [ unclaimed ])
                        |> Query.has [ Selector.text "Never" ]
            ]
        , describe "collecting no email"
            -- The spec defers email entirely while bitmagnet's verification is inert.
            [ test "renders no email field" <|
                \_ ->
                    rendered administrator (loaded [ unclaimed ])
                        |> Query.findAll
                            [ Selector.attribute (Html.Attributes.type_ "email") ]
                        |> Query.count (Expect.equal 0)
            ]
        , describe "what the Identity may do"
            [ test "an Identity that cannot mutate is offered no create form" <|
                \_ ->
                    rendered observer (loaded [ unclaimed ])
                        |> Query.hasNot [ Selector.tag "form" ]
            , test "an Identity that cannot mutate is offered no withdrawal" <|
                \_ ->
                    rendered observer (loaded [ unclaimed ])
                        |> Query.hasNot [ Selector.text "Withdraw" ]
            , test "an Identity that can mutate is offered both" <|
                \_ ->
                    rendered administrator (loaded [ unclaimed ])
                        |> Expect.all
                            [ Query.has [ Selector.tag "form" ]
                            , Query.has [ Selector.text "Withdraw" ]
                            ]
            , test "the Roles offered are the ones the server named" <|
                \_ ->
                    rendered administrator (loaded [ unclaimed ])
                        |> Query.findAll [ Selector.tag "option" ]
                        |> Query.count (Expect.atLeast 3)
            ]
        , describe "withdrawing"
            [ test "asks before doing it" <|
                \_ ->
                    -- One click arms it; the destructive click is the second one.
                    loaded [ unclaimed ]
                        |> Invitations.withConfirming (Just unclaimed.code)
                        |> rendered administrator
                        |> Query.has [ Selector.text "Withdraw it?" ]
            , test "arms only the Invitation it was asked about" <|
                \_ ->
                    loaded [ unclaimed, { unclaimed | code = "other" } ]
                        |> Invitations.withConfirming (Just "other")
                        |> rendered administrator
                        |> Query.findAll [ Selector.text "Withdraw it?" ]
                        |> Query.count (Expect.equal 1)
            , test "a refusal is announced rather than leaving the row unchanged in silence" <|
                \_ ->
                    loaded [ unclaimed ]
                        |> Invitations.withWithdrawal (Just ApiError.Unreachable)
                        |> rendered administrator
                        |> Query.has
                            [ Selector.attribute (Html.Attributes.attribute "role" "alert")
                            , Selector.text (ApiError.toMessage ApiError.Unreachable)
                            ]
            ]
        , describe "a created Invitation"
            [ test "is shown, because its code is the whole product of making it" <|
                \_ ->
                    loaded []
                        |> Invitations.withSubmission (Invitations.Created unclaimed)
                        |> rendered administrator
                        |> Query.has
                            [ Selector.attribute (Html.Attributes.attribute "role" "status")
                            , Selector.attribute (Html.Attributes.href "/register?code=abc123")
                            ]
            ]
        , describe "the warning before withdrawing"
            [ test "names the Role the link would have granted" <|
                \_ ->
                    loaded [ { unclaimed | role = "admin" } ]
                        |> Invitations.withConfirming (Just unclaimed.code)
                        |> rendered administrator
                        |> Query.has [ Selector.text "register as admin" ]
            , test "says bitmagnet's own first-run Invitation comes back" <|
                \_ ->
                    -- It has no author because no administrator existed to create it.
                    loaded [ { unclaimed | createdBy = Nothing } ]
                        |> Invitations.withConfirming (Just unclaimed.code)
                        |> rendered administrator
                        |> Query.has [ Selector.text "mints another at startup" ]
            , test "an ordinary Invitation is not told it comes back, because it does not" <|
                \_ ->
                    loaded [ unclaimed ]
                        |> Invitations.withConfirming (Just unclaimed.code)
                        |> rendered administrator
                        |> Query.hasNot [ Selector.text "mints another at startup" ]
            ]
        , describe "the expiry"
            [ test "offers bitmagnet's Go duration strings, not seconds" <|
                \_ ->
                    Invitations.expiries
                        |> List.map Tuple.second
                        |> Expect.equal [ "", "24h0m0s", "168h0m0s", "720h0m0s" ]
            , test "defaults to one that does not expire, which is what the API defaults to" <|
                \_ ->
                    Invitations.empty.expiry
                        |> Expect.equal ""
            ]
        , describe "paging"
            [ test "asks for the next page by offset" <|
                \_ ->
                    Invitations.nextOffset { offset = 0, totalCount = 120 }
                        |> Expect.equal (Just Invitations.pageSize)
            , test "there is no page after the last one" <|
                \_ ->
                    Invitations.nextOffset { offset = 100, totalCount = 120 }
                        |> Expect.equal Nothing
            , test "the previous page is the one before this" <|
                \_ ->
                    Invitations.previousOffset 100
                        |> Expect.equal (Just 50)
            , test "there is nothing before the first page" <|
                \_ ->
                    Invitations.previousOffset 0
                        |> Expect.equal Nothing
            ]
        ]
