# Deploy browser UIs separately

The fork ships the Go backend, GraphQL and Torznab APIs without an embedded Angular UI.
Browser clients are maintained and deployed in their own projects. This removes the
transitional Angular authentication screens, committed bundles and frontend generators
from the backend release cycle. The GraphQL schema and browser session cookie contract
remain the integration boundary.

Operators can serve a built UI through `http_server.static.dir` (default path `/ui`),
or serve it behind the same reverse proxy as the API. `/webui` is available for a
configured static mount. The root remains unavailable for a static mount because Gin's
wildcard route conflicts with the API routes; there is no implicit root redirect.

Cross-origin browser reads are denied by default. The removed Angular development server
was the in-tree caller relying on wildcard CORS. Other origins require an explicit
`http_server.cors.allowed_origins` allowlist. An empty list must install a deny callback,
because the CORS library treats an empty list as allow-all. Non-browser clients and
same-origin clients continue to use the existing API.

Repository formatting retains Prettier 3.3.3 in the root tooling package, independently
of any frontend. Node remains in the development shell for that formatter; Angular,
Chromium and the frontend build and generation tasks are removed.
