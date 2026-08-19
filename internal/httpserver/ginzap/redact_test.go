package ginzap

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRedactQuery(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"nothing sensitive", "t=caps&cat=2000", "t=caps&cat=2000"},
		{
			name: "torznab api key",
			in:   "t=caps&apikey=3kPtrZwj1Y2h8PppoEXw0o",
			want: "t=caps&apikey=[redacted]",
		},
		{
			name: "key first, order preserved",
			in:   "apikey=secretvalue&t=search&q=x",
			want: "apikey=[redacted]&t=search&q=x",
		},
		{"case insensitive", "APIKey=abc&t=caps", "APIKey=[redacted]&t=caps"},
		{"underscore variant", "api_key=abc", "api_key=[redacted]"},
		{"token", "token=abc&x=1", "token=[redacted]&x=1"},
		{"password", "password=hunter2", "password=[redacted]"},
		// A bare key with no value carries nothing to leak.
		{"valueless", "apikey&t=caps", "apikey&t=caps"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, redactQuery(tt.in))
		})
	}
}

// Anything unparseable is dropped entirely rather than logged raw, since a
// credential may be hiding in whatever made it unparseable.
func TestRedactQueryRedactsUnparseableWholesale(t *testing.T) {
	t.Parallel()

	assert.Equal(t, redactedValue, redactQuery("%zz=broken&apikey=leak"))
}

// The redaction must never leak the value it was given.
func TestRedactQueryNeverEmitsTheSecret(t *testing.T) {
	t.Parallel()

	const secret = "3kPtrZwj1Y2h8PppoEXw0o"

	for _, q := range []string{
		"apikey=" + secret,
		"t=caps&apikey=" + secret,
		"apikey=" + secret + "&t=caps",
		"APIKEY=" + secret,
	} {
		assert.NotContains(t, redactQuery(q), secret, "query %q leaked its credential", q)
	}
}
