# Moderation ledger

`internal/moderation` owns exact harmful-content decisions keyed by the versioned
[moderation identifier](../adr/0006-version-moderation-identifiers.md). This is a persistence
foundation: the classifier, crawler, importer and catalogue reads do not yet consume it,
and there is no moderation HTTP or GraphQL API. Existing blocking behaviour remains until
those integrations land together with catalogue visibility enforcement.

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

Mutations insert on conflict and lock the exact decision row before updating private rows.
This serializes first-time imports, automatic matches and moderator decisions for the same
identifier. Failed history writes roll back state and evidence together. All ledger reads
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
These operations are separate from ordinary catalogue queries and do not activate quarantine.
