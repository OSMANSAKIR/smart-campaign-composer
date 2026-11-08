package content

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"smartcampaign/internal/catalog"
)

// Proposal is the model's content answer (content.wire.schema.json).
type Proposal struct {
	Language string `json:"language"`
	SMS      struct {
		Text string `json:"text"`
	} `json:"sms"`
	Email struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	} `json:"email"`
	Notes string `json:"notes"`
}

// Content returns the copy in the storage shape.
func (p Proposal) Content() Content {
	return Content{SMSText: p.SMS.Text, EmailSubject: p.Email.Subject, EmailBody: p.Email.Body}
}

// MaxProposalBytes bounds what ParseProposal accepts (5000-rune body ≈ 20 KB at most).
const MaxProposalBytes = 64 << 10

// ParseProposal strictly decodes a content proposal: invalid JSON, trailing data, unknown or missing
// properties and wrong types are issues with JSON paths, like segment.Parse.
func ParseProposal(raw []byte) (Proposal, []Issue) {
	if len(raw) > MaxProposalBytes {
		return Proposal{}, []Issue{{Code: "invalid_json", Message: fmt.Sprintf("payload is larger than %d bytes", MaxProposalBytes)}}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var tree map[string]json.RawMessage
	if err := dec.Decode(&tree); err != nil {
		return Proposal{}, []Issue{{Code: "invalid_json", Message: "not a valid JSON object: " + err.Error()}}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Proposal{}, []Issue{{Code: "invalid_json", Message: "unexpected data after the JSON value"}}
	}
	var issues []Issue
	issues = append(issues, checkObject("", tree, map[string]string{"language": "string", "sms": "object", "email": "object", "notes": "string"})...)
	for name, props := range map[string]map[string]string{"sms": {"text": "string"}, "email": {"subject": "string", "body": "string"}} {
		raw, ok := tree[name]
		if !ok {
			continue
		}
		var sub map[string]json.RawMessage
		if json.Unmarshal(raw, &sub) == nil && sub != nil {
			issues = append(issues, checkObject(name, sub, props)...)
		}
	}
	if len(issues) > 0 {
		slices.SortFunc(issues, func(a, b Issue) int {
			if a.Path < b.Path {
				return -1
			}
			if a.Path > b.Path {
				return 1
			}
			return 0
		})
		return Proposal{}, issues
	}
	var p Proposal
	strict := json.NewDecoder(bytes.NewReader(raw))
	strict.DisallowUnknownFields()
	if err := strict.Decode(&p); err != nil {
		return Proposal{}, []Issue{{Code: "wrong_type", Message: err.Error()}}
	}
	if !slices.Contains(catalog.Locales, p.Language) {
		return Proposal{}, []Issue{{Path: "language", Code: "invalid_enum_value", Message: "language must be one of: tr, en"}}
	}
	return p, nil
}

func checkObject(prefix string, obj map[string]json.RawMessage, props map[string]string) []Issue {
	path := func(k string) string {
		if prefix == "" {
			return k
		}
		return prefix + "." + k
	}
	var out []Issue
	for k, v := range obj {
		want, known := props[k]
		if !known {
			out = append(out, Issue{Path: path(k), Code: "unknown_property", Message: fmt.Sprintf("unknown property %q", k)})
			continue
		}
		if got := rawKind(v); got != want {
			out = append(out, Issue{Path: path(k), Code: "wrong_type", Message: fmt.Sprintf("%s must be a %s, got %s", path(k), want, got)})
		}
	}
	for k := range props {
		if _, ok := obj[k]; !ok {
			out = append(out, Issue{Path: path(k), Code: "missing_property", Message: "required property " + k + " is missing"})
		}
	}
	return out
}

func rawKind(v json.RawMessage) string {
	t := bytes.TrimSpace(v)
	if len(t) == 0 {
		return "empty"
	}
	switch t[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 'n':
		return "null"
	case 't', 'f':
		return "boolean"
	}
	return "number"
}
