package segment

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
)

// MaxRawBytes bounds what Parse accepts. A real proposal is a few KB; anything larger is rejected before
// decoding so a hostile payload cannot make the decoder allocate without bound.
const MaxRawBytes = 64 << 10

// Parse strictly decodes a proposal: unknown fields, missing fields, wrong JSON types and trailing data are
// errors with JSON paths (segment-dsl.md §5 step 2). It does not check the catalog; Validate does.
func Parse(raw []byte) (Proposal, []Issue) {
	var p Proposal
	if issues := strictDecode(raw, proposalSpec, &p); len(issues) > 0 {
		return Proposal{}, issues
	}
	return p, nil
}

// ParseRule strictly decodes a bare rule ({"groups": [...]}), as sent by a manual edit. Paths use the same
// "segment." prefix as proposals so the UI maps errors the same way.
func ParseRule(raw []byte) (Rule, []Issue) {
	var r Rule
	if issues := strictDecodeAt(raw, ruleSpec, &r, "segment"); len(issues) > 0 {
		return Rule{}, issues
	}
	return r, nil
}

// spec describes the expected JSON shape. The decoder first checks the generic tree against it (which gives
// precise paths for every problem), then decodes into the typed struct.
type spec struct {
	kind  string // object | array | string | any
	props map[string]spec
	items *spec
}

var (
	stringSpec    = spec{kind: "string"}
	conditionSpec = spec{kind: "object", props: map[string]spec{
		"field": stringSpec, "op": stringSpec, "value": {kind: "any"},
	}}
	groupSpec = spec{kind: "object", props: map[string]spec{
		"conditions": {kind: "array", items: &conditionSpec},
	}}
	ruleSpec = spec{kind: "object", props: map[string]spec{
		"groups": {kind: "array", items: &groupSpec},
	}}
	proposalSpec = spec{kind: "object", props: map[string]spec{
		"segment":              ruleSpec,
		"unsupported_criteria": {kind: "array", items: &stringSpec},
		"assumptions":          {kind: "array", items: &stringSpec},
		"channels_requested":   stringSpec,
		"language":             stringSpec,
		"explanation":          stringSpec,
	}}
)

func strictDecode(raw []byte, s spec, out any) []Issue { return strictDecodeAt(raw, s, out, "") }

func strictDecodeAt(raw []byte, s spec, out any, root string) []Issue {
	if len(raw) > MaxRawBytes {
		return []Issue{{Path: root, Code: "invalid_json", Message: fmt.Sprintf("payload is larger than %d bytes", MaxRawBytes)}}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return []Issue{{Path: root, Code: "invalid_json", Message: "not valid JSON: " + jsonErr(err)}}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return []Issue{{Path: root, Code: "invalid_json", Message: "unexpected data after the JSON value"}}
	}
	if issues := walk(tree, s, root); len(issues) > 0 {
		return CapIssues(issues, MaxIssues) // RT-1: a 64 KB answer must not become thousands of issues
	}
	// The tree matched the spec, so this cannot fail on shape; DisallowUnknownFields is a second guard.
	typed := json.NewDecoder(bytes.NewReader(raw))
	typed.DisallowUnknownFields()
	if err := typed.Decode(out); err != nil {
		return []Issue{{Path: root, Code: "wrong_type", Message: jsonErr(err)}}
	}
	return nil
}

func walk(v any, s spec, path string) []Issue {
	where := path
	if where == "" {
		where = "the document"
	}
	switch s.kind {
	case "any":
		return nil
	case "string":
		if _, ok := v.(string); !ok {
			return []Issue{{Path: path, Code: "wrong_type", Message: where + " must be a string, got " + jsonType(v)}}
		}
		return nil
	case "array":
		arr, ok := v.([]any)
		if !ok {
			return []Issue{{Path: path, Code: "wrong_type", Message: where + " must be an array, got " + jsonType(v)}}
		}
		var issues []Issue
		for i, item := range arr {
			issues = append(issues, walk(item, *s.items, path+"["+strconv.Itoa(i)+"]")...)
		}
		return issues
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			return []Issue{{Path: path, Code: "wrong_type", Message: where + " must be an object, got " + jsonType(v)}}
		}
		var issues []Issue
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if _, known := s.props[k]; !known {
				// RT-5: the key is model-chosen; quote at most 40 sanitized runes of it.
				safe := SafeText(k, MaxQuotedRunes)
				issues = append(issues, Issue{Path: join(path, safe), Code: "unknown_property",
					Message: fmt.Sprintf("unknown property %q; allowed: %s", safe, propNames(s))})
			}
		}
		for _, k := range propNamesSorted(s) {
			child, present := obj[k]
			if !present {
				issues = append(issues, Issue{Path: join(path, k), Code: "missing_property", Message: "required property " + k + " is missing"})
				continue
			}
			issues = append(issues, walk(child, s.props[k], join(path, k))...)
		}
		return issues
	}
	return nil
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func propNamesSorted(s spec) []string {
	names := make([]string, 0, len(s.props))
	for k := range s.props {
		names = append(names, k)
	}
	slices.Sort(names)
	return names
}

func propNames(s spec) string {
	b, _ := json.Marshal(propNamesSorted(s))
	return string(b)
}

func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case json.Number:
		return "number"
	case bool:
		return "boolean"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

// jsonErr keeps decoder messages short and free of Go type names.
func jsonErr(err error) string {
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return fmt.Sprintf("%s (at byte %d)", se.Error(), se.Offset)
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "unexpected end of input"
	}
	return err.Error()
}
