package json_schema_test

import (
	"testing"

	"github.com/bitmagnet-io/bitmagnet/pkg/json_schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// A JSONValue represents a value, not a document. yaml.Unmarshal into a yaml.Node
// always yields a DocumentNode wrapper, so the constructors have to unwrap it —
// otherwise values built here compare unequal to the scalar nodes produced by
// encoders elsewhere, even when they denote the same value.
func TestNewValueUnwrapsDocumentNode(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		value   any
		wantKtd yaml.Kind
		wantTag string
		wantVal string
	}{
		{"int", 2, yaml.ScalarNode, "!!int", "2"},
		{"string", "hello", yaml.ScalarNode, "!!str", "hello"},
		{"bool", true, yaml.ScalarNode, "!!bool", "true"},
		{"slice", []int{1}, yaml.SequenceNode, "!!seq", ""},
		{"map", map[string]int{"a": 1}, yaml.MappingNode, "!!map", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			v, err := json_schema.NewValue(tt.value)
			require.NoError(t, err)

			node := yaml.Node(v)
			assert.Equal(t, tt.wantKtd, node.Kind, "kind")
			assert.Equal(t, tt.wantTag, node.Tag, "tag")

			if tt.wantVal != "" {
				assert.Equal(t, tt.wantVal, node.Value, "value")
			}
		})
	}
}

// Unmarshalling must agree with NewValue, so a round trip is stable.
func TestUnmarshalYAMLUnwrapsDocumentNode(t *testing.T) {
	t.Parallel()

	var v json_schema.JSONValue

	require.NoError(t, v.UnmarshalYAML([]byte("2\n")))

	assert.Equal(t, yaml.ScalarNode, yaml.Node(v).Kind)
	assert.Equal(t, json_schema.MustNewValue(2), v)
}

// Whatever the node shape, decoding back must still produce the original value.
func TestValueRoundTrip(t *testing.T) {
	t.Parallel()

	v := json_schema.MustNewValue(map[string]any{"a": 1})
	assert.Equal(t, map[string]any{"a": 1}, v.Raw())

	marshalled, err := v.MarshalJSON()
	require.NoError(t, err)
	assert.JSONEq(t, `{"a":1}`, string(marshalled))
}
