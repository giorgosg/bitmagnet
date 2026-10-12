---
title: About Magnes
description: The project and its relationship to Bitmagnet.
---

Magnes is a self-hosted BitTorrent indexer, DHT crawler, content classifier, and
search interface. It builds on [Bitmagnet](https://github.com/bitmagnet-io/bitmagnet)
and work from its community, with a bundled Elm UI and backend changes maintained in
[this repository](https://github.com/giorgosg/magnes).

The crawler discovers infohashes and fetches torrent metadata from peers. PostgreSQL
stores the catalogue and work queue. Classification enriches searchable content;
the UI, GraphQL API, and Torznab endpoint expose it to authorised clients.
Magnes is an indexer; downloads are handled by your torrent client.

These docs describe the Magnes integration branch, `trunk`. Upstream documentation
may describe different UI, authentication, or deployment behaviour. The project keeps
upstream credit and provenance in its source history and integration records.

Magnes inherits Bitmagnet's MIT licence; see the repository's
[licence](https://github.com/giorgosg/magnes/blob/trunk/LICENSE) for its terms.
