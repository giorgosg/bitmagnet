// Copy to config.js and point it at your bitmagnet instance. config.js is
// gitignored — nobody else's host belongs in the repository.
//
// The file is optional: without it Magnes falls back to bitmagnet's default
// address on this machine, http://localhost:3333/graphql.
//
// Whatever you set has to be reachable *from the browser*, not from wherever
// the dev server runs, since the page queries bitmagnet directly.

window.MAGNES_API_URL = "http://localhost:3333/graphql";
