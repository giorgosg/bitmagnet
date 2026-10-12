# Magnes website

Public user documentation and the project homepage, built with Astro and Starlight.
Contributor architecture notes remain in `../docs/`; Elm development notes remain in
`../webui/docs/`. This site replaces the legacy Jekyll website.

## Develop

Use Node 22.12 or newer and npm 9.6.5 or newer:

```sh
cd website
npm ci
npm run dev
```

Open the printed URL under `/magnes/`. `npm run build` checks Astro/TypeScript, builds
static HTML and the search index, and verifies local links and assets under the Pages
base path. The check also rejects the footer edit links intentionally omitted from this site. `npm run preview` serves the production build.

Write pages in `src/content/docs/` with a title and description in frontmatter. Use
relative links between documents so the `/magnes/` prefix works on GitHub Pages.
`src/pages/schemas/classifier-0.1.json.ts` publishes the repository's existing generated
classifier schema from `../schemas/classifier-0.1.json`; regenerate it with
`task gen-classifier-schema` rather than editing it or maintaining a second copy.

Format supported files with the root repository's pinned Prettier. When editing `.astro`
files, use Astro's formatting tools separately; no `.astro` sources are needed yet.

## GitHub Pages

The Pages workflow builds pull requests without deploying them. Pushes and merges to
`trunk` that change the website, shared schemas, Markdown documentation, or the workflow
automatically rebuild and publish the site at `https://giorgosg.github.io/magnes/`.
Repository Pages uses **GitHub Actions** as its source. A manual workflow run on `trunk`
can also rebuild and deploy the current revision.

The workflow is restricted to `giorgosg/magnes`; forks can build without deploying to
this project's URL. Update the Astro `site` and `base`, the workflow repository guard,
and the production link checker together if the hosting address changes. A custom domain
will also need the base path and internal-link validation adjusted.

## Dependency note

The selector parser is overridden to `7.1.6` to pick up the CPU-exhaustion fix in
GHSA-rj75-hqrm-r3gf while the code-highlighting dependency still requests 6.x. The
production build exercises syntax highlighting; remove the override once the parent
dependency accepts a patched release.

## Editorial scope

This first edition covers installation from source, HTTPS and first login, the bundled
UI, text search, classification, reprocessing, imports, monitoring, configuration,
authentication, API endpoints, Servarr, backups, upgrades and troubleshooting.
The resource guide covers crawler concurrency, partial file retention, and recovery
after the classification queue reaches its threshold.
Examples retain the current `bitmagnet` executable and configuration names. Do not rename
those examples merely to match the product name.

Next: document release-image installation once its published tags are verified.

## UI screenshots

`src/assets/screenshots/` contains captures from the current UI, taken on 2026-10-12.
The search screenshot uses:

```text
("ubuntu 24.04.3" | "ubuntu 24.04.4") "desktop amd64" iso !downloads
```

The two results were checked against the info dictionaries in the `.iso.torrent`
files from [Ubuntu's release directory](https://releases.ubuntu.com/24.04/):

| Image                        | Infohash                                   |
| ---------------------------- | ------------------------------------------ |
| Ubuntu 24.04.3 desktop amd64 | `d160b8d8ea35a5b4e52837468fc8f03d55cef1f7` |
| Ubuntu 24.04.4 desktop amd64 | `01c137287d6f0ed05a56742dae794f632c79ff3d` |

The details screenshot shows the second image. Captures contain no server address or
account name. Metadata and result counts are examples, not promises of availability.
No torrent payloads were downloaded.

## Installation validation

Checked on 2026-10-12 against a fresh clone of `trunk` at `e95885db936e`, using
disposable PostgreSQL 16 databases and running each runtime check sequentially.

| Walkthrough step                                      | Result                                                                          |
| ----------------------------------------------------- | ------------------------------------------------------------------------------- |
| Clone `trunk` and `docker build -t magnes:local .`    | Passed; optimized Elm UI embedded                                               |
| Start the documented Compose stack                    | Passed; migration version 24, database and DHT healthy                          |
| Open `/` and `/ui/` through Caddy HTTPS               | Passed; redirect, assets and same-origin GraphQL work                           |
| Register with the bootstrap Invitation and sign in    | Passed in Chromium; administrator created, Secure/HTTP-only cookie set          |
| Search and reload                                     | Passed; authenticated search answered and session retained                      |
| Restart with a stable JWT secret                      | Passed; browser cookie remains valid                                            |
| Fresh anonymous catalogue access                      | Denied as documented                                                            |
| Invitation CLI after registration                     | Reports an existing administrator                                               |
| `nix develop --command task build` and native startup | Passed against another fresh database; UI, health and invitation retrieval work |
| Host Caddy forwarding client IP through Docker        | Passed after trusting the bridge gateway                                        |

The checks exposed and verified corrections for SELinux config-volume labelling,
host-proxy addresses across Docker NAT, and health checks racing application startup.
The installation and first-login guides include those corrections.

The UI toolchain's `npm audit` reported six high-severity development-dependency
advisories in the existing lockfile. `npm audit --omit=dev` reported none. Both builds
completed; these dependency updates remain separate work.

TLS checks used a local test CA. Public DNS, firewall configuration and ACME certificate
issuance for a real hostname were not tested. Disposable services, databases, credentials
and certificate trust were removed afterwards.

The backup guide's custom-format `pg_dump`, archive listing, empty-database creation,
and `pg_restore --exit-on-error` commands were also exercised on PostgreSQL 16 with a
small synthetic table. Both rows and its primary key were restored. This checks the
command sequence, not a full production-index restore or its duration. The isolated
test container was limited to 256 MiB and one CPU, and removed after the check.
