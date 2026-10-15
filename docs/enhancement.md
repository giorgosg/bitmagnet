# Metadata enhancement

`bitmagnet enhancement evaluate` reads selected visible torrents, evaluates their
metadata through an explicitly configured OpenAI-compatible Chat Completions endpoint,
and prints one JSON report. It does not apply metadata or save evaluation state.
Automatic enhancement is unavailable; no enhancement worker runs.

Configure the existing `postgres` and `tmdb` sections as usual. The database must
already be migrated. This command opens a separate PostgreSQL connection with
`default_transaction_read_only=on`, loads evidence in a read-only repeatable-read
transaction, rolls it back, and then evaluates outside the transaction. It never
resolves the application's migration-decorated database or starts workers. Pending
quarantined and banned hashes are excluded from explicit selections and samples.

For example, with the secret supplied in the named environment variable:

```bash
bitmagnet enhancement evaluate \
  --hash 0123456789abcdef0123456789abcdef01234567 \
  --base-url https://your-provider.example/v1 \
  --model your-model \
  --api-key-env LLM_API_KEY \
  --run-max-cost 0.25 \
  --input-cost-per-million 1 \
  --output-cost-per-million 2 > evaluation.json
```

The prices above illustrate configuration, not a provider price quote. Supply
conservative prices for the chosen model, including billed reasoning output. The
positive run spending ceiling and both positive token prices are required. The
currency unit is the unit used for those prices. Cost is a token-price estimate;
it does not include other provider fees. There is no provider/model fallback.
The model must support Chat Completions function tools. `output_token_parameter`
(default `max_completion_tokens`) selects the explicit request cap field; set
`--output-token-parameter max_tokens` for providers that require that spelling.
Both spellings must bound all billed output, including reasoning, for the chosen
provider/model. There is no automatic parameter retry or fallback.
Provider incompatibility is reported as a failure. Compatible reasoning replay fields
remain in memory only for the bounded conversation and are not returned in reports.
For providers supporting `thinking.type`, `--thinking disabled` or `--thinking enabled`
sets that generation option explicitly; the default omits it. OpenAI-compatible
providers vary in these extensions, so choose the option their API documents.

Repeat `--hash` for explicit selections, or use `--sample 10` for a deterministic
sample ordered by infohash. Choose one selection mode, with at most 100 torrents.
An explicit experiment always runs again: no saved fingerprint or previous answer
suppresses it. `--model`, `--base-url`, `--temperature`, and `--prompt-file` override
configuration for comparison. Prompt files add operator instructions; they cannot
relax local output validation. Save reports yourself when comparing experiments.
Application shutdown, including SIGINT handled by fx, cancels this command's
in-flight HTTP work and waits for its bounded result reporting; canceled work
cannot become an accepted late result.

All settings also live under the `enhancement` configuration section, using the
usual snake-case configuration names. Defaults are:

| Setting                   | Default                  |
| ------------------------- | ------------------------ |
| `concurrency`             | 2 evaluations            |
| `max_requests`            | 4 per evaluation         |
| `max_tool_calls`          | 6 per evaluation         |
| `timeout`                 | 2 minutes per evaluation |
| `request_output_tokens`   | 2,000 per request        |
| `max_output_tokens`       | 8,000 per evaluation     |
| `max_run_requests`        | 40 per command           |
| `max_run_output_tokens`   | 80,000 per command       |
| `run_timeout`             | 10 minutes per command   |
| `max_run_cost`            | No default; required     |
| `input_cost_per_million`  | No default; required     |
| `output_cost_per_million` | No default; required     |

CLI run-limit flags use `--run-max-requests`, `--run-max-output-tokens`,
`--run-timeout`, and `--run-max-cost`; evaluation flags use the corresponding
hyphenated setting names. `base_url`, `api_key`, `model`, `temperature`, `prompt`
and `tags` configure the provider, additional instructions and tag whitelist.
By default the whitelist permits `llm-platform-windows`, `llm-platform-linux`,
`llm-platform-macos`, `llm-platform-android`, `llm-platform-ios`,
`llm-edition-deluxe`, and `llm-edition-collectors`. Tags must use lowercase
kebab-case, the `llm-` prefix and at most 30 characters.

Spending, request and output reservations are shared by concurrent evaluations in
this command only. They do **not** enter a database-backed shared budget ledger.
Running several commands multiplies these allowances. Each request reserves its
maximum output and a conservative input allowance before contacting the provider.
Successful responses require explicit nonnegative `prompt_tokens` and
`completion_tokens` counters before reconciling reservations. Missing/null counters
are provider failures, rather than zero-token completions. Unknown usage
retains the full reservation and sets `usage.estimated`; its cost is an upper-bound
estimate of both input and output tokens and their cost, including reservations
from a later failed turn after earlier billed calls. `completion_tokens` includes reasoning, so reasoning is reported separately
and is not added twice. Transient transport/429/5xx failures use backoff and at most
three attempts total; malformed output (including invalid tool names/arguments) gets at most one retry
within that ceiling. Tool batches are validated before any catalogue call.
Every retry consumes request, token, spending and time limits.

