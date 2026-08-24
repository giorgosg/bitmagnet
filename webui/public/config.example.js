// Copy to config.js and point it at your bitmagnet instance. config.js is
// gitignored — nobody else's host belongs in the repository.
//
// The file is optional: without it Magnes falls back to bitmagnet's default
// address on this machine, http://localhost:3333/graphql.
//
// Whatever you set has to be reachable *from the browser*, not from wherever
// the dev server runs, since the page queries bitmagnet directly.
//
// If bitmagnet is serving Magnes itself, via its http_server.static option,
// use a RELATIVE address instead — same origin, and no CORS involved:
//
//   window.MAGNES_API_URL = "/graphql";
//   window.MAGNES_BASE_PATH = "/magnes";
//
// Also set index.html's <base href> to the same mount path with a trailing
// slash (for example <base href="/magnes/" />), so assets load on deep links.

window.MAGNES_API_URL = "http://localhost:3333/graphql";
