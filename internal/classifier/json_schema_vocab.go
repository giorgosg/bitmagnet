package classifier

// JSON Schema vocabulary used by the schema builders in this package. These
// literals appear across json_schema.go, payload.go and every action and
// condition that contributes a fragment, so they are named once here.
const (
	schemaKeyType                 = "type"
	schemaKeyRef                  = "$ref"
	schemaKeyProperties           = "properties"
	schemaKeyAdditionalProperties = "additionalProperties"
	schemaKeyItems                = "items"
	schemaKeyOneOf                = "oneOf"
	schemaKeyCondition            = "condition"

	schemaTypeObject = "object"
	schemaTypeArray  = "array"
	schemaTypeString = "string"

	schemaRefAction       = "#/definitions/action"
	schemaRefActionSingle = "#/definitions/action_single"
	schemaRefCondition    = "#/definitions/condition"
)