Both read-only experiments and persistent evaluation use the durable ordinary snapshot
when present, including inferred title/year, without reading prior enhancement as ordinary
classification. Legacy rows without a snapshot use existing classification. Both paths
select the same representative files; read-only loading remains bounded and performs no
state writes.

Evidence contains the name, size/count, ordinary classification, supplied hints and
at most twenty representative stored paths/sizes, favoring large relevant files and
directory diversity. Missing/truncated lists are explicitly marked. Names and paths
are bounded; content bytes and NFO text are never available. Each request, including
tool definitions and accumulated search summaries, is limited to 8,000 UTF-8 bytes.
This deliberately conservative input bound stays below the 8,000-token target without
assuming the runtime model's tokenizer; a long conversation can become `limited`.

The only model-facing tool is `search_works(title, type, year?)`, backed by TMDB's
existing movie/TV search client. It returns at most ten short summaries and only
movie or series IDs. A TV episode/season filename year is not automatically used as
a premiere filter. Search summaries omit original language, and work language never
becomes a release-track language automatically. The model may refine searches within
the limits. A selected identity must have been retrieved in this evaluation; mixed
works cannot attach one identity. Unknown fields, invalid enums/ranges, invented IDs,
confidence/justification fields and non-whitelisted tags fail local validation.

The report contains model, accounting scope, each infohash, latency, usage, optional
cost and one outcome:

- `accepted`: validated, nonempty metadata for inspection.
- `unresolved`: a completed empty metadata answer.
- `limited`: request, token, tool, spending, input, cancellation or deadline limit.
- `malformed`: invalid structured output after its permitted retry.
- `provider_failure`: transport, authentication, catalogue or usage failure.

Accepted metadata is still a model inference for manual evaluation, not proof of
correctness. This command supplies the engine for later quality measurements; no
live model-quality claim follows from deterministic tests passing.

The transport follows the official [Chat Completions API](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)
, the [DeepSeek compatible parameter reference](https://api-docs.deepseek.com/api/create-chat-completion/),
and TMDB's [search workflow](https://developer.themoviedb.org/docs/search-and-query-for-details).

## Durable application for internal callers

`enhancement.Application.Enhance` is the explicit single-torrent service entry point.
It requires a real completed ordinary classification for current evidence, runs the
bounded evaluator, fetches selected TMDB work details outside the application transaction,
and rechecks freshness, authority and moderation before writing. The application
transaction follows the original run deadline and cancellation through lock waits,
regardless of the completion caller context, and checks again before committing. It does not run on its
own. Dedicated applying CLI commands, scheduling and shared automatic budgets are not
yet available; `enhancement evaluate` remains read-only even with this storage present.

Ordinary enhancement may correct unsourced rule results. Importer hints and existing
catalogue identity are protected; a type-only hint permits adding a compatible absent
catalogue identity. This includes a work attached by an earlier enhancement: force
alone cannot replace or drop it, and a later fields-only result retains it. The retained
identity lives with accepted state so reconstruction also works after search-row deletion.
An explicit rematch request may replace supplied values or remove a work match.
Stored hint title/year now participate in ordinary classification. Accepted inferred
title/year remains searchable and displayable without a catalogue match. Catalogue
values take precedence in display, Release Year facets and Torznab. Original torrent
names remain available and are retained in magnet names.

Accepted results replay during ordinary reconstruction only while their evidence is
valid, including ordinary inferred title/year. Changing those ordinary values invalidates
prior output and unchanged-input suppression; replayed enhancement values never become
their own ordinary evidence. Catalogue identity remains protected when changed ordinary
evidence invalidates release-level output; explicit ordinary rematch can establish new
identity, including an absent match, independently of whether ordinary evidence changes.
Valid release fields still replay, but cannot restore the prior identity proposal. When
changed evidence invalidates release fields, reconstruction rebuilds the search vector
from the retained catalogue work. Failed/malformed later attempts retain valid accepted
output and remain retryable.
Completed unresolved and limited results suppress unchanged input/recipe; explicit force,
rematch or a changed recipe/evidence can reconsider them. These records are backed up
with the database; catalogue exports omit evaluation/recipe internals. A search-vector
rebuild does not count as evaluation. Existing torrents acquire ordinary completion
records when genuinely classified, never from a fabricated migration backfill.

Recipes store sanitized canonical JSON excluding API credentials, prices and operational
concurrency/run bounds. They include the requested endpoint/model/generation settings,
prompt, controlled tags, implementation revision and ordinary workflow recipe. Hosted
model aliases may change behind the same requested recipe. The reserved generated tag
set is replaced atomically while user/importer tags outside it survive. No year tags
are generated. All processing remains explicitly invoked; nothing is enabled or deployed
by migrating the database.
