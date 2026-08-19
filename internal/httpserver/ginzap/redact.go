package ginzap

import (
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
