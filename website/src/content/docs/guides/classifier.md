---
title: Classification and custom rules
description: Inspect and extend the workflows that organise your torrent metadata.
---

After discovery or import, the processor runs a classification workflow and writes
searchable content. The workflow uses torrent names, available file metadata, hints,
local content records, and optional TMDB lookups. An unidentified torrent can remain
`unknown`; classification is a best-effort interpretation of metadata.

## Start with the included workflow

The default workflow identifies content types and video attributes, tries available
content matches, quarantines harmful-keyword matches, and applies configured category
deletion rules. You can inspect the merged
source and generate its schema with the CLI:

```sh
bitmagnet classifier show
bitmagnet classifier schema --format json
```

For the Docker installation, prefix CLI commands with
`docker compose -f compose.magnes.yml exec magnes`.
The [classifier schema](../../schemas/classifier-0.1.json) on this site is built from
the generated schema in this repository. Use the CLI schema from your deployed
revision when it differs from the documentation revision.

## Where custom rules live

Classifier sources merge in this order, with later sources taking precedence:

1. The embedded core workflow.
2. `classifier.yml` in the `bitmagnet` XDG configuration directory.
3. `classifier.yml` in the process's working directory.
4. The `classifier` section of application configuration.

Keyword and extension groups accumulate entries. Workflows with the same name are
replaced by the later definition. Flags are overridden by later values. Editing a
source file does not automatically reload an already running worker; restart the
process that performs classification after reviewing the merged source.

In the Docker walkthrough, put `classifier.yml` beside `config.yml` in the mounted
`config/` directory. Keep classifier source separate from the application configuration:
`workflows` is a top-level classifier-source key, while `classifier.workflow` selects
which named workflow to run.

## Add a tag without replacing the default classification

This example adds a custom workflow in `classifier.yml`. It runs the included workflow,
then tags torrents whose name contains `public-domain`:

```yaml
workflows:
  public_domain:
    - run_workflow: default
    - if_else:
        condition: 'torrent.name.contains("public-domain")'
        if_action:
          add_tag:
            - public-domain
```

Select it in `config.yml`:

```yaml
classifier:
  workflow: public_domain
```

The condition is a CEL expression evaluated against the torrent being classified.
The tag records your rule's match; it does not verify the content's licence.
Inspect `classifier show` with the same configuration and restart the classification
worker. Try new rules on a restored test database before applying them to your index.

## Control external lookups

Set `tmdb.enabled: false` in application configuration to disable TMDB. For an individual
reprocessing run, `--apisDisabled` disables external lookups without changing the global
configuration. Existing local content can still supply a match; use
`--localSearchDisabled` as well when that is the behaviour you need to inspect.

## Harmful-keyword quarantine

The included workflow sends matches from the existing `keywords.banned` group to pending
moderation by default. Pending and banned torrents are hidden from catalogue queries,
GraphQL details/files, Torznab and catalogue exports. The decision retains the torrent name,
available complete file paths and matching rule privately for review. It adds no tag and
stops classification without deleting metadata or repeatedly retrying the job.

An operator can choose automatic bans in application `config.yml`:

```yaml
moderation:
  automatic_ban: true
```

The default is `false`. Automatic bans retain their origin and evidence; they are distinct
from moderator-reviewed bans. A local allow skips later harmful checks even when keywords
change, while ordinary classification continues. Existing pending/banned decisions are
skipped on reprocessing; changing this setting does not re-decide them.

The moderation ledger and classifier integration are present in this revision. The
moderator API and review UI are separate integrations; complete those
before deploying the moderation feature. Filenames describe metadata and do not verify
file contents.

If you replace the default workflow, use the `moderate_keywords` action for harmful
keyword moderation, or call the included `default` workflow. A custom keyword branch
using `delete` keeps catalogue-deletion semantics and cannot create a reviewable decision.

## Deletion rules

The included workflow supports `classifier.delete_xxx: true` and the
`delete_content_types` flag. For example, in `config.yml`:

```yaml
classifier:
  flags:
    delete_content_types:
      - comic
      - software
```

These rules delete matching catalogue records; they are not search filters. The
processor does not create a harmful-content decision or persistent suppression; deleted
torrents may be rediscovered. Make a backup and test the
rules before reprocessing existing data. Use [reprocessing](../reprocessing/) to apply
changed rules to torrents already in the database.
