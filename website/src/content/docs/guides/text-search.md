---
title: Text search
description: Combine words, phrases, exclusions, and facets to narrow your catalogue.
---

Start with a few distinctive words from the torrent name. Unquoted terms are combined
with AND: `ubuntu desktop amd64` requires all three terms, without requiring their order.
Use the UI's facets alongside the text query to narrow content type, source, or other
available attributes.

## Search syntax

| Syntax        | Example                             | Effect                                                                         |
| ------------- | ----------------------------------- | ------------------------------------------------------------------------------ |
| Quoted phrase | `"desktop amd64" ubuntu`            | The quoted words must be adjacent and in order; `ubuntu` can appear elsewhere. |
| Followed by   | `apple . orange`                    | Require `apple` immediately followed by `orange`.                              |
| Either term   | `apple \| orange`                   | Match either word.                                                             |
| Exclusion     | `orange !apple`                     | Match `orange` without `apple`.                                                |
| Word prefix   | `archiv*`                           | Match words beginning with `archiv`.                                           |
| Grouping      | `"banana split" \| (apple !toffee)` | Match the phrase, or `apple` without `toffee`.                                 |

Wildcards are word prefixes expressed with a trailing `*`; they are not arbitrary
substring matches. A leading or middle wildcard is not supported. Punctuation other
than the operators is treated as a separator, so quoting means a phrase of search
terms rather than a byte-for-byte filename comparison.

The parser makes a best effort with incomplete input. An unclosed quote such as
`"desktop amd64` is still treated as a phrase. When results are unexpected, simplify
the query first, then add operators one at a time.

## Example: Ubuntu installation images

This query combines alternatives, phrases and an exclusion:

```text
("ubuntu 24.04.3" | "ubuntu 24.04.4") "desktop amd64" iso !downloads
```

![Two Ubuntu desktop installation images returned by the example search](../../../assets/screenshots/ubuntu-search.jpg)

On the instance shown, it returns two desktop ISOs whose infohashes match the
[official Ubuntu release torrents](https://releases.ubuntu.com/24.04/).
The exclusion removes names containing `downloads`; it does not verify the remaining
results. Verify a torrent against its publisher when provenance matters. Your instance
may return different results or none, depending on its indexed metadata.

## Normalisation and ordering

Search is case-insensitive. Both index text and query terms are transliterated for
matching, so accents do not require exact spelling. Transliteration is not translation:
it does not turn a query into equivalent words in another language.

Text matching and sorting are separate controls. Change the sort in the UI when the
same matches should appear in a different order. The query, sort, and facets are
represented in the URL, so a bookmark preserves the view.

## Missing results

Clear facets and try a simpler query. Check that the torrent has been discovered or
imported and that its classification job has completed. A metadata-only import has no
stored file list to search or inspect. Reprocessing changes classification and searchable
content; it does not fetch missing file metadata.

See [using the interface](../interface/) and [reprocessing](../reprocessing/).
