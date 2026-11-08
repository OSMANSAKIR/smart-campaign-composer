package content

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readVectors(t *testing.T, name string, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "contracts", "testvectors", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}

func TestCount_SharedVectors_RCNT1(t *testing.T) {
	var file struct {
		Vectors []struct {
			Name        string `json:"name"`
			Input       string `json:"input"`
			Repeat      int    `json:"repeat"`
			Runes       int    `json:"runes"`
			UTF16Units  int    `json:"utf16_units"`
			Encoding    string `json:"encoding"`
			GSM7Septets *int   `json:"gsm7_septets"`
			Segments    int    `json:"segments"`
		} `json:"vectors"`
	}
	readVectors(t, "text_length.json", &file)
	for _, v := range file.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			s := strings.Repeat(v.Input, max(v.Repeat, 1))
			got := Count(s)
			want := Counts{Runes: v.Runes, UTF16Units: v.UTF16Units, Encoding: v.Encoding, Segments: v.Segments}
			if v.GSM7Septets != nil {
				want.GSM7Septets = *v.GSM7Septets
			}
			if got != want {
				t.Fatalf("Count = %+v, want %+v", got, want)
			}
		})
	}
}

func TestRender_SharedVectors_RCNT3(t *testing.T) {
	var file struct {
		Render []struct {
			Name     string `json:"name"`
			Template string `json:"template"`
			Locale   string `json:"locale"`
			Vars     Vars   `json:"vars"`
			Expected string `json:"expected"`
		} `json:"render"`
	}
	readVectors(t, "template_vars.json", &file)
	for _, v := range file.Render {
		t.Run(v.Name, func(t *testing.T) {
			if got := Render(v.Template, v.Locale, v.Vars); got != v.Expected {
				t.Fatalf("Render = %q, want %q", got, v.Expected)
			}
		})
	}
}
