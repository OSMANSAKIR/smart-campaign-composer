// Package segment holds the segment DSL types, the strict parser, the validator, the SQL compiler, the
// server-side rendering and the audience count (docs/contracts/segment-dsl.md). Owned by the llm builder.
//
// Pipeline: Parse (strict decode) → Validate (catalog + limits; the only constructor of ValidRule) →
// Compile (whitelisted SQL, values only as $n) → Count. Nothing the model writes reaches SQL as text.
package segment

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"smartcampaign/internal/catalog"
)

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
	rule  Rule
	conds [][]cond // typed, validated values; Compile and Render read only these
	ok    bool
}

// Rule returns a copy of the validated rule for storage and display.
func (v ValidRule) Rule() Rule { return cloneRule(v.rule) }

// cloneRule deep-copies r (groups, conditions, raw values). Groups is never nil, so a copy marshals as
// {"groups":[]} rather than {"groups":null}.
func cloneRule(r Rule) Rule {
	out := Rule{Groups: make([]Group, len(r.Groups))}
	for i, g := range r.Groups {
		cs := make([]Condition, len(g.Conditions))
		for j, c := range g.Conditions {
			cs[j] = Condition{Field: c.Field, Op: c.Op, Value: append(json.RawMessage(nil), c.Value...)}
		}
		out.Groups[i] = Group{Conditions: cs}
	}
	return out
}

// Valid reports whether v was produced by a successful Validate.
func (v ValidRule) Valid() bool { return v.ok }

// cond is a validated condition with its value decoded for the field's type.
type cond struct {
	field catalog.Field
	op    catalog.Op
	num   float64  // number compare
	lo    float64  // number between
	hi    float64  // number between
	str   string   // enum eq/neq
	strs  []string // enum in/not_in
	days  int      // date relative
	date  string   // date absolute, YYYY-MM-DD
	key   string   // canonical form for duplicate detection
}

// Audience is the dry-run count result.
type Audience struct {
	Total          int `json:"total"`
	SMSReachable   int `json:"sms_reachable"`
	EmailReachable int `json:"email_reachable"`
	Both           int `json:"both"`
}

// Querier is satisfied by *pgxpool.Pool and pgx.Tx. Begin is needed so Count can scope
// `SET LOCAL statement_timeout` to its own transaction (or savepoint, inside a caller's transaction).
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Begin(ctx context.Context) (pgx.Tx, error)
}
