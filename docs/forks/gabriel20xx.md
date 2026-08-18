# gabriel20xx/Bitmagnet

<https://github.com/gabriel20xx/Bitmagnet> · remote `gabriel20xx` · branch `main`

**86 unique commits, 0 missing from upstream.** Active Jul 2026.
Real diff: 226 Go files / 715 files total. Go module path unchanged.

## The headline: the web UI was rewritten in React

`webui/package.json` declares `react ^19.2.7` — upstream is Angular 18. There is a
`webui/src/features/` tree with `.tsx` components.

Relevant mostly as a warning: it cannot be combined with any other fork's UI work. The
useful side effect is that swapping frontends forced a clean API boundary, so the
GraphQL schema additions are self-contained and portable.

## Auth — the reason to look here

The most self-contained auth implementation available. See [../auth.md](../auth.md) for
the full comparison against `upstream/next` and rolling your own.

**Go side** (5 files, ~small):

```
internal/auth/config.go      context.go    factory.go
internal/auth/service.go     service_test.go
internal/auth/authfx/module.go
internal/gql/gqlmodel/auth.go
migrations/00033_auth.sql
```

**GraphQL surface:**

```graphql
directive @authenticated on FIELD_DEFINITION

type AuthQuery {
  status: AuthStatus!
}
type AuthMutation {
  createInitialUser(input: CreateInitialUserInput!): AuthResult!
  login(input: LoginInput!): AuthResult!
  logout: Boolean!
  updateCredentials(input: UpdateCredentialsInput!): AuthResult!
}
type AuthStatus {
  setupRequired: Boolean!
  authenticated: Boolean!
  user: AuthUser
}
```

**Schema** (`migrations/00033_auth.sql` — renumber, upstream is at `00020`):

- `auth_users` — bcrypt `password_hash`, with a deliberate singleton constraint:
  ```sql
  create unique index auth_users_singleton_idx on auth_users ((true));
  ```
  The migration comment explains this is intentional — race-safe initial setup, and
  removable later without a schema change if multi-user is wanted.
- `auth_sessions` — `token_hash` (hashed, not stored raw), `expires_at`, cascade delete,
  indexes on expiry and user.

Implementation uses `golang.org/x/crypto/bcrypt`, `crypto/rand` for token generation,
and sha256 for token hashing. Has a `service_test.go`. Follows the standard fx module
layout, so it drops into `appfx/module.go` with one line.

**Assessment:** small, correct-looking, and idiomatic for this codebase. Single-user
session auth with a `setupRequired` first-run flow — appropriate for a self-hosted app.
Not a full RBAC system, which is a feature if you want something you can actually read.

## Other backend work

- **Torrent file reconstruction and download endpoint** — rebuild a `.torrent` from
  stored metadata. Genuinely interesting, independent of the UI.
- **qBittorrent integration** — connection testing, API key support, integration status
- **Fuzzy search with a plain-text companion column** — search quality
- Database statistics query + diagnostics (slow queries, table scan stats)
- DAO methods supporting unscoped queries and raw SQL row retrieval
- Media streaming with text file previews
- Metadata timeout handling
- Postgres config via `POSTGRESQL_URL`
- Dockerfile cleanup: healthcheck, specific binary copy, default CMD; goreleaser `goarm` fix
- Quicktest → Testify in DHT message tests

## Caveats

**Commit hygiene is poor.** Several commits are titled just `Update`, `Overhaul`, or
`Remove licenses`; one commit message concatenates five unrelated changes. Review by
diff, not by commit log — `git log` will not tell you what happened.

Also removed the `licenses/` directory and changed `.gitignore` to stop tracking
`webui/dist` — check what else got swept up.

## Assessment

**Take the auth module and the torrent-file reconstruction endpoint; ignore the React
rewrite.** The auth work is the best-scoped implementation of the three options and the
one you can realistically read end to end before trusting it with your instance.
