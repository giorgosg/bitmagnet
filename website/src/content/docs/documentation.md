---
title: Documentation
description: Explore Magnes, use your catalogue, and operate your instance.
---

Magnes discovers torrent metadata, organises it in PostgreSQL, and makes your catalogue
searchable through its Elm interface and APIs. These guides cover everyday use,
configuration and maintenance, as well as installation.

For an introduction to the project and its relationship to Bitmagnet, see
[About Magnes](../project/about/).

## Use your instance

- [Using the interface](../guides/interface/): search your catalogue, inspect results,
  and find the status and administration pages.
- [Text search](../guides/text-search/): combine words, phrases, alternatives and
  exclusions with filters.
- [Classification and custom rules](../guides/classifier/): understand how metadata
  becomes searchable content and customise its classification.
- [Servarr integration](../guides/servarr/): connect Prowlarr, Sonarr and Radarr.
- [Import torrent metadata](../guides/import/): add an existing catalogue.

## Run and maintain your instance

- [Configuration](../reference/configuration/): set database, HTTP and worker options.
- [Authentication and Permissions](../reference/authentication/): manage access for
  Users and integrations.
- [Crawling and resource use](../guides/crawler-resources/): understand throughput,
  storage growth and classification backlogs.
- [Monitoring and profiling](../guides/monitoring/): inspect health, logs and metrics.
- [Backups and upgrades](../guides/backup-upgrades/): preserve your data, test a restore
  and plan an upgrade.
- [Reprocess existing torrents](../guides/reprocessing/): apply classifier changes to
  stored metadata.
- [Troubleshooting](../guides/troubleshooting/): diagnose startup, sign-in and search
  problems.

## Get started or build on Magnes

- [Installation](../getting-started/installation/): build and start a new instance.
- [HTTPS and first login](../getting-started/first-login/): register the first
  administrator and sign in.
- [Moving from Bitmagnet](../getting-started/from-bitmagnet/): review deployment and
  access changes before migrating an existing instance.
- [HTTP and API endpoints](../reference/endpoints/): connect your own clients.
- [Contributing](../project/contributing/): find the source and development guides.
