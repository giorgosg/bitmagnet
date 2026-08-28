package httpserver

// Config gates the two developer-facing halves of the GraphQL surface. Both
// default to off: they are reconnaissance rather than access, but the schema and
// the playground are of no use to a deployed client, and per docs/auth.md an
// instance may be reachable anonymously.
type Config struct {
	// Introspection enables `extension.Introspection`, which answers `__schema`
	// and `__type` queries. The web UI's client is generated from the schema
	// files at build time, so nothing shipped needs this at runtime.
	Introspection bool
	// Playground serves the GraphiQL page on GET /graphql. With it off the route
	// is not registered at all, so the path 404s.
	Playground bool
}

func NewDefaultConfig() Config {
	return Config{
		Introspection: false,
		Playground:    false,
	}
}
