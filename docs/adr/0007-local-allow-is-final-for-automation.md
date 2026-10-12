# Local allow decisions are final for automation

A moderator's local allow exempts a torrent from every future automatic harmful-content
check, including changed keyword rules and trusted imported bans. Rechecking after rule
changes would repeatedly undo manual review and recreate the false-positive queue.
Moderators can search allowed records and explicitly reopen a decision when reassessment
is needed. Normal content classification remains independent of moderation.

The exact ledger serializes updates for an identifier. Automatic rules and imports cannot
replace a local allow or downgrade a ban. Importing against an existing local reviewed ban
cannot relabel it as imported or automatic. New imported bans retain both import origin
and the assertion's original moderator/automatic origin; this prevents later export from
misrepresenting an imported assertion as a local review.

Private evidence and decision history outlive catalogue deletion. Explicit evidence purge
clears those retained private rows while retaining the current decision and provenance
needed for enforcement. No subscriptions, scheduled synchronization or user reporting are
part of this decision.
