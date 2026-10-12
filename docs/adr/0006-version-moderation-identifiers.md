# Version moderation identifiers over canonical infohash bytes

Moderation identifiers identify torrents for shared ban membership without distributing
usable magnets. Version 1 is `sha256-btih-v1`: SHA-256 of the ASCII bytes
`magnes:moderation:v1:btih:` followed immediately by the raw 20-byte BitTorrent v1
infohash, serialized as exactly 64 lowercase hexadecimal characters. The context prefix
separates this purpose from other hashes; raw bytes avoid variation from magnet names,
trackers, parameter ordering, and hexadecimal or base32 spelling.

`internal/moderation` accepts only `protocol.ID`, whose size fixes the supported hash
kind. Parsing requires the exact format name and canonical digest; it rejects unsupported
versions or hash kinds, prefixes, whitespace, uppercase and wrong lengths. Do not truncate
other hash kinds to fit. Future formats need a new name and preimage; changing this
contract would silently break membership checks against previously shared lists.

This digest is a membership identifier, not an anonymity guarantee: someone holding a
candidate infohash can compute it and test whether that torrent appears on a list. It
cannot be used as the original BitTorrent infohash in a magnet.

## Compatibility vectors

These synthetic inputs were independently calculated with Python's `hashlib.sha256` over
`b"magnes:moderation:v1:btih:" + bytes.fromhex(infohash)`. They are fixed test literals,
not recomputed expectations in the Go tests.

| Raw infohash bytes (hex)                   | Version-1 digest                                                   |
| ------------------------------------------ | ------------------------------------------------------------------ |
| `0000000000000000000000000000000000000000` | `20fed20512528a3e12b7bcda59b0c84eff6bbe33007773edc0ecc785d2c450b6` |
| `ffffffffffffffffffffffffffffffffffffffff` | `9afaa45890d2acf0f8760eb8c7bcb8f55193d8eadf816f1165a02410b68912be` |
| `000102030405060708090a0b0c0d0e0f10111213` | `41a75932cf9e555dca1575a5029f6a665d8ec8ef568e978298a9dbff8c9a913b` |
| `0080ff017f0281fe030405060708090a0b0c0d0e` | `25e64157619734bf1d1fc41154d6cf1caecc7cab0ca5446f7b308c34dd2f1091` |
