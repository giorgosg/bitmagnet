# Private moderation review

Open **Moderation** from the Identity menu, or `/moderation` beneath the configured UI
mount. The route and menu both require `graphql:moderation:query`; ordinary catalogue
reading and editing do not admit this area. Each decision control additionally requires
`graphql:moderation:mutate`. The server enforces both Object actions on every request.
See [operator permissions and decisions](../../docs/moderation.md).

Pending, Allowed and Banned links select one decision list. Search matches a literal digest,
retained name or complete file path. Status, committed search text, the opaque continuation
cursor and the opened record are stored in the URL, including beneath a non-root mount.
Search pages request 50 summaries and replace the current page. First page, Next page and
browser history navigate the bounded list; no total count or catalogue-wide lookup is added.

Review details show the digest, status, decision origin, import provenance, display timestamp,
retained name, full paths, matching rule and decision history. History requests 50 events at
a time through **More history**. Missing evidence, names or file metadata are valid and shown
as unavailable. The page never reconstructs them from a digest. It offers no magnet or
content download controls. Filenames describe metadata and cannot verify file contents.

A moderator can flag a known catalogue infohash, allow or ban a pending record, reopen an
allowed/banned record, or change its decision. Each decision needs explicit confirmation.
**Purge evidence** explicitly confirms permanent removal of retained metadata and prior
private history; the decision remains, with its enforcement and provenance.

Allow restores catalogue visibility. The response says when normal classification is queued,
which does not mean classification has completed. A digest-only or purged record can be
allowed without a raw hash. The UI uses the retained evidence hash implicitly when available;
the API also accepts a separately known, validated hash, as described in the operator guide.
Local allows survive later automatic checks and imported bans until explicitly reopened or
banned. Ordinary category deletion remains independent.

The exact opaque `revision` is passed unchanged as `expectedRevision`; the millisecond
`DateTime` codec is used only to display `updatedAt`. Conflicts and refusals never display
success. A lost answer reports an unknown outcome. For record decisions, conflicts and lost
answers require **Refresh review** before
another decision. Successful flagging opens the returned record, including when the flag
started from a different detail page. Its URL, refresh and reload all address that record.

A lost flag response is separate from a decision on the currently opened record. Its requested
infohash is retained in the private review URL so refresh, navigation and reload keep the
unknown outcome visible. Refreshing another record cannot settle it. Opening retained evidence
for that exact infohash confirms the current review record without claiming the lost flag
response succeeded, and clears the uncertain target from the URL. Missing evidence cannot
settle this lookup. **Dismiss unknown flag warning**
explicitly removes the warning and says that dismissal does not confirm a saved flag.

Refresh advances the request epoch and clears the previous look. Leaving
the route or refreshing Identity clears private data and discards old answers. A history
page from a changed revision restarts history so events from different revisions are not
combined.

The Elm registry search found [jschomay/elm-paginate 3.1.2](https://package.elm-lang.org/packages/jschomay/elm-paginate/3.1.2/), whose Elm 0.19-compatible
[source](https://github.com/jschomay/elm-paginate/blob/3.1.2/src/Paginate.elm) pages a complete in-memory list and computes its total length. That does not fit the
API's opaque keyset cursor and absent count. Existing `elm/html`, `elm/url` and the locked
`elm-graphql` client provide the controls, routes and requests without a new dependency.

`e2e/credentialed/moderation.spec.js` drives the real UI and API against opt-in harmless
synthetic fixture records. Its workflow allows, explicitly reprocesses through the real
offline classifier/processor, searches ordinary and allowed results, then reopens and bans.
It also checks bounded search/history, digest-only and missing-file evidence, flagging,
permission refusals, stale decisions, failed searches, lost mutation answers and late
responses. It runs in the queue-changing project because allows add processor jobs.

## Sharing ban files

The moderation area offers **Export reviewed bans** and the separate deliberate
**Export reviewed and automatic bans** link only with `http:moderation:export`. Query or
mutate alone does not grant sharing. These download the whole eligible ledger snapshot,
independent of the selected review status, search or opened record. No state is changed.
Imported, pending and allowed decisions never appear; automatic inclusion retains its
origin. The server enforces the export Permission separately. See the
[frozen format, harmless fixture and membership-test privacy limit](../../docs/moderation-ban-file.md).

The links use the API URL followed by `/moderation/export`, within the existing `/graphql`
browser-cookie scope. The development proxy forwards that exact path too. The registry
search checked `elm/html` 1.0.1, including its Elm 0.19 constraint and `Html.Attributes`
source: the existing `download` attribute lets the browser stream an attachment directly.
`elm/file` 1.0.5 provides downloads from in-memory strings/bytes, which would buffer the
whole file in the UI; native links need no new dependency. The actual browser test reads
the downloaded reviewed and automatic JSON files and checks their exact public fields,
while a reviewer without export sees no links and receives HTTP 403 from the server.

**Select ban file** opens the native picker. **Import selected bans** explicitly uploads that
file to the API's `/moderation/import` path with `http:moderation:import`; entering this area
still needs private query Permission. The file's producer is trusted by this explicit action.
Imports add bans atomically, retain original origin/source, preserve local allows and never
withdraw entries absent from later files. Limits are 8 MiB, 10,000 entries and 256 UTF-8 source-label bytes. A server
acceptance reports distinct identifiers, including unchanged bans/allows, then refreshes the
private list. A refusal never shows success, and HTTP 403 refreshes Identity. Lost or unreadable
answers report an unknown outcome; inspecting decisions or retrying the same file is safe.
Navigation and Identity epochs discard stale answers and selected files.

The Elm registry search checked official `elm/file` 1.0.5 (`File.Select` and native picker
source) and `elm/http` 2.0.0 (`fileBody` and request source), both Elm 0.19 compatible and
already locked indirect dependencies. Promoting these to direct dependencies provides
native selection/upload without a custom JavaScript port or a new package. The development
proxy forwards the exact import path with Host and Origin preserved. The real browser suite
selects the frozen harmless fixture twice and a truncated file, and checks unknown outcomes,
late answers and Permission refusals.
