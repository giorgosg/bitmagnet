# Data — models, queries, search, migrations

PostgreSQL only. There is no other supported store and no abstraction pretending
otherwise. `internal/database` is layered, and knowing which layer you are in decides
whether your change is safe.

## The layers

```
  internal/database/dao/     generated GORM Gen DAOs        ── `task gen-gorm`
  internal/database/query/   generic query builder: criteria, facets, options, hydrators
  internal/database/search/  bitmagnet's concrete criteria, facets, orderings
  internal/database/fts/     Postgres tsvector/tsquery construction and parsing
  internal/database/exclause/ CTE, UNION, INTERSECT, EXCEPT clauses GORM lacks
  internal/model/            the structs; `*.gen.go` are generated, the rest hand-written
```

`internal/model` is the layer everything else depends on. Note the split: `*.gen.go`
files are produced from the schema, and the hand-written neighbours (`null.go`,
`episodes.go`, `language.go`, `date.go`, the `*_enum.go` files) carry the custom
`Scan`/`Value`/GraphQL marshalling. `null.go` is 550 lines of nullable scalar types used
in every signature in the tree.

## Tables

| Table                                                                                  | Holds                                                     |
| -------------------------------------------------------------------------------------- | --------------------------------------------------------- |
| `torrents`                                                                             | infohash, name, size, private, files_status, files_count  |
| `torrent_files`                                                                        | one row per stored file, capped by `SaveFilesThreshold`   |
| `torrent_pieces`                                                                       | piece hashes, only when `SavePieces` is on                |
| `torrent_sources`                                                                      | the catalogue of sources (`dht`, importer names)          |
| `torrents_torrent_sources`                                                             | per-source seeders, leechers, published_at, import_id     |
| `torrent_hints`                                                                        | externally supplied classification hints from importers   |
| `torrent_tags`                                                                         | free-form tags                                            |
| `torrent_contents`                                                                     | the search table: classification + `tsv` full-text vector |
| `content`                                                                              | metadata records (TMDB etc.), with their own `tsv`        |
| `content_collections`                                                                  | genres, collections, and the join to `content`            |
| `queue_jobs`                                                                           | the job queue — see [processing.md](processing.md)        |
| `bloom_filters`                                                                        | the blocklist, stored as a Postgres large object          |
| `users`, `roles`, `role_permissions`, `api_keys`, `api_key_permissions`, `invitations` | auth — see [auth.md](auth.md)                             |

`torrent_contents.id` is _derived from the content identity_, not a surrogate key
(`InferID`). Re-classifying a torrent into a different match therefore produces a
different row, and the processor deletes the old one.

## Search

A search is assembled as `query.Option`s — criteria, facets, orderings, hydrators — and
run through the generic query in `query/query.go`. Three properties are worth knowing:

- **Facets** (`query/facets.go`, `search/facet_*.go`) run as additional aggregate queries
  in the same request. Language and content-type values use one grouped count when its
  planned cost fits the combined per-value aggregation budget; otherwise they use the
  estimate-capable per-value path. Other facets still count per value. All facet counts
  share an eight-slot limit so a search cannot take the whole database pool.
- **Full text** goes through `fts.AppQueryToTsquery`, which lexes the user's search string
  into a Postgres `tsquery` (`&`, `|`, `<->`, `!`, `:*`) and binds it as a **parameter**
  — `tsv @@ ?::tsquery`. Ranking uses `ts_rank_cd`.
- **Counting is budgeted.** `dao.BudgetedCount` asks Postgres to `EXPLAIN` the query
  first and, if the estimated cost exceeds a budget, returns the planner's row estimate
  rather than an exact count. This is what stops a broad search from sequentially scanning
  a 33-million-row table just to render "about N results". The plan and the exact count
  are separate parameterized queries, so each value stays bound through both operations.

The existence check and limited CTE item strategy also pass GORM subqueries with bound
parameters. Search no longer renders user values into SQL text. The historical
`budgeted_count(text, double precision)` function in `migrations/00010_budgeted_count.sql`
is unused by the application; it remains in existing databases for compatibility with
older instances during an upgrade. `search/injection_integration_test.go` checks search
strings, tag criteria and tag facet filters with both counting modes, including with
`standard_conforming_strings` off.

## Migrations

Goose SQL in [`migrations/`](../../migrations), embedded and run through
`internal/database/migrations` — an fx decorator, so they apply before anything queries.
Current high-water mark is `00022_auth.sql`; `ls migrations/` rather than trusting this
sentence.

**Forks number theirs independently and the numbers collide.** Anything cherry-picked gets
renumbered to the next free slot, and no number already applied to a live database is ever
reused. AGENTS.md Trap 3 has the rule.

```bash
task create-migration NAME=add_my_thing   # needs a goose binary; the Nix shell has none
task migrate                              # runs goose as a library, no binary needed
```

Run `task gen-gorm` afterwards if the change touches `internal/model`.

## Testing against a real database

`internal/database/dbtest` creates and drops an isolated database per test, by either of
two paths:

```go
db := dbtest.New(t)       // empty, fully migrated
db := dbtest.NewSeeded(t) // cloned from the fixture template, corpus included
```

Both hand back the same `*dbtest.DB` — `db.Gorm`, `db.Query`, `db.Pool`, `db.DSN`,
`db.Name` — and differ only in how the database was provisioned. `New` migrates from
empty, which is what the auth tests want. `NewSeeded` clones a seed template built by the
local btm-testdb project, so the database arrives populated for the cost of a file copy;
the template's name carries the migration version it was built at, and one that does not
match the migrations in this tree is refused rather than handed back.

Each path **skips silently** without its own variable — `TEST_POSTGRES_DSN`, which CI
sets, and `TEST_POSTGRES_TEMPLATE_DSN`, which only a checkout with fixtures has — so a
bare `go test ./...` proves considerably less than it appears to. See
[docs/hacking.md](../hacking.md).

---

_Known defects and improvement ideas referenced above are kept as untracked `docs/issues/*.local.md` notes, which a given checkout may or may not have._
