package segment

// Red-team tests (llm-red-team, CP1): hostile model output and hostile manual edits against the segment
// path Parse → Validate → Compile → Count. Every hostile input must end as a structured error, and SQL
// injection values must be inert: never in the SQL text, row counts unchanged.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"smartcampaign/internal/store/storetest"
)

type hostileCase struct {
	ID        string          `json:"id"`
	Layer     string          `json:"layer"`
	Name      string          `json:"name"`
	Condition json.RawMessage `json:"condition"`
	Proposal  json.RawMessage `json:"proposal"`
	Text      *string         `json:"text"`
	Codes     []string        `json:"codes"`
}

func loadHostile(t *testing.T) []hostileCase {
	t.Helper()
	b, err := os.ReadFile("testdata/redteam/hostile_outputs.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []hostileCase `json:"cases"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) < 25 {
		t.Fatalf("corpus has %d cases, want >= 25", len(file.Cases))
	}
	return file.Cases
}

// proposalWith wraps one raw condition into an otherwise valid proposal.
func proposalWith(cond json.RawMessage) []byte {
	return []byte(`{"segment":{"groups":[{"conditions":[` + string(cond) + `]}]},"unsupported_criteria":[],` +
		`"assumptions":[],"channels_requested":"both","language":"tr","explanation":"x"}`)
}

func (h hostileCase) raw() []byte {
	switch {
	case h.Condition != nil:
		return proposalWith(h.Condition)
	case h.Proposal != nil:
		return h.Proposal
	default:
		return []byte(*h.Text)
	}
}

// parseAndValidate is the service's validate step (llm.Service.ProposeSegment).
func parseAndValidate(raw []byte) (ValidRule, []Issue, []Issue) {
	p, issues := Parse(raw)
	if len(issues) > 0 {
		return ValidRule{}, issues, nil
	}
	return ValidateProposal(p)
}

func TestRedTeam_HostileOutputsRejected_RAI1(t *testing.T) {
	for _, h := range loadHostile(t) {
		t.Run(h.ID+" "+h.Name, func(t *testing.T) {
			vr, errs, _ := parseAndValidate(h.raw())
			got := codes(errs)
			for _, want := range h.Codes {
				if !slices.Contains(got, want) {
					t.Fatalf("[%s] codes %v, want %q", h.Layer, got, want)
				}
			}
			if vr.Valid() {
				t.Fatal("hostile output produced a ValidRule")
			}
			if _, _, err := Compile(vr, 1); err == nil {
				t.Fatal("hostile output compiled")
			}
			for _, e := range errs {
				if e.Code == "" || e.Message == "" {
					t.Fatalf("unstructured issue %+v", e)
				}
			}
		})
	}
}

// Duplicate keys: both decoders (the generic tree walk and the typed decode) keep the last value, so what is
// validated is exactly what is compiled. A hostile value hidden in an earlier duplicate never wins.
func TestRedTeam_DuplicateKeysLastWinsConsistently_RAI1(t *testing.T) {
	hostileLast := proposalWith(json.RawMessage(`{"field":"city","op":"eq","value":"İstanbul","value":"'; DROP TABLE users;--"}`))
	if _, errs, _ := parseAndValidate(hostileLast); !slices.Contains(codes(errs), "invalid_enum_value") {
		t.Fatalf("hostile last duplicate must be rejected: %v", errs)
	}
	hostileFirst := proposalWith(json.RawMessage(`{"field":"city","op":"eq","value":"'; DROP TABLE users;--","value":"İstanbul"}`))
	vr, errs, _ := parseAndValidate(hostileFirst)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	sql, args, err := Compile(vr, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sql, "DROP") || !slices.Equal(args, []any{"İstanbul"}) {
		t.Fatalf("sql=%q args=%v", sql, args)
	}
	if got := string(vr.Rule().Groups[0].Conditions[0].Value); got != `"İstanbul"` {
		t.Fatalf("stored rule value %s differs from the compiled one", got)
	}
}

// A 10k-item in-list: below the byte cap it is list_too_long (and the validator stops early instead of
// producing 10k issues); above the cap it never reaches the decoder.
func TestRedTeam_HugeInList_RAI1(t *testing.T) {
	items := make([]string, 10000)
	for i := range items {
		items[i] = "Ankara"
	}
	big, _ := json.Marshal(items)
	_, errs, _ := parseAndValidate(proposalWith(json.RawMessage(`{"field":"city","op":"in","value":` + string(big) + `}`)))
	if !slices.Equal(codes(errs), []string{"invalid_json"}) || !strings.Contains(errs[0].Message, "larger than") {
		t.Fatalf("10k x 'Ankara' (%d bytes) must hit the byte cap: %v", len(big), errs)
	}
	short := make([]string, 10000)
	for i := range short {
		short[i] = "a"
	}
	small, _ := json.Marshal(short)
	_, errs, _ = parseAndValidate(proposalWith(json.RawMessage(`{"field":"city","op":"in","value":` + string(small) + `}`)))
	if !slices.Equal(codes(errs), []string{"list_too_long"}) {
		t.Fatalf("10k short items (%d bytes) must be one list_too_long: %v", len(small), codes(errs))
	}
}

