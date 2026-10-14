# Moderating torrents

The GraphQL moderation surface is separate from catalogue search and tag editing. An
administrator may create a Role named `moderator` with these Object actions, then assign
that Role to the intended User:

```graphql
mutation {
  auth {
    putRole(
      role: "moderator"
      objectActions: [
        { namespace: "graphql", object: "moderation", action: "query" }
        { namespace: "graphql", object: "moderation", action: "mutate" }
        { namespace: "http", object: "moderation", action: "export" }
      ]
    ) {
      name
    }
  }
}
```

This is a custom Role, not a new core Role. The query action permits private review
search, evidence and history; the mutate action permits decisions. Ordinary catalogue
Permissions and tag editing do not grant them. The separate `http:moderation:export`
action permits sharing ban files; private query or mutate alone does not grant export. API keys remain bounded by both their
own selected Object actions and their owner's Role. Actor identifiers come from the
server-resolved User or API key, never request input.

## Browser review

In Magnes, open **Moderation** from the Identity menu. This requires the private query
Permission; decision controls also require mutate. Use Pending, Allowed and Banned lists,
then search a digest, retained name or complete path. Search and history load 50 records per
request. Open a record to review evidence and history, including records with no metadata.
No magnet or content download actions are offered in private review.

Flag a known catalogue infohash with **Flag for review**. Allow, Ban and Reopen require
confirmation; **Purge evidence** separately confirms permanent clearing of retained metadata
and prior private history while keeping the decision. After allow, the page reports if normal
classification is queued. A stale decision must be refreshed before another action. When an
decision answer is lost, refresh the record to inspect its current state; the page never reports unconfirmed
success. A successful flag opens its returned record. If a flag answer is lost, the page
retains its requested infohash through refresh/reload; refreshing a different record cannot
settle it. Open matching retained evidence to inspect the current decision. Dismissing the
unknown-flag warning explicitly acknowledges uncertainty and does not confirm success.
See [UI behavior](../webui/docs/moderation.md).

## Search and review

`moderation.search(input: {status: pending, query: "literal text", limit: 50})` returns
private summaries. Status can be `pending`, `allowed` or `banned`; omitting it searches
all decisions. Text searches the digest, retained name and complete retained file paths
literally. Allowed decisions remain searchable for later reassessment. `next` is an opaque
keyset cursor passed back as `after`. Search and history default to 50 items and reject
limits outside 0–100; zero selects the default. There is no unbounded count query.

`moderation.record(identifier: "<64 lowercase hexadecimal characters>")` returns the
current decision, optional private evidence and a `revision`. Evidence may be absent on
a digest-only import or after purge. `history(input: {limit: 50, after: "..."})` returns
ordered decision events with its own `next` cursor. Filenames describe observed metadata;
no file content is fetched or verified.

## Decisions

`moderation.flag(infoHash: "<40 hexadecimal characters>")` flags an existing catalogue
torrent and retains its server-loaded name and available paths. Flagging a decision that
already exists is refused; use explicit reopen to reassess it.

`allow`, `ban`, `reopen` and `purgeEvidence` take a `ModerationDecisionInput` containing the
digest `identifier` and the exact `expectedRevision` string returned by the latest record
or summary. Keep this string intact; `updatedAt` is for display, and clients with
millisecond date precision must not recreate the revision from it. A stale revision returns
`MODERATION_CONFLICT`; reload and review the current record before retrying. Changed private
evidence or a purge also invalidates the revision, even when the decision's status stays the
same. Repeating unchanged evidence does not invalidate a review.

Allow returns `{record, classificationQueued}`. The decision, its history and any needed
normal classification job commit atomically. It immediately restores ordinary visibility
of retained catalogue metadata. Existing classification is retained; otherwise a normal
processor job is arranged for a known raw infohash. Local allows exempt the torrent from
all later automatic harmful-content rules and imported bans. Normal content classification
continues independently. Reopen explicitly returns an allowed or banned decision to
pending and hides its catalogue metadata again.

The optional `infoHash` in a decision input must compute to its digest. For allow it can
supply a known hash after evidence purge or for a digest-only record, enabling a bounded
catalogue lookup and missing-classification job. It does not recreate purged evidence.
Without a known hash, the allow still succeeds and returns `classificationQueued: false`;
later ingestion respects the allow and arranges ordinary processing when metadata arrives.
No catalogue-wide digest scan is performed. `classificationQueued: true` includes work
already pending through queue deduplication; it does not mean the processor has completed.

Ban is a local moderator decision, distinct from ordinary catalogue deletion. Purge clears
private evidence and prior private history while retaining the current decision and import
provenance for enforcement and export, plus a minimal purge event. It does not unban a
torrent or withdraw an allow. Conflicting/no-op decisions return
`MODERATION_INVALID_TRANSITION`; absent records, malformed identifiers, pages and mismatched
raw hashes have `MODERATION_NOT_FOUND`, `MODERATION_INVALID_IDENTIFIER`,
`MODERATION_INVALID_PAGE` and `MODERATION_INVALID_INFOHASH` codes respectively.

Ordinary GraphQL and Torznab reads keep applying the same pending/banned exclusion, even
for moderators. Private metadata is available only through the moderation query Permission.
In the moderation area, **Export reviewed bans** downloads only currently banned local
moderator decisions. **Export reviewed and automatic bans** deliberately adds local
automatic bans with their origin retained. Both require the export Permission; search and
status controls do not narrow the file. Imported assertions, pending/allowed decisions and
all private evidence/history are excluded. Downloaded files contain membership digests and
decision origins, not magnets or private metadata. See the
[frozen version-1 format and privacy limit](moderation-ban-file.md).

Machine clients can send a bearer API key with the selected export Object action to
`GET /graphql/moderation/export`, optionally with `?includeAutomatic=true`. Its owner's
Role must also permit export. Browser downloads use the existing secure `/graphql` cookie
scope through this exact additional read-only path. Reverse proxies must forward it to the
API. Include `moderation` in an explicit `http_server.options` list to mount the route.
Ordinary `task export-data` remains a visible-catalogue SQL export without ban files.
No file import, background sharing, user reporting or anonymous submission is added here.
