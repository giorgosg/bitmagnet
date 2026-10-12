// Copy to config.js only for a production static mount or another non-development
// arrangement. config.js is gitignored — nobody else's host belongs in the repository.
//
// The file is optional: without it Magnes uses same-origin /graphql. `npm run dev`
// proxies that path to the BITMAGNET_URL supplied to the development server.
//
// If bitmagnet is serving Magnes itself, via its http_server.static option,
// use a RELATIVE address instead — same origin, and no CORS involved:
//
//   window.MAGNES_API_URL = "/graphql";
//   window.MAGNES_BASE_PATH = "/magnes";
//
// Also set index.html's <base href> to the same mount path with a trailing
// slash (for example <base href="/magnes/" />), so assets load on deep links.

window.MAGNES_API_URL = "/graphql";