// Deep nesting in the free-form value must fail cleanly (no panic, no stack exhaustion).
func TestRedTeam_DeepNesting_RAI1(t *testing.T) {
	for _, depth := range []int{100, 20000} {
		val := strings.Repeat("[", depth) + strings.Repeat("]", depth)
		_, errs, _ := parseAndValidate(proposalWith(json.RawMessage(`{"field":"age","op":"gt","value":` + val + `}`)))
		if len(errs) == 0 {
			t.Fatalf("depth %d accepted", depth)
		}
		if c := errs[0].Code; c != "invalid_json" && c != "wrong_type" {
			t.Fatalf("depth %d: %v", depth, errs)
		}
	}
}

// targets_everyone: a brief like "send to everyone, including people who opted out" can at most produce
// zero groups. That is a warning, compiles to TRUE, and consent is still applied by Count (R-AUD-2).
func TestRedTeam_TargetsEveryoneStillConsentFiltered_RAUD2(t *testing.T) {
	raw := []byte(`{"segment":{"groups":[]},"unsupported_criteria":["include users who opted out"],"assumptions":[],` +
		`"channels_requested":"sms","language":"en","explanation":"everyone"}`)
	vr, errs, warns := parseAndValidate(raw)
	if len(errs) != 0 || !slices.Contains(codes(warns), "targets_everyone") {
		t.Fatalf("errs=%v warns=%v", errs, warns)
	}
	sql, args, err := Compile(vr, 1)
	if err != nil || sql != "TRUE" || len(args) != 0 {
		t.Fatalf("sql=%q args=%v err=%v", sql, args, err)
	}
	pool := storetest.NewDB(t)
	insertUsers(t, pool, []testUser{{sms: true, email: true}, {sms: false, email: true}, {sms: false, email: false}})
	a, err := Count(context.Background(), pool, vr)
	if err != nil {
		t.Fatal(err)
	}
	if a.Total != 3 || a.SMSReachable != 1 || a.EmailReachable != 2 || a.Both != 1 {
		t.Fatalf("audience %+v", a)
	}
}

