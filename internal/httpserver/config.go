package httpserver

type Config struct {
	LocalAddress string
	GinMode      string
	Cors         CorsConfig
	Static       StaticConfig
	Options      []string
	// TrustedProxies lists the CIDRs whose X-Forwarded-For and X-Real-IP headers
	// may be believed. Empty means believe nobody, and the client address is the
	// peer that actually opened the connection.
	//
	// Gin's own default is to trust every proxy, which makes the reported client
	// address a header the caller writes. Anything keyed by it — the login
	// throttle above all — is then keyed by a value the attacker chooses, and
	// bounds nothing. An operator running behind a reverse proxy has to name it
	// here for the real client address to survive the hop.
	TrustedProxies []string
}

type CorsConfig struct {
	// AllowedOrigins is a list of origins a cross-domain request can be executed from.
	// If the special "*" value is present in the list, all origins will be allowed.
	// An origin may contain a wildcard (*) to replace 0 or more characters
	// (i.e.: http://*.domain.com). Usage of wildcards implies a small performance penalty.
	// Only one wildcard can be used per origin.
	// Default value is ["*"]
	AllowedOrigins []string
	// AllowedMethods is a list of methods the client is allowed to use with
	// cross-domain requests. Default value is simple methods (HEAD, GET and POST).
	AllowedMethods []string
	// AllowedHeaders is list of non simple headers the client is allowed to use with
	// cross-domain requests.
	// If the special "*" value is present in the list, all headers will be allowed.
	// Default value is [].
	AllowedHeaders []string
	// ExposedHeaders indicates which headers are safe to expose to the API of a CORS
	// API specification
	ExposedHeaders []string
	// MaxAge indicates how long (in seconds) the results of a preflight request
	// can be cached. Default value is 0, which stands for no
	// Access-Control-Max-Age header to be sent back, resulting in browsers
	// using their default value (5s by spec). If you need to force a 0 max-age,
	// set `MaxAge` to a negative value (ie: -1).
	MaxAge int
	// AllowCredentials indicates whether the request can include user credentials like
	// cookies, HTTP authentication or client side SSL certificates.
	AllowCredentials bool
	// AllowPrivateNetwork indicates whether to accept cross-origin requests over a
	// private network.
	AllowPrivateNetwork bool
	// OptionsPassthrough instructs preflight to let other potential next handlers to
	// process the OPTIONS method. Turn this on if your application handles OPTIONS.
	OptionsPassthrough bool
	// Provides a status code to use for successful OPTIONS requests.
	// Default value is http.StatusNoContent (204).
	OptionsSuccessStatus int
	// Debugging flag adds additional output to debug server side CORS issues
	Debug bool
}

// StaticConfig mounts a directory of static files, for serving a web UI that is
// not the bundled one. The bundled UI is compiled into the binary and cannot be
// replaced without rebuilding it; this is the seam for an alternative.
type StaticConfig struct {
	// Dir is the directory to serve. Empty - the default - disables the mount
	// entirely, which is why an absent directory is not an error and a configured
	// one that does not exist is.
	Dir string
	// Path is where it mounts. It cannot be "/", because the bundled web UI already
	// redirects the root, and two options claiming the same route make gin panic at
	// startup rather than report a conflict.
	Path string
}

func NewDefaultConfig() Config {
	return Config{
		LocalAddress: ":3333",
		GinMode:      "release",
		Cors: CorsConfig{
			// AllowedOrigins stays permissive: the bundled web UI is served from the
			// same origin and needs no CORS at all, but serving it from a separate
			// origin is a supported deployment, and narrowing this would break those
			// silently. Tightening it is an operator decision - see docs/auth.md.
			AllowedOrigins: []string{"*"},
			// Only the headers the server actually reads. "*" reflected back whatever
			// a caller asked for, which grants more than anything here needs.
			// Literals rather than the constants that define them: those live in
			// packages that import this one.
			AllowedHeaders: []string{
				"Content-Type",
				"Authorization", // http_auth.AuthorizationHeader
				"X-Api-Key",     // torznab api key header
				"X-Import-Id",   // importer/httpserver.ImportIDHeader
			},
		},
		Static: StaticConfig{
			Path: "/ui",
		},
		Options: []string{"*"},
	}
}
