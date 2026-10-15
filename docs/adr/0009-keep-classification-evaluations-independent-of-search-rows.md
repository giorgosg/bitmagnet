# Keep classification evaluations independent of search rows

Accepted design, 2026-10-14; implemented in migration `00028`, 2026-10-15. Classification evaluations
are retained by infohash and stage, independently of the searchable classification
rows, because changing a work match replaces those rows and reprocessing reconstructs
their release attributes. A single evaluation table retains the latest completed
evaluation and accepted enhancement output for each stage; ordinary classification
and LLM enhancement remain distinct, and importer hints retain their own authority.

Recipe fingerprints reference sanitized settings kept in the existing database key/value
store. This preserves explanations through database backup and restore without a
separate file registry. Full evaluation history is deferred; retained results must be
checked against current evidence and protected metadata before being reapplied.

The ordinary snapshot is retained before enhancement replay and is used as the next
classifier input, preventing enhancement from triggering itself. Accepted output is
kept independently of the latest completed result and attempt so failures and limited
runs preserve prior valid metadata. A monotonically increasing admission generation,
current evidence/authority checks and the moderation ledger lock guard transactional
application. The internal application service is explicit; scheduling and applying CLI
commands are separate follow-up work.
