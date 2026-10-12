# Magnes

A self-hosted BitTorrent indexer, DHT crawler, content classifier and torrent search engine with a GraphQL API and Servarr stack integration. This fork bundles the [Magnes Elm UI](webui/README.md) at `/ui/`, replacing Angular.

> This is an LLM-assisted integration fork. Its purpose is to evaluate focused changes from
> downstream repositories, validate them with regression tests and CI, and merge accepted work
> into the `trunk` branch while keeping `main` as a pristine upstream mirror.

Read the [Magnes documentation](https://giorgosg.github.io/magnes/documentation/) for
installation, everyday use, configuration and maintenance. The homepage and docs source
live in [website/](website/README.md). Run `task serve-website` to preview them locally.

Working on the code: [AGENTS.md](AGENTS.md) for the rules, [docs/](docs/README.md) for the
notes behind them.

`task build` builds the backend and UI into one executable. Start the HTTP worker with
`./bitmagnet worker run --keys=http_server` and open `/ui/`; `/` redirects there. Browser
sign-in requires HTTPS through a reverse proxy. See [docs/hacking.md](docs/hacking.md)
for development and [docs/auth.md](docs/auth.md#ui-deployment-and-cors) for deployment.
