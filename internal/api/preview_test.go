package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestSegmentPreview_HTTP_RFLOW1 (F2): the rule editor's live preview answers 200 for valid and invalid
// rules alike (problems are data), uses the strict decoder (unknown keys are reported, never ignored), and
// rejects a missing rule with 400.
func TestSegmentPreview_HTTP_RFLOW1(t *testing.T) {
	srv := newTestServer(t)
	for name, tc := range map[string]struct {
		body   string
		status int
		valid  bool
		code   string
	}{
		"valid":        {`{"rule":{"groups":[{"conditions":[{"field":"city","op":"eq","value":"Ankara"}]}]}}`, 200, true, ""},
		"invalid op":   {`{"rule":{"groups":[{"conditions":[{"field":"city","op":"between","value":[1,2]}]}]}}`, 200, false, "op_type_mismatch"},
		"unknown key":  {`{"rule":{"Groups":[]}}`, 200, false, ""},
		"missing rule": {`{}`, 400, false, ""},
		"not json":     {`{`, 400, false, ""},
	} {
		t.Run(name, func(t *testing.T) {
			code, _, body := do(t, http.MethodPost, srv.URL+"/api/segment/preview", tc.body, nil)
			if code != tc.status {
				t.Fatalf("status %d, want %d: %s", code, tc.status, body)
			}
			if code != 200 {
				return
			}
			var p struct {
				Valid    bool
				Errors   []struct{ Code string }
				Audience *struct{ Total int }
			}
			if err := json.Unmarshal(body, &p); err != nil {
				t.Fatal(err)
			}
			if p.Valid != tc.valid || (p.Audience != nil) != tc.valid || (!tc.valid && len(p.Errors) == 0) {
				t.Fatalf("preview %s", body)
			}
			if tc.code != "" && !strings.Contains(string(body), `"code":"`+tc.code+`"`) {
				t.Fatalf("want error %s: %s", tc.code, body)
			}
		})
	}
}
