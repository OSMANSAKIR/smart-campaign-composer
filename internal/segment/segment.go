// Package segment holds the segment DSL types, the validator, the SQL compiler, the server-side rendering
// and the audience count (docs/contracts/segment-dsl.md). Owned by the llm builder.
//
// Phase 0 skeleton: types and signatures are the contract; bodies return ErrNotImplemented.
package segment

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ErrNotImplemented marks skeleton functions.
var ErrNotImplemented = errors.New("segment: not implemented")

// Condition is one {field, op, value} triple as proposed by the model or a user. Value stays raw until the
// validator interprets it for the field's type.
type Condition struct {
	Field string          `json:"field"`
	Op    string          `json:"op"`
	Value json.RawMessage `json:"value"`
}

// Group is a disjunction (OR) of conditions.
type Group struct {
	Conditions []Condition `json:"conditions"`
}

// Rule is a conjunction (AND) of groups. Zero groups means all users.
type Rule struct {
	Groups []Group `json:"groups"`
}

// Proposal is the model's full answer (segment.wire.schema.json).
type Proposal struct {
	Segment             Rule     `json:"segment"`
	UnsupportedCriteria []string `json:"unsupported_criteria"`
	Assumptions         []string `json:"assumptions"`
	ChannelsRequested   string   `json:"channels_requested"`
	Language            string   `json:"language"`
	Explanation         string   `json:"explanation"`
}

// Issue is a structured validation error or warning; Path is a JSON path into the proposal.
type Issue struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ValidRule can only be constructed by Validate. Compile accepts nothing else, so unvalidated model output
// can never reach SQL (KICKOFF §6.3). The zero value is invalid and must be rejected by Compile.
type ValidRule struct {
	rule Rule
	ok   bool
}

// Rule returns a copy of the validated rule for storage and display.
func (v ValidRule) Rule() Rule { return v.rule }

// Audience is the dry-run count result.
type Audience struct {
	Total          int `json:"total"`
	SMSReachable   int `json:"sms_reachable"`
	EmailReachable int `json:"email_reachable"`
	Both           int `json:"both"`
}

// Querier is satisfied by *pgxpool.Pool and pgx.Tx.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Parse strictly decodes a proposal: unknown fields and trailing data are errors.
func Parse(raw []byte) (Proposal, []Issue) {
	return Proposal{}, []Issue{{Path: "", Code: "not_implemented", Message: ErrNotImplemented.Error()}}
}

// Validate checks a rule against the catalog and limits. Warnings never block; errors do.
func Validate(r Rule) (ValidRule, []Issue, []Issue) {
	return ValidRule{}, []Issue{{Path: "segment", Code: "not_implemented", Message: ErrNotImplemented.Error()}}, nil
}

// Compile turns a validated rule into a WHERE fragment over `users u` with $1.. placeholders starting at
// firstParam, plus the matching arguments. Values are always parameters.
func Compile(v ValidRule, firstParam int) (where string, args []any, err error) {
	if !v.ok {
		return "", nil, errors.New("segment: Compile called with an unvalidated rule")
	}
	return "", nil, ErrNotImplemented
}

// Render returns the human-readable rendering per group for the given locale (tr|en).
func Render(v ValidRule, locale string) []string { return nil }

// Count runs the dry-run count with a statement timeout.
func Count(ctx context.Context, q Querier, v ValidRule) (Audience, error) {
	return Audience{}, ErrNotImplemented
}
