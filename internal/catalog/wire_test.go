package catalog

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Keywords the structured-outputs API accepts (claude-api skill, checked 2026-11-08). The Go SDK does not
// strip unsupported keywords, so anything outside this list would be a 400 on a reviewer's machine.
var allowedKeywords = map[string]bool{
	"type": true, "properties": true, "required": true, "additionalProperties": true, "items": true,
	"enum": true, "const": true, "anyOf": true, "allOf": true, "$ref": true, "$defs": true,
	"format": true, "description": true,
}

func checkSchema(t *testing.T, path string, node any) {
	t.Helper()
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	for k, v := range m {
		if !allowedKeywords[k] {
			t.Errorf("%s: unsupported keyword %q", path, k)
		}
		switch k {
		case "properties", "$defs":
			for name, child := range v.(map[string]any) {
				checkSchema(t, path+"/"+k+"/"+name, child)
			}
		case "items":
			checkSchema(t, path+"/items", v)
		case "anyOf", "allOf":
			for i, child := range v.([]any) {
				checkSchema(t, path+"/"+k+"/"+string(rune('0'+i)), child)
			}
		}
	}
	if m["type"] == "object" {
		if m["additionalProperties"] != false {
			t.Errorf("%s: object without additionalProperties:false", path)
		}
		props, _ := m["properties"].(map[string]any)
		var keys []string
		for k := range props {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		req, _ := m["required"].([]string)
		if !slices.Equal(keys, req) {
			t.Errorf("%s: required %v != properties %v", path, req, keys)
		}
	}
}

func TestWireSchemas_UseOnlySupportedKeywords_RAI1(t *testing.T) {
	checkSchema(t, "segment", SegmentWireSchema())
	checkSchema(t, "content", ContentWireSchema())
}

func TestWireSchemas_CommittedFilesMatchGenerator(t *testing.T) {
	files, err := WireSchemaFiles()
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join("..", "..", "docs", "contracts", name))
		if err != nil {
			t.Fatalf("%s: %v (run `make schema`)", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is stale; run `make schema`", name)
		}
	}
}

func TestCatalog_EveryFieldHasOpsAndSQL(t *testing.T) {
	for _, f := range Fields() {
		if f.SQL == "" || len(f.Ops) == 0 || f.Label.EN == "" || f.Label.TR == "" || f.Description == "" {
			t.Errorf("incomplete field %+v", f)
		}
		if f.Type == Enum && len(f.Values) == 0 {
			t.Errorf("enum field %s has no values", f.Name)
		}
	}
	if _, ok := Lookup("password"); ok {
		t.Fatal("unexpected field")
	}
}
