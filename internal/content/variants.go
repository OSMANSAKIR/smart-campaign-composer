package content

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"unicode"
	"unicode/utf8"

	"smartcampaign/internal/catalog"
	"smartcampaign/internal/segment"
)

// MaxLabelRunes bounds a variant label (docs/contracts/api.md §9.3).
const MaxLabelRunes = 40

// VariantKeys are the fixed variant keys; "exactly two" is a schema property (api.md §9.3).
var VariantKeys = []string{"a", "b"}

// VariantProposal is one variant of the model's content_v2 answer.
type VariantProposal struct {
	Label string `json:"label"`
	SMS   struct {
		Text string `json:"text"`
	} `json:"sms"`
	Email struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	} `json:"email"`
}

// Content returns the variant's copy in the storage shape.
func (v VariantProposal) Content() Content {
	return Content{SMSText: v.SMS.Text, EmailSubject: v.Email.Subject, EmailBody: v.Email.Body}
}

// ProposalV2 is the model's two-variant content answer (content.wire.schema.json, prompt content_v2).
type ProposalV2 struct {
	Language string `json:"language"`
	Variants struct {
		A VariantProposal `json:"a"`
		B VariantProposal `json:"b"`
	} `json:"variants"`
	Notes string `json:"notes"`
}

// Variant returns variant "a" or "b".
func (p ProposalV2) Variant(key string) VariantProposal {
	if key == "b" {
		return p.Variants.B
	}
	return p.Variants.A
}

// RefineProposal is the model's answer to a refine request (refine.wire.schema.json, prompt refine_v1):
// one rewritten variant.
type RefineProposal struct {
	Language string `json:"language"`
	Label    string `json:"label"`
	SMS      struct {
		Text string `json:"text"`
	} `json:"sms"`
	Email struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	} `json:"email"`
	Notes string `json:"notes"`
}

// Content returns the refined copy in the storage shape.
func (p RefineProposal) Content() Content {
	return Content{SMSText: p.SMS.Text, EmailSubject: p.Email.Subject, EmailBody: p.Email.Body}
}

// shape describes a closed JSON object: a property maps to a kind ("string") or to a nested shape.
type shape map[string]any

var variantShape = shape{"label": "string", "sms": shape{"text": "string"}, "email": shape{"subject": "string", "body": "string"}}

var proposalV2Shape = shape{"language": "string", "notes": "string",
	"variants": shape{"a": variantShape, "b": variantShape}}

var refineShape = shape{"language": "string", "label": "string", "notes": "string",
	"sms": shape{"text": "string"}, "email": shape{"subject": "string", "body": "string"}}

// ParseProposalV2 strictly decodes a content_v2 answer: invalid JSON, trailing data, unknown or missing
// properties and wrong types are issues with JSON paths such as variants.b.sms.text.
func ParseProposalV2(raw []byte) (ProposalV2, []Issue) {
	var p ProposalV2
	if issues := parseStrict(raw, proposalV2Shape, &p); len(issues) > 0 {
		return ProposalV2{}, issues
	}
	if is := checkProposalCommon(p.Language, p.Notes); is != nil {
		return ProposalV2{}, []Issue{*is}
	}
	return p, nil
}

// ParseRefine strictly decodes a refine_v1 answer.
func ParseRefine(raw []byte) (RefineProposal, []Issue) {
	var p RefineProposal
	if issues := parseStrict(raw, refineShape, &p); len(issues) > 0 {
		return RefineProposal{}, issues
	}
	if is := checkProposalCommon(p.Language, p.Notes); is != nil {
		return RefineProposal{}, []Issue{*is}
	}
	return p, nil
}

func checkProposalCommon(language, notes string) *Issue {
	if !slices.Contains(catalog.Locales, language) {
		return &Issue{Path: "language", Code: "invalid_enum_value", Message: "language must be one of: tr, en"}
	}
	if issues := segment.ControlIssue("notes", notes); len(issues) > 0 { // RT-8a
		is := Issue(issues[0])
		return &is
	}
	return nil
}

func parseStrict(raw []byte, spec shape, out any) []Issue {
	if len(raw) > MaxProposalBytes {
		return []Issue{{Code: "invalid_json", Message: fmt.Sprintf("payload is larger than %d bytes", MaxProposalBytes)}}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var tree map[string]json.RawMessage
	if err := dec.Decode(&tree); err != nil {
		return []Issue{{Code: "invalid_json", Message: "not a valid JSON object: " + err.Error()}}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return []Issue{{Code: "invalid_json", Message: "unexpected data after the JSON value"}}
	}
	issues := checkShape("", tree, spec)
	if len(issues) > 0 {
		slices.SortFunc(issues, func(a, b Issue) int {
			switch {
			case a.Path < b.Path:
				return -1
			case a.Path > b.Path:
				return 1
			}
			return 0
		})
		return capIssues(issues)
	}
	strict := json.NewDecoder(bytes.NewReader(raw))
	strict.DisallowUnknownFields()
	if err := strict.Decode(out); err != nil {
		return []Issue{{Code: "wrong_type", Message: err.Error()}}
	}
	return nil
}

// checkShape checks one object level with checkObject and recurses into nested objects that are present.
func checkShape(prefix string, obj map[string]json.RawMessage, spec shape) []Issue {
	kinds := make(map[string]string, len(spec))
	for k, v := range spec {
		if s, ok := v.(string); ok {
			kinds[k] = s
		} else {
			kinds[k] = "object"
		}
	}
	issues := checkObject(prefix, obj, kinds)
	for k, v := range spec {
		sub, ok := v.(shape)
		raw, present := obj[k]
		if !ok || !present {
			continue
		}
		var child map[string]json.RawMessage
		if json.Unmarshal(raw, &child) != nil || child == nil {
			continue // wrong_type already reported by checkObject
		}
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		issues = append(issues, checkShape(path, child, sub)...)
	}
	return issues
}

// ValidateLabel checks a variant label: 1..MaxLabelRunes code points, no control characters. path is the
// issue path (e.g. "variants.a.label"). allowEmpty is for stored labels of pre-variant versions.
func ValidateLabel(path, label string, allowEmpty bool) []Issue {
	switch n := utf8.RuneCountInString(label); {
	case !utf8.ValidString(label):
		return []Issue{{Path: path, Code: "invalid_value", Message: "The label is not valid UTF-8."}}
	case n == 0 && !allowEmpty:
		return []Issue{{Path: path, Code: "required", Message: "Give the variant a short label (1 to 40 characters)."}}
	case n > MaxLabelRunes:
		return []Issue{{Path: path, Code: "label_too_long", Message: fmt.Sprintf("The label has %d characters; the limit is %d.", n, MaxLabelRunes)}}
	}
	for _, r := range label { // a label is one line: even \n and \t are refused
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return []Issue{{Path: path, Code: "control_character", Message: fmt.Sprintf("remove the control character U+%04X", r)}}
		}
	}
	return nil
}
