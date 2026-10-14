# Moderation ban file version 1

A ban file shares exact banned membership identifiers. It is separate from ordinary
catalogue export and administrative database backups. It carries no private review metadata
and supplies no usable BitTorrent infohash or magnet.

## Frozen envelope

The file is one UTF-8 JSON object, with these fields only:

| Field              | Type   | Meaning                                                       |
| ------------------ | ------ | ------------------------------------------------------------- |
| `version`          | number | Exactly the integer `1`, the envelope version                 |
| `identifierFormat` | string | Exactly `sha256-btih-v1`, independent of the envelope version |
| `source`           | string | Optional operator-chosen label for the whole file             |
| `entries`          | array  | Zero or more banned membership assertions                     |

Each entry has exactly two fields: `digest`, a canonical 64-character lowercase hexadecimal
[version-1 moderation identifier](adr/0006-version-moderation-identifiers.md), and `origin`,
either `moderator` or `automatic`. Every entry asserts a ban; there is no per-entry status.
Export orders entries by digest ascending, without duplicates. An empty export contains
`"entries": []`. JSON property order and whitespace are not meaningful.

```json
{
  "version": 1,
  "identifierFormat": "sha256-btih-v1",
  "entries": [
    {
      "digest": "20fed20512528a3e12b7bcda59b0c84eff6bbe33007773edc0ecc785d2c450b6",
      "origin": "moderator"
    }
  ]
}
```

This example uses the harmless all-zero compatibility vector from ADR 0006. The fixed
[round-trip fixture](../internal/moderation/testdata/ban-file-v1.json) includes both origins
and an optional source label, using that vector and the all-ones vector. It is the
compatibility input for explicit file import and export.
Unsupported envelope versions, identifier formats and origins are outside this contract.
Raw infohashes, magnets, torrent names, file paths, reviewer identities, timestamps and
free-text evidence are not fields of this format.

## Selection and provenance

`GET /graphql/moderation/export` requires `http:moderation:export` and returns an attachment
named `moderation-bans-v1.json`. Its default includes only currently banned decisions with
local moderator origin. `?includeAutomatic=true` deliberately adds currently banned local
automatic decisions and preserves `automatic` origin. Omitting the parameter or setting it
to `false` selects reviewed bans only; other nonempty values return HTTP 400.

Pending, allowed and imported decisions are always excluded. An imported assertion's
original moderator origin does not make it a local review. An explicit later local
moderator ban is a local decision. Evidence purge preserves ban eligibility. The download
omits `source`; it never derives a public label from private actors or imported provenance.

The service reads decision identifiers and origins only, with no evidence/history joins,
in 100-row keyset pages from one read-only repeatable-read snapshot. Concurrent reviews
cannot splice different states into one file. Reads bypass the catalogue cache. HTTP
responses use `Cache-Control: no-store`; catalogue permissions do not bypass this export
Permission or the normal catalogue visibility predicate.

If a download fails or is interrupted, discard its incomplete JSON file and retry. The
server cannot change the HTTP status after a streamed response has begun.

## Additive trusted import

`POST /graphql/moderation/import` requires the separate `http:moderation:import` Permission.
Send the selected JSON file as the entire request body with `Content-Type: application/json`.
The file is limited to **8 MiB (8,388,608 bytes) and 10,000 entries**, counting repeated
entries before deduplication. The optional source label is limited to **256 UTF-8 bytes**
to bound provenance copied into every decision and history row. Split larger lists into complete supported files; each file
is a separate atomic import. The response is HTTP 200 with `{"entries": N}`, where `N`
counts distinct accepted identifiers, including existing bans and local allows left intact.
It does not claim that every entry changed a decision.

The entire bounded UTF-8 document is validated before any mutation. Required fields must
be present and non-null. Unsupported versions, formats or origins, noncanonical digests,
unknown/case-variant fields, duplicate JSON properties, trailing documents, conflicting
origins for one digest and truncated files return HTTP 400 without applying a prefix.
Repeated entries with the same digest and origin are no-ops. Oversize files, entry counts or source labels
return HTTP 413; a non-JSON content type returns HTTP 415. Permission and browser-origin
refusals return HTTP 403. Server failures return HTTP 500; inspect current decisions or
retry the same file if the outcome was lost. A successful import is additive and idempotent.

One transaction applies all accepted entries through the moderation service's ingestion
and decision locks in stable identifier order. A later file omitting an identifier never
withdraws its ban. A local allow is final until an explicit local review changes it.
Existing bans retain their origin and source, including locally reviewed bans. New or
promoted bans have `import` origin and retain the entry's original `moderator`/`automatic`
origin and optional whole-file source. No import grants a provenance label greater trust
than the local allow, and imported assertions remain excluded from export.

Existing matching catalogue metadata is copied into private evidence by an exact indexed
digest lookup in the import transaction. Available complete stored paths are retained;
uncapped private paths already retained take precedence. Unseen digests remain valid bans
without metadata and suppress future crawler/importer ingestion. Catalogue deletion cannot
erase private evidence. Duplicate imports do not reconstruct explicitly purged evidence.

Selecting and importing a file means the operator trusts its source. There is no signature,
subscription, scheduled synchronization or additional approval workflow. Cookie-backed
browser uploads must have an HTTPS Origin matching the request host; bearer clients require
the selected Object action and their owner's current Role. Forward this exact path through
the same HTTPS proxy as `/graphql`.

## Privacy limit

The digest is a membership identifier, not anonymity. Someone holding a candidate raw
infohash can compute its digest and test whether that torrent appears in the file. Origin
records how the exporting instance made its decision; it does not independently verify
file contents. An optional source label is public metadata chosen by the file producer,
so it must not contain private review information.
