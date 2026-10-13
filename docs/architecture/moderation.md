# Moderation ledger

`internal/moderation` owns exact harmful-content decisions keyed by the versioned
[moderation identifier](../adr/0006-version-moderation-identifiers.md). Ordinary catalogue reads now enforce
this ledger. Crawler/import ingestion and the default classifier share automatic decisions
from the existing harmful keyword group; crawler discovery and crawler/import writes enforce exact bans. Legacy
Bloom blocking is removed. There is no moderation HTTP or GraphQL API yet.

## Keyword matching

`moderate_keywords` replaces the default harmful-keyword `delete` branch. It uses the
same keyword grammar and combined base name/base file paths as the previous CEL condition.
`KeywordPolicy` compiles the merged classifier `keywords.banned` source and is supplied to
both ingestion factories. Crawler metadata and importer names are checked before catalogue
writes or classification queues; harmful crawler matches retain every observed path before
`SaveFilesThreshold` caps ordinary file rows. Ordinary metadata acquires no private evidence.
Known single-file metainfo retains its implicit filename; imports without metainfo keep
missing paths nil. The classifier action reuses this policy for existing catalogue rows.
Before evaluating it, the action checks for a local allow; allowed torrents continue normal
classification, regardless of keyword changes. Category-deletion rules remain independent.

Matches create pending decisions by default. `moderation.automatic_ban: true` instead
creates banned decisions with automatic origin. Either decision hides the torrent using the
mandatory catalogue predicate and stops classification successfully. No moderation tag is
added, and torrent metadata is retained. Private evidence contains the original name, all
available full file paths, raw infohash, matched text and compiled keyword rule. Missing
file metadata remains missing; no torrent payload is fetched for review.

The service serializes assertions with moderator decisions; an allow made during matching
wins and classification continues when observed by the action. Existing pending/banned
processing inputs are skipped rather than retried as missing hashes. Switching the mode
changes new matches, not existing decisions. After allow, a processing job can complete
missing classification; the later moderator API integration schedules it.

## State and precedence

No ledger row means ordinary catalogue content. A row is pending, allowed or banned, with
moderator, automatic or import origin. Local allows override all automatic/import assertions
until a moderator explicitly reopens or bans them. Existing bans cannot be downgraded by an
automatic pending assertion. Imports may promote pending records, but preserve an existing
ban's origin; imported assertions retain the original moderator/automatic origin and source.
See [ADR 0007](../adr/0007-local-allow-is-final-for-automation.md).

`Service.Flag`, `Allow`, `Ban`, `Reopen`, `ApplyAutomatic` and `ApplyImport` update a decision
and its history atomically. Flags on existing records and repeated assertions are no-ops.
`RetainEvidence` separately enriches pending/banned metadata without changing a decision;
missing fields preserve existing metadata and duplicate evidence does not append history.
An optional raw infohash must produce the record's identifier. Local allows skip enrichment.
These are internal operations; later API wiring must authorize private reads and mutations.

## Storage and transactions

Migration `00025` adds `moderation_decisions`, `moderation_evidence` and
`moderation_history`. Private evidence stores the torrent name, complete supplied file
paths, rule/match metadata and optional raw infohash. Missing metadata is valid, so a
trusted digest-only assertion can precede catalogue discovery. No foreign key points to
`torrents`: catalogue deletion cannot erase the decision or evidence. Filenames describe
metadata, not verified file contents.

Mutations acquire a transaction advisory lock derived from the identifier, then insert
on conflict and lock the exact decision row before updating private rows. Ingestion holds
the same locks through catalogue writes, including identifiers without a decision row.
Batch identifiers are locked in a stable order. Lock-key collisions only serialize work;
membership remains an exact comparison of the complete identifier. Evidence retention and
purge also acquire the same lock. This serializes imports, automatic matches, moderator
decisions and crawler/import ingestion for the same identifier. Failed history writes roll back state and evidence together. All ledger reads
force the no-cache context mode; `Get` reads state and evidence within one repeatable-read
snapshot. The lazy fx factory opens no database for configuration-only CLI commands.

`PurgeEvidence` locks the decision, deletes retained evidence and prior private history,
then records a minimal purge event with current status/origin and timestamp. The decision
and import provenance remain for enforcement and future export. Later observations may
retain newly supplied evidence; purge does not withdraw a ban or allow.

`Search` returns private summaries, with optional status and literal identifier/name/full
file-path search. `History` returns ordered decision events. Both use keyset cursors, default
to 50 items and reject limits above 100. Search has no unbounded count query; it returns a
next cursor only when another page exists. Complete evidence is loaded separately via `Get`.
These operations are separate from ordinary catalogue queries.

## Catalogue visibility

`internal/database/catalogue` owns the mandatory read predicate. Torrent search, direct
infohash lookup, file queries and torrent-content search exclude pending/banned digests.
PostgreSQL computes the frozen v1 SHA-256 over the context plus raw infohash bytes and
compares it with the exact ledger primary key; no Bloom filter or catalogue-wide digest
index is involved. Digest-only bans work before retained evidence exists.

Counts and facets use the same exclusion. Dynamic tag/source/year/collection values hide
metadata backed only by hidden torrents, including a selected value whose count is zero.
Unassociated metadata and source/collection configuration retain their previous visibility.
Shared content stays visible when at least one visible torrent backs it, while hidden
associations cannot contribute to ordinary content queries. GraphQL and Torznab inherit
these constraints, with a separate guard on GraphQL source listings. Operational metrics
and worker counts remain operational totals.

Ordinary catalogue reads force fresh database queries after caller cache options, including
hydration and dynamic facet values. A result cached before a flag or ban cannot return
its old names or paths. The trusted `search.MetadataSearch` is separately injected into
classification, so classification can reuse known content metadata before a visible torrent
exists; GraphQL and Torznab do not receive that interface. It is not a catalogue bypass
option. Private review evidence remains accessible only through the internal moderation
service, pending a permissioned moderator API.

`task export-data` streams only visible catalogue rows and their metadata relationships
from a read-only repeatable-read snapshot. It omits auth/internal key-value state, moderation
decisions, evidence/history and ban lists. See [hacking.md](../hacking.md) for connection and
restore details; a full administrative `pg_dump` remains a separate database backup.

## Ingestion and legacy cutover

`Service.Discoverable` excludes only exact banned decisions. Pending records can acquire
missing evidence and local allows permit ingestion. `Service.Ingest` rechecks under locks
at the transaction write boundary: bans retain observed evidence without inserting torrents,
files, hints, sources or classification work. Pending records preserve complete supplied
paths independently of catalogue deletion and file thresholds. Sparse observations also
copy missing existing catalogue metadata by indexed raw-infohash lookup; already retained
uncapped paths take precedence over capped catalogue rows. Catalogue metadata stays
hidden until a local allow; classification remains a separate operation.

Ordinary GraphQL deletion and classifier category deletion remove catalogue rows only.
They create neither harmful-content decisions nor persistent suppression. Migration `00026`
discards the old `blocked_torrents` key and unlinks its existing large object when unshared;
it never reads the Bloom entries or turns them into decisions. Downgrade cannot restore
them. Old and new executables must not run concurrently against the upgraded database.
