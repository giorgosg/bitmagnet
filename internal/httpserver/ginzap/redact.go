package ginzap

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// redactedValue replaces a sensitive query parameter's value in logs.
const redactedValue = "[redacted]"

// sensitiveQueryParams are parameters whose values are credentials.
//
// The Torznab protocol carries its API key in the query string, so it lands in
// the request line. API keys do not expire by default, which would make read
// access to a log file equivalent to application access.
//
// This only covers this application's own logging. Anything else in the request
// path — a reverse proxy, an ingress controller, a CDN — records the URL too,
// and must be configured separately; see docs/auth.md.
var sensitiveQueryParams = map[string]struct{}{
	"apikey":   {},
	"api_key":  {},
	"password": {},
	"secret":   {},
	"token":    {},
}

// redactQuery rewrites a raw query string, replacing the values of sensitive
// parameters. Parameter order and unaffected values are preserved so the logs
// stay useful. A query that cannot be parsed is redacted wholesale rather than
// logged raw, since a credential may be hiding in whatever made it unparseable.
func redactQuery(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}

	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return redactedValue
	}

	sensitive := false

	for key := range values {
		if _, ok := sensitiveQueryParams[strings.ToLower(key)]; ok {
			sensitive = true

			break
		}
	}

	if !sensitive {
		return rawQuery
	}

	pairs := strings.Split(rawQuery, "&")
	out := make([]string, 0, len(pairs))

	for _, pair := range pairs {
		key, _, found := strings.Cut(pair, "=")

		decodedKey, err := url.QueryUnescape(key)
		if err != nil {
			decodedKey = key
		}

		if _, ok := sensitiveQueryParams[strings.ToLower(decodedKey)]; ok && found {
			out = append(out, key+"="+redactedValue)

			continue
		}

		out = append(out, pair)
	}

	return strings.Join(out, "&")
}

// sensitiveHeaders are headers whose values are credentials. Torznab accepts
// X-Api-Key as an alternative to the query parameter, and Cookie carries
// whatever the browser holds, so a request dump leaks through them just as
// readily as through the URL.
var sensitiveHeaders = map[string]struct{}{
	"authorization":       {},
	"proxy-authorization": {},
	"cookie":              {},
	"x-api-key":           {},
}

// redactRequestURI rewrites a request-line URI so that its query string carries
// no credentials. Splitting the raw string is deliberate: it is what
// httputil.DumpRequest writes, so redacting a reconstruction of it would leave
// the original in the log.
func redactRequestURI(requestURI string) string {
	path, rawQuery, found := strings.Cut(requestURI, "?")
	if !found {
		return requestURI
	}

	return path + "?" + redactQuery(rawQuery)
}

// dumpRequest renders a request for a panic log with its credentials removed.
//
// httputil.DumpRequest writes the request line verbatim from RequestURI and
// every header as it stands, which is how a Torznab apikey — a credential that
// travels in the query string and does not expire — reaches a log file that the
// normal request logging is careful to keep it out of. gin's own recovery
// middleware masks the Authorization header and nothing else, and it prints the
// dump on the broken-pipe path regardless of release mode.
func dumpRequest(req *http.Request) []byte {
	if req == nil {
		return nil
	}

	redacted := req.Clone(req.Context())
	redacted.RequestURI = redactRequestURI(req.RequestURI)

	if redacted.URL != nil {
		redacted.URL.RawQuery = redactQuery(redacted.URL.RawQuery)
	}

	for name := range redacted.Header {
		if _, ok := sensitiveHeaders[strings.ToLower(name)]; ok {
			redacted.Header.Set(name, redactedValue)
		}
	}

	dump, err := httputil.DumpRequest(redacted, false)
	if err != nil {
		// Never fall back to the raw request: the reason to dump it at all is
		// weaker than the reason not to leak what it carries.
		return nil
	}

	return dump
}
