package json_schema

import (
	"encoding/json"
	"errors"
	"io"

	"gopkg.in/yaml.v3"
)

// JSONValue aliases yaml.Node as this package is used with both YAML and JSON payloads,
// and it is a useful and expressive type.
type JSONValue yaml.Node

func NewValue(value any) (JSONValue, error) {
	if num, ok := value.(json.Number); ok {
		flt, err := num.Float64()
		if err != nil {
			return JSONValue{}, err
		}

		value = flt
	}

	bytes, err := json.Marshal(value)
	if err != nil {
		return JSONValue{}, err
	}

	var node yaml.Node
	if err := yaml.Unmarshal(bytes, &node); err != nil {
		return JSONValue{}, err
	}

	return JSONValue(normalizeValueNode(node)), nil
}

// yaml.Unmarshal into a yaml.Node always produces a DocumentNode wrapping the value,
// annotated with the position it was parsed from. A JSONValue denotes a value rather
// than a document, encoders elsewhere emit the value node directly, and nothing reads
// the position — so unwrap and clear it, otherwise values that denote the same thing
// compare unequal depending on how they were constructed.
func normalizeValueNode(node yaml.Node) yaml.Node {
	if node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		node = *node.Content[0]
	}

	clearPosition(&node)

	return node
}

func clearPosition(node *yaml.Node) {
	node.Line = 0
	node.Column = 0

	for _, child := range node.Content {
		clearPosition(child)
	}
}

func MustNewValue(value any) JSONValue {
	jv, err := NewValue(value)
	if err != nil {
		panic(err)
	}

	return jv
}

func (v JSONValue) yamlNode() *yaml.Node {
	vn := yaml.Node(v)
	return &vn
}

func (v JSONValue) Raw() any {
	var raw any

	_ = v.yamlNode().Decode(&raw)

	return raw
}

func (v JSONValue) MarshalYAML() ([]byte, error) {
	return yaml.Marshal(v.yamlNode())
}

func (v JSONValue) MarshalJSON() ([]byte, error) {
	var value any
	if err := v.yamlNode().Decode(&value); err != nil {
		return nil, err
	}

	return json.Marshal(value)
}

func (v *JSONValue) UnmarshalYAML(data []byte) error {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return err
	}

	*v = JSONValue(normalizeValueNode(node))

	return nil
}

func (v *JSONValue) UnmarshalJSON(data []byte) error {
	if !json.Valid(data) {
		return errors.New("invalid json")
	}

	return v.UnmarshalYAML(data)
}

func (v JSONValue) MarshalGQL(w io.Writer) {
	bytes, err := json.Marshal(v)
	if err == nil {
		_, _ = w.Write(bytes)
	}
}

func (v *JSONValue) UnmarshalGQL(gql any) error {
	node, err := NewValue(gql)
	if err != nil {
		return err
	}

	*v = node

	return nil
}
