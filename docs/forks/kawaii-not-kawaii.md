# kawaii-not-kawaii/bitmagnet

<https://github.com/kawaii-not-kawaii/bitmagnet> · remote `kawaii-not-kawaii` · branch `main`

**340 unique commits, 0 missing from upstream.** Active through 2026-08-02.
Real Go diff: 157 files. Go module path unchanged.

By far the largest body of coherent work built on the upstream architecture. Development
is disciplined — numbered PRs merged into their own `main`, conventional commits,
descriptive messages ("stop the recommendation vanishing 3.2s after it appears").

## New subsystems

Two new top-level packages that don't exist upstream:

- **`internal/llm`** (21 files) — LLM-based content classification
- **`internal/client`** (9 files) — HTTP client abstraction supporting it

Plus substantial changes to `internal/classifier` (34 files) and `internal/gql` (31 files)
to wire LLM classification into the existing workflow engine and expose it over GraphQL.

## Authentication

The later `auth-v2` work is a third implementation worth studying alongside
`upstream/next` and gabriel20xx; see [../auth.md](../auth.md). It adds:

- username/password setup and login with signed browser sessions;
- a machine API key accepted by GraphQL and Torznab (`apikey` or `X-Api-Key`);
- trusted-network bypass with an explicit trusted-proxy list;
- HTTP, middleware, session, and Torznab authorization tests.

It is not an isolated module. Credentials are persisted through this fork's live config
writer, the implementation lives under `internal/gql/auth`, and the browser flow is tied
to its Angular UI. Treat it as a design and test source for a focused backend port, not a
standalone cherry-pick.

## Themes

### LLM classification

- Classification via LLM providers as an alternative/supplement to the rule engine
- `fix(llm): tolerate markdown-fenced JSON from providers` — the classic failure mode
- Recommend a complete config and expose effective concurrency
- A paginated LLM classifications feed in the UI

### Web UI

Angular upgraded **18 → 21**, plus a design refresh delivered as numbered PRs:

- LLM config dashboard — recommendation, presets, live capacity
- Grade swarm health on search rows
- Offer the unclassified bucket on search
- Make the LLM tab usable on a narrow viewport
- `fix(webui): make copy magnet work outside a secure context` — useful on plain HTTP LAN
- Keep the requested route through the login redirect (implies some auth exists)

### Fixes worth extracting independently

- `fix(reprocess): scope --contentType null to unclassified torrents` — sounds like a
  genuine correctness bug in the reprocess command
- `fix(webui): make copy magnet work outside a secure context`

## Assessment

Interesting but **not a cherry-pick target**. The LLM work is 340 commits deep and
threaded through the classifier and GraphQL layers; taking it means taking it as a unit,
along with a new external-service dependency and its config surface.

Relevance depends on whether LLM classification is something you want at all. If it is,
this is a serious implementation and the right starting point. If not, there are only a
few isolated fixes worth extracting.

If you're replacing the frontend, the Angular 21 upgrade is irrelevant and their
backend/GraphQL changes are the only portable part — but those are entangled with the
LLM feature.

**Defer the LLM and Angular work.** Revisit it once the base is stable. Evaluate the auth
backend separately because it covers the Torznab API-key gap left by gabriel20xx's smaller
session implementation.
