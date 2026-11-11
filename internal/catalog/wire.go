package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
)

// Wire schemas are the JSON Schemas sent to the model (structured outputs). They use only the subset the
// API supports: no minimum/maximum, no length limits, no recursion, additionalProperties:false everywhere.
// Everything they cannot express is enforced by the validators (segment-dsl.md §8, need.md N-ORC-003).

// SegmentWireSchema returns the SegmentProposal schema. Conditions are an anyOf of typed variants so that
// constrained decoding cannot pair a field with an operator of the wrong type.
func SegmentWireSchema() map[string]any {
	variants := []struct {
		name  string
		typ   Type
		ops   []Op
		value map[string]any
	}{
		{"number_compare", Number, []Op{OpEq, OpNeq, OpGt, OpGte, OpLt, OpLte}, typ("number")},
		{"number_between", Number, []Op{OpBetween}, arrayOf(typ("number"))},
		{"enum_compare", Enum, []Op{OpEq, OpNeq}, typ("string")},
		{"enum_list", Enum, []Op{OpIn, OpNotIn}, arrayOf(typ("string"))},
		{"bool_check", Bool, []Op{OpIsTrue, OpIsFalse}, typ("null")},
		{"date_relative", Date, []Op{OpWithinLastDays, OpNotWithinLastDays}, typ("integer")},
		{"date_absolute", Date, []Op{OpBefore, OpAfter}, map[string]any{"type": "string", "format": "date"}},
		{"date_null", Date, []Op{OpIsNull, OpIsNotNull}, typ("null")},
	}
	defs := map[string]any{}
	var refs []any
	for _, v := range variants {
		defs[v.name] = object(map[string]any{
			"field": enumOf(FieldNames(v.typ)),
			"op":    enumOf(opStrings(v.ops)),
			"value": v.value,
		})
		refs = append(refs, map[string]any{"$ref": "#/$defs/" + v.name})
	}
	condition := map[string]any{"anyOf": refs}
	group := object(map[string]any{"conditions": arrayOf(condition)})
	schema := object(map[string]any{
		"segment":              object(map[string]any{"groups": arrayOf(group)}),
		"unsupported_criteria": arrayOf(typ("string")),
		"assumptions":          arrayOf(typ("string")),
		"channels_requested":   enumOf([]string{"sms", "email", "both"}),
		"language":             enumOf(Locales),
		"explanation":          typ("string"),
	})
	schema["$defs"] = defs
	return schema
}

// variantSchema is one content variant: a short label plus the SMS and the email.
func variantSchema() map[string]any {
	return object(map[string]any{
		"label": typ("string"),
		"sms":   object(map[string]any{"text": typ("string")}),
		"email": object(map[string]any{"subject": typ("string"), "body": typ("string")}),
	})
}

// ContentWireSchema returns the content_v2 schema: two variants under the fixed keys a and b, so "exactly
// two" is a schema property (docs/contracts/api.md §9.3). Label length is checked by the validator.
func ContentWireSchema() map[string]any {
	return object(map[string]any{
		"language": enumOf(Locales),
		"variants": object(map[string]any{"a": variantSchema(), "b": variantSchema()}),
		"notes":    typ("string"),
	})
}

// RefineWireSchema returns the refine_v1 schema: the one rewritten variant (docs/contracts/api.md §9.4).
func RefineWireSchema() map[string]any {
	return object(map[string]any{
		"language": enumOf(Locales),
		"label":    typ("string"),
		"sms":      object(map[string]any{"text": typ("string")}),
		"email":    object(map[string]any{"subject": typ("string"), "body": typ("string")}),
		"notes":    typ("string"),
	})
}

// LocalizeWireSchema returns the localize_v1 schema: the campaign's variants in the target language, under the
// same fixed keys a and b as content_v2 (docs/contracts/api.md §9.10).
func LocalizeWireSchema() map[string]any {
	return object(map[string]any{
		"language": enumOf(Locales),
		"variants": object(map[string]any{"a": variantSchema(), "b": variantSchema()}),
		"notes":    typ("string"),
	})
}

// WireSchemaFiles maps contract file names to their generated content.
func WireSchemaFiles() (map[string][]byte, error) {
	out := map[string][]byte{}
	for name, s := range map[string]map[string]any{
		"segment.wire.schema.json":  SegmentWireSchema(),
		"content.wire.schema.json":  ContentWireSchema(),
		"refine.wire.schema.json":   RefineWireSchema(),
		"localize.wire.schema.json": LocalizeWireSchema(),
	} {
		b, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			return nil, err
		}
		out[name] = append(b, '\n')
	}
	return out, nil
}

// WriteWireSchemas writes the generated schemas into dir (docs/contracts). Used by `api schema`.
func WriteWireSchemas(dir string) error {
	files, err := WireSchemaFiles()
	if err != nil {
		return err
	}
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// object builds a closed object schema whose properties are all required, as structured outputs expect.
func object(props map[string]any) map[string]any {
	required := make([]string, 0, len(props))
	for k := range props {
		required = append(required, k)
	}
	slices.Sort(required)
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

func typ(t string) map[string]any { return map[string]any{"type": t} }
func arrayOf(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}
func enumOf(values []string) map[string]any { return map[string]any{"type": "string", "enum": values} }

func opStrings(ops []Op) []string {
	out := make([]string, len(ops))
	for i, o := range ops {
		out[i] = string(o)
	}
	return out
}