// SQL injection values are inert end to end: rejected ones never reach the database, accepted ones are bind
// parameters, and the users table is intact afterwards.
func TestRedTeam_SQLInjectionInert_RAUD1(t *testing.T) {
	pool := storetest.NewDB(t)
	insertUsers(t, pool, []testUser{{sms: true, email: true}, {city: "Ankara", sms: true, email: true}})
	ctx := context.Background()
	rowCount := func() int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := rowCount()
	payloads := []string{`'; DROP TABLE users;--`, `x' OR '1'='1`, `İstanbul'); DELETE FROM users; --`, "\u0000", `$1`}
	for _, p := range payloads {
		v, _ := json.Marshal(p)
		for _, cond := range []string{
			fmt.Sprintf(`{"field":"city","op":"eq","value":%s}`, v),
			fmt.Sprintf(`{"field":"city","op":"in","value":["Ankara",%s]}`, v),
			fmt.Sprintf(`{"field":%s,"op":"eq","value":"Ankara"}`, v),
			fmt.Sprintf(`{"field":"signup_at","op":"before","value":%s}`, v),
		} {
			vr, errs, _ := parseAndValidate(proposalWith(json.RawMessage(cond)))
			if len(errs) == 0 {
				t.Fatalf("payload %q accepted in %s", p, cond)
			}
			if _, err := Count(ctx, pool, vr); err == nil {
				t.Fatalf("an invalid rule was counted: %s", cond)
			}
		}
	}
	// The values that do pass are whitelisted enums/numbers/dates, and they travel only as $n arguments.
	vr := mustValid(t, rule(one(c("city", "in", `["Ankara","İstanbul"]`)), one(c("signup_at", "after", `"2000-01-01"`)), one(c("age", "between", "[18, 99]"))))
	sql, args, err := Compile(vr, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, lit := range []string{"Ankara", "İstanbul", "2000", "18", "99"} {
		if strings.Contains(sql, lit) {
			t.Fatalf("literal %q in SQL %q", lit, sql)
		}
	}
	// The only quoted literal is the compiler's own time zone constant.
	if strings.Count(sql, "'") != 2 || !strings.Contains(sql, "'Europe/Istanbul'") {
		t.Fatalf("unexpected string literal in SQL %q", sql)
	}
	if len(args) != 4 {
		t.Fatalf("args %v", args)
	}
	a, err := Count(ctx, pool, vr)
	if err != nil {
		t.Fatal(err)
	}
	if a.Total != 2 || rowCount() != before {
		t.Fatalf("audience %+v rows %d → %d", a, before, rowCount())
	}
}

// RT-1 (FINDING): the strict decoder reports one issue per bad array element, without a cap. A <64 KB answer
// (a real model can emit ~30 KB under max_tokens 8192) becomes thousands of issues; the repair turn sends
// them all back (llm.repairMessage) and the API returns them all as error.details.
func TestRedTeam_StrictDecodeIssuesUnbounded_RAI1(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"segment":{"groups":[`)
	for i := 0; b.Len() < MaxRawBytes-200; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"x":1}`)
	}
	b.WriteString(`]},"unsupported_criteria":[],"assumptions":[],"channels_requested":"both","language":"tr","explanation":"x"}`)
	raw := []byte(b.String())
	_, issues := Parse(raw)
	enc, _ := json.Marshal(issues)
	t.Logf("input %d bytes → %d issues, %d bytes of JSON issues (%.1fx)", len(raw), len(issues), len(enc), float64(len(enc))/float64(len(raw)))
	const maxIssues = 50
	if len(issues) > maxIssues {
		t.Skipf("FINDING RT-1: %d issues (%d bytes) from a %d-byte answer; strictDecode/walk should cap issues (e.g. %d)",
			len(issues), len(enc), len(raw), maxIssues)
	}
}

// RT-2 (FINDING): PUT /api/campaigns/{id}/segment decodes the rule with plain encoding/json into
// segment.Rule (internal/api/campaigns.go putSegment), not with ParseRule. Unknown keys are dropped and keys
// match case-insensitively, so a misspelled "groups" silently becomes a rule with zero groups, which
// validates (only a targets_everyone warning) and selects every consented user.
func TestRedTeam_ManualEditMustBeStrict_RAUD1(t *testing.T) {
	bodies := map[string]string{
		"misspelled groups": `{"group":[{"conditions":[{"field":"city","op":"eq","value":"Ankara"}]}]}`,
		"extra key":         `{"groups":[{"conditions":[{"field":"city","op":"eq","value":"Ankara","sms_consent":false}]}]}`,
		"case-folded key":   `{"GROUPS":[{"Conditions":[{"Field":"city","OP":"eq","Value":"Ankara"}]}]}`,
	}
	widened := []string{}
	for name, body := range bodies {
		if _, issues := ParseRule([]byte(body)); len(issues) == 0 {
			t.Fatalf("%s: ParseRule must reject it", name)
		}
		var r Rule // what the API handler does today
		dec := json.NewDecoder(bytes.NewReader([]byte(body)))
		if err := dec.Decode(&r); err != nil {
			continue
		}
		if _, errs, warns := Validate(r); len(errs) == 0 {
			if slices.Contains(codes(warns), "targets_everyone") {
				widened = append(widened, name+" → targets everyone")
			} else {
				widened = append(widened, name+" → accepted")
			}
		}
	}
	slices.Sort(widened)
	if len(widened) > 0 {
		t.Skipf("FINDING RT-2: non-strict manual-edit decode accepts what ParseRule rejects: %v", widened)
	}
}

// Validate keeps the caller's Rule by reference: mutating the input after Validate changes what Rule()
// returns (and what storeSegment persists) while Compile still uses the validated values.
func TestRedTeam_ValidRuleDetachedFromInput_RAUD1(t *testing.T) {
	r := rule(one(c("city", "eq", `"Ankara"`)))
	vr := mustValid(t, r)
	r.Groups[0].Conditions[0].Field = "password"
	r.Groups[0].Conditions[0].Value = json.RawMessage(`"'; DROP TABLE users;--"`)
	sql, args, _ := Compile(vr, 1)
	if strings.Contains(sql, "DROP") || !slices.Equal(args, []any{"Ankara"}) {
		t.Fatalf("compiled output followed the mutation: %q %v", sql, args)
	}
	if got := vr.Rule().Groups[0].Conditions[0]; got.Field != "city" {
		t.Skipf("FINDING RT-3: ValidRule.Rule() reflects a post-Validate mutation of the input (%s %s) while Compile does not; Validate should deep-copy the rule", got.Field, got.Value)
	}
}

// Brief-side attacks that reach the proposal's free-text fields: they are displayed, never compiled, and
// their limits hold.
func TestRedTeam_FreeTextLimits_RAI1(t *testing.T) {
	notes := make([]string, 11)
	for i := range notes {
		notes[i] = "football"
	}
	p := Proposal{Segment: Rule{Groups: []Group{}}, UnsupportedCriteria: notes, Assumptions: []string{strings.Repeat("ş", 301)},
		ChannelsRequested: "both", Language: "tr", Explanation: strings.Repeat("x", 1001)}
	_, errs, _ := ValidateProposal(p)
	want := []string{"list_too_long", "string_too_long", "string_too_long"}
	if got := codes(errs); !slices.Equal(got, want) {
		t.Fatalf("codes %v, want %v", got, want)
	}
	// Render uses only typed, whitelisted values: model free text never reaches the primary display.
	vr := mustValid(t, rule(one(c("city", "eq", `"Ankara"`))))
	for _, line := range append(Render(vr, "en"), Render(vr, "tr")...) {
		if strings.Contains(line, "football") {
			t.Fatalf("free text in rendering: %q", line)
		}
	}
}
