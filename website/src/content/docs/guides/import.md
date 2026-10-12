---
title: Import torrent metadata
description: Load an existing catalogue through the authenticated import endpoint.
---

`POST /import` accepts newline-delimited JSON: one torrent metadata object per line.
It does not accept a JSON array, a multipart upload, or a raw `.torrent` file. Convert
your source catalogue to this format before sending it.

Use an API key whose owning User and key Permissions include `http:import:mutate`.
The HTTP component is named `import`; the default HTTP options include it.
Configure your own TMDB key or disable TMDB before queueing a large classification load.

## Prepare records

Each record should include a source name, valid 40-character hexadecimal infohash,
torrent name, and size in bytes. This is an illustrative record; replace the synthetic
infohash and size with the real metadata from your source:

```json
{
  "source": "my-catalogue",
  "infoHash": "0123456789abcdef0123456789abcdef01234567",
  "name": "Public-domain collection",
  "size": 1048576
}
```

The source becomes a catalogue source you can filter on. Additional supported fields
include `private`, `contentType`, `contentSource`, `contentId`, `title`, `releaseDate`,
`releaseYear`, `episodes`, video attributes, `releaseGroup`, and `publishedAt`.
Dates and enum values must use the formats accepted by the deployed version; the
[import Item definition](https://github.com/giorgosg/magnes/blob/trunk/internal/importer/importer.go)
and model types are the current contract.

The endpoint has no complete public input schema. Test a small sample and inspect the
result before converting a whole catalogue. Unknown JSON fields are not a reliable
validation signal: the decoder can ignore them. In particular, this endpoint does not
import file lists, and the crawler may supply those later if it encounters the torrent.

## Send the import

Save the converted records as `torrents.ndjson`, with a final newline. Send them with
`Content-Type: application/json`, despite the line-oriented body. The endpoint requires
that media type. The following example reads the API key from a private file and passes
it to curl through stdin, avoiding a literal key in command history or curl's arguments:

```sh
{
  printf 'header = "Authorization: Bearer '
  tr -d '\r\n' < /secure/path/import-api-key
  printf '"\n'
} | curl --config - --fail-with-body --no-buffer \
  -H 'Content-Type: application/json' \
  -H 'X-Import-Id: catalogue-2026-10' \
  --data-binary @torrents.ndjson \
  https://magnes.example.com/import
```

Replace the URL, key-file path and import ID. Store the file with access limited to its
owner. `X-Import-Id` associates an identifier with this import; it is not a transaction
or a promise that retrying cannot change data. Without it, the server uses a timestamp.

## Check the outcome

The response reports item counts and ends with `import complete` on success. Importing
is buffered and can partially persist data before an error. On a long stream, progress
may already have sent the HTTP headers before a later error, so a successful HTTP status
alone is not proof that the whole import finished. Read the response and backend logs.

Imported items enter the classification queue. They become searchable after processing.
Watch Queue jobs, then verify a few known names and source filters in the UI. Split large
inputs into manageable files and keep the converted source so you can diagnose failed
records without rerunning an entire import.
