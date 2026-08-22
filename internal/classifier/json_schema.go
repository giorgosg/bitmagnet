package classifier

import (
	"encoding/json"
)

type JSONSchema map[string]any

func (s JSONSchema) MarshalJSON() ([]byte, error) {
	return json.MarshalIndent(map[string]any(s), "", "  ")
}

const schemaID = "https://bitmagnet.io/schemas/classifier-0.1.json"

func (f features) JSONSchema() JSONSchema {
	return map[string]any{
		"$schema":     "http://json-schema.org/draft-07/schema#",
		"$id":         schemaID,
		schemaKeyType: schemaTypeObject,
		schemaKeyProperties: map[string]any{
			"$schema": map[string]any{
				"const": schemaID,
			},
			"workflows": map[string]any{
				schemaKeyType: schemaTypeObject,
				schemaKeyAdditionalProperties: map[string]any{
					schemaKeyRef: schemaRefAction,
				},
			},
			"flag_definitions": map[string]any{
				schemaKeyType: schemaTypeObject,
				schemaKeyAdditionalProperties: map[string]any{
					schemaKeyType: schemaTypeString,
					"enum":        FlagTypeValues(),
				},
			},
			"flags": map[string]any{
				schemaKeyType:                 schemaTypeObject,
				schemaKeyAdditionalProperties: true,
			},
			"keywords": map[string]any{
				schemaKeyType: schemaTypeObject,
				schemaKeyAdditionalProperties: map[string]any{
					schemaKeyType: schemaTypeArray,
					schemaKeyItems: map[string]any{
						schemaKeyType: schemaTypeString,
					},
				},
			},
			"extensions": map[string]any{
				schemaKeyType: schemaTypeObject,
				schemaKeyAdditionalProperties: map[string]any{
					schemaKeyType: schemaTypeArray,
					schemaKeyItems: map[string]any{
						schemaKeyType: schemaTypeString,
					},
				},
			},
		},
		schemaKeyAdditionalProperties: false,
		"definitions": func() map[string]any {
			defs := map[string]any{
				"action": map[string]any{
					schemaKeyOneOf: []map[string]any{
						{
							schemaKeyRef: schemaRefActionSingle,
						},
						{
							schemaKeyRef: "#/definitions/action_multi",
						},
					},
				},
				"action_multi": map[string]any{
					schemaKeyType: schemaTypeArray,
					schemaKeyItems: map[string]any{
						schemaKeyRef: schemaRefActionSingle,
					},
				},
				"action_single": map[string]any{
					schemaKeyOneOf: func() []map[string]any {
						result := make([]map[string]any, 0, len(f.actions))
						for _, def := range f.actions {
							result = append(result, map[string]any{
								schemaKeyRef: "#/definitions/action__" + def.name(),
							})
						}

						return result
					}(),
				},
				schemaKeyCondition: map[string]any{
					schemaKeyOneOf: func() []map[string]any {
						result := make([]map[string]any, 0, len(f.conditions))
						for _, def := range f.conditions {
							result = append(result, map[string]any{
								schemaKeyRef: "#/definitions/condition__" + def.name(),
							})
						}

						return result
					}(),
				},
			}
			for _, def := range f.actions {
				defs["action__"+def.name()] = def.JSONSchema()
			}

			for _, def := range f.conditions {
				defs["condition__"+def.name()] = def.JSONSchema()
			}

			return defs
		}(),
	}
}

func DefaultJSONSchema() JSONSchema {
	return defaultFeatures.JSONSchema()
}
