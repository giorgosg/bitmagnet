# Freeze a minimal versioned ban-file envelope

Explicit moderation sharing uses a versioned JSON file, with an identifier format and
entries containing only the digest and decision origin. An optional whole-file source
label is public producer metadata. The [version-1 contract](../moderation-ban-file.md) and
[fixed harmless fixture](../../internal/moderation/testdata/ban-file-v1.json) freeze this
boundary independently of private review records and database backups.

Default export includes local moderator-reviewed bans only. Automatic bans need deliberate
inclusion and retain their origin. Imported assertions are excluded: an original moderator
origin from another instance cannot silently become a local review or be republished by
default. Pending/allowed records and private evidence/history are never serialization
inputs. Snapshot paging keeps a large file consistent without materializing the ledger.

A digest permits candidate membership testing and provides no anonymity guarantee. No
signature, synchronization, report, vote or content-verification claim follows from a file.
This export establishes the later import compatibility boundary; it does not implement import.
