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
  in the same request. Each facet is a separate scan.
- **Full text** goes through `fts.AppQueryToTsquery`, which lexes the user's search string
  into a Postgres `tsquery` (`&`, `|`, `<->`, `!`, `:*`) and binds it as a **parameter**
  — `tsv @@ ?::tsquery`. Ranking uses `ts_rank_cd`.
- **Counting is budgeted.** `dao.BudgetedCount` asks Postgres to `EXPLAIN` the query
  first and, if the estimated cost exceeds a budget, returns the planner's row estimate
  rather than an exact count. This is what stops a broad search from sequentially scanning
  a 33-million-row table just to render "about N results".

The budgeted count and `genericQuery.checkExists` both work by **rendering the built query
back to a SQL string** with `gorm.DB.ToSQL` and re-executing that string — in the budgeted
case by passing it as text to a plpgsql function that `EXECUTE`s it
(`migrations/00010_budgeted_count.sql`). This is upstream's design. It is the single most
structurally risky thing in the codebase and is written up in
the issue notes below; read them before extending the pattern.

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

`internal/database/dbtest` creates and drops a fully migrated, isolated database per test:

```go
db := dbtest.New(t) // db.Gorm, db.Query, db.Pool, db.DSN, db.Name
```

These tests **skip silently** when `TEST_POSTGRES_DSN` is unset, so a bare `go test ./...`
proves considerably less than it appears to.

---

_Known defects and improvement ideas referenced above are kept as untracked `docs/issues/*.local.md` notes, which a given checkout may or may not have._
