package segment

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"smartcampaign/internal/catalog"
)

func c(field, op, value string) Condition {
	return Condition{Field: field, Op: op, Value: json.RawMessage(value)}
}

func rule(groups ...[]Condition) Rule {
	r := Rule{Groups: []Group{}}
	for _, g := range groups {
		r.Groups = append(r.Groups, Group{Conditions: g})
	}
	return r
}

func one(cs ...Condition) []Condition { return cs }

func codes(issues []Issue) []string {
	var out []string
	for _, i := range issues {
		out = append(out, i.Code)
	}
	return out
}

// goodValue returns a valid JSON value for a field/op pair.
func goodValue(f catalog.Field, op catalog.Op) string {
	switch f.Type {
	case catalog.Number:
		if op == catalog.OpBetween {
			return fmt.Sprintf("[%v, %v]", f.Min, f.Max)
		}
		return fmt.Sprintf("%v", f.Min)
	case catalog.Enum:
		if op == catalog.OpIn || op == catalog.OpNotIn {
			b, _ := json.Marshal(f.Values[:2])
			return string(b)
		}
		b, _ := json.Marshal(f.Values[0])
		return string(b)
	case catalog.Date:
		switch op {
		case catalog.OpWithinLastDays, catalog.OpNotWithinLastDays:
			return "30"
		case catalog.OpBefore, catalog.OpAfter:
			return `"2026-01-15"`
		}
	}
	return "null"
}

func allOps() []catalog.Op {
	var out []catalog.Op
	for _, t := range []catalog.Type{catalog.Number, catalog.Enum, catalog.Bool, catalog.Date} {
		out = append(out, catalog.OpsByType[t]...)
	}
	return out
}

// Every field × every operator: allowed pairs validate, compile and render; the rest are op_type_mismatch.
func TestValidate_EveryFieldOperator_RAUD1(t *testing.T) {
	for _, f := range catalog.Fields() {
		for _, op := range allOps() {
			t.Run(f.Name+"/"+string(op), func(t *testing.T) {
				allowed := slices.Contains(f.Ops, op)
				val := "null"
				if allowed {
					val = goodValue(f, op)
				}
				vr, errs, _ := Validate(rule(one(c(f.Name, string(op), val))))
				if allowed {
					if len(errs) != 0 || !vr.Valid() {
						t.Fatalf("want valid, got %+v", errs)
					}
					if _, _, err := Compile(vr, 1); err != nil {
						t.Fatalf("compile: %v", err)
					}
					for _, loc := range []string{"tr", "en"} {
						if got := Render(vr, loc); len(got) != 1 || got[0] == "" || got[0] == f.Name+" "+string(op) {
							t.Fatalf("render %s: %q", loc, got)
						}
					}
					return
				}
				if got := codes(errs); !slices.Equal(got, []string{"op_type_mismatch"}) {
					t.Fatalf("want op_type_mismatch, got %v", got)
				}
				if errs[0].Path != "segment.groups[0].conditions[0].op" {
					t.Fatalf("path = %q", errs[0].Path)
				}
			})
		}
	}
}

func TestValidate_InvalidConditions_RAUD1(t *testing.T) {
	long := strings.Repeat("a", 101)
	many := make([]string, 51)
	for i := range many {
		many[i] = fmt.Sprintf("v%d", i)
	}
	manyJSON, _ := json.Marshal(many)
	tests := []struct {
		name string
		cond Condition
		code string
		path string
	}{
		{"unknown field", c("consent", "is_true", "null"), "unknown_field", ".field"},
		{"consent field is not targetable", c("sms_consent", "is_false", "null"), "unknown_field", ".field"},
		{"sql in field", c("u.city; DROP TABLE users", "eq", `"x"`), "unknown_field", ".field"},
		{"unknown operator", c("city", "like", `"İst%"`), "unknown_operator", ".op"},
		{"enum case differs", c("city", "eq", `"istanbul"`), "invalid_enum_value", ".value"},
		{"enum ascii fold", c("city", "eq", `"Istanbul"`), "invalid_enum_value", ".value"},
		{"enum number", c("city", "eq", `34`), "wrong_type", ".value"},
		{"enum too long", c("city", "eq", `"`+long+`"`), "string_too_long", ".value"},
		{"enum list not array", c("city", "in", `"İstanbul"`), "wrong_type", ".value"},
		{"enum list empty", c("city", "in", `[]`), "empty_list", ".value"},
		{"enum list too long", c("city", "not_in", string(manyJSON)), "list_too_long", ".value"},
		{"enum list duplicate", c("city", "in", `["Ankara","Ankara"]`), "duplicate_value", ".value[1]"},
		{"enum list bad item", c("loyalty_tier", "in", `["gold","diamond"]`), "invalid_enum_value", ".value[1]"},
		{"age below range", c("age", "gt", `17`), "out_of_range", ".value"},
		{"age above range", c("age", "lt", `101`), "out_of_range", ".value"},
		{"age not integer", c("age", "eq", `30.5`), "not_integer", ".value"},
		{"age string", c("age", "eq", `"30"`), "wrong_type", ".value"},
		{"age huge", c("age", "eq", `1e400`), "out_of_range", ".value"},
		{"total_spent negative", c("total_spent", "gte", `-1`), "out_of_range", ".value"},
		{"purchase_count fraction", c("purchase_count", "gte", `1.5`), "not_integer", ".value"},
		{"between not array", c("age", "between", `30`), "wrong_type", ".value"},
		{"between one item", c("age", "between", `[30]`), "wrong_type", ".value"},
		{"between reversed", c("age", "between", `[40, 30]`), "invalid_value", ".value"},
		{"between out of range", c("age", "between", `[10, 30]`), "out_of_range", ".value[0]"},
		{"bool with value", c("cart_abandoned", "is_true", `true`), "invalid_value", ".value"},
		{"days zero", c("last_purchase_at", "within_last_days", `0`), "out_of_range", ".value"},
		{"days too many", c("last_purchase_at", "within_last_days", `3651`), "out_of_range", ".value"},
		{"days fraction", c("last_purchase_at", "not_within_last_days", `1.5`), "not_integer", ".value"},
		{"days string", c("last_purchase_at", "within_last_days", `"30"`), "wrong_type", ".value"},
		{"date bad format", c("signup_at", "before", `"15/01/2026"`), "invalid_value", ".value"},
		{"date impossible", c("signup_at", "before", `"2026-02-30"`), "invalid_value", ".value"},
		{"date too early", c("signup_at", "after", `"1999-12-31"`), "invalid_value", ".value"},
		{"date too late", c("signup_at", "after", `"2101-01-01"`), "invalid_value", ".value"},
		{"date number", c("signup_at", "after", `20260101`), "wrong_type", ".value"},
		{"is_null with value", c("last_login_at", "is_null", `30`), "invalid_value", ".value"},
		{"number op on enum", c("city", "gt", `"Ankara"`), "op_type_mismatch", ".op"},
		{"date op on number", c("age", "within_last_days", `30`), "op_type_mismatch", ".op"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vr, errs, _ := Validate(rule(one(tt.cond)))
			if vr.Valid() {
				t.Fatal("rule must be invalid")
			}
			if len(errs) == 0 || errs[0].Code != tt.code {
				t.Fatalf("want %s, got %+v", tt.code, errs)
			}
			if want := "segment.groups[0].conditions[0]" + tt.path; errs[0].Path != want {
				t.Fatalf("path = %q, want %q", errs[0].Path, want)
			}
			if _, _, err := Compile(vr, 1); err == nil {
				t.Fatal("Compile must reject the invalid result")
			}
		})
	}
}

func TestValidate_Limits_RAUD1(t *testing.T) {
	cond := c("age", "gte", "18")
	groups := func(n, per int) Rule {
		r := Rule{}
		for range n {
			g := Group{}
			for range per {
				g.Conditions = append(g.Conditions, cond)
			}
			r.Groups = append(r.Groups, g)
		}
		return r
	}
	tests := []struct {
		name string
		r    Rule
		code string
	}{
		{"six groups", groups(6, 1), "too_many_groups"},
		{"eleven in a group", groups(1, 11), "too_many_conditions"},
		{"21 total", groups(3, 7), "too_many_conditions"},
		{"empty group", rule([]Condition{}), "empty_group"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs, _ := Validate(tt.r)
			if !slices.Contains(codes(errs), tt.code) {
				t.Fatalf("want %s, got %v", tt.code, codes(errs))
			}
		})
	}
	if _, errs, _ := Validate(groups(5, 4)); len(errs) != 0 {
		t.Fatalf("5 groups × 4 = 20 conditions is at the limit and valid: %v", errs)
	}
}

func TestValidate_Warnings_RAUD1(t *testing.T) {
	vr, errs, warns := Validate(Rule{})
	if len(errs) != 0 || !vr.Valid() || !slices.Equal(codes(warns), []string{"targets_everyone"}) {
		t.Fatalf("empty rule: errs=%v warns=%v", errs, warns)
	}
	_, errs, warns = Validate(rule(
		one(c("city", "in", `["Ankara","İzmir"]`)),
		one(c("city", "in", `["İzmir","Ankara"]`)),
	))
	if len(errs) != 0 || !slices.Equal(codes(warns), []string{"duplicate_condition"}) {
		t.Fatalf("duplicate: errs=%v warns=%v", errs, warns)
	}
	if warns[0].Path != "segment.groups[1].conditions[0]" {
		t.Fatalf("duplicate path %q", warns[0].Path)
	}
}

func TestValidate_Contradictions_RAUD1(t *testing.T) {
	tests := []struct {
		name          string
		r             Rule
		contradiction bool
	}{
		{"age >60 and <20", rule(one(c("age", "gt", "60")), one(c("age", "lt", "20"))), true},
		{"age >=30 and <=30", rule(one(c("age", "gte", "30")), one(c("age", "lte", "30"))), false},
		{"age >30 and <31 integer gap", rule(one(c("age", "gt", "30")), one(c("age", "lt", "31"))), true},
		{"total_spent >30 and <31 real", rule(one(c("total_spent", "gt", "30")), one(c("total_spent", "lt", "31"))), false},
		{"age eq 30 and neq 30", rule(one(c("age", "eq", "30")), one(c("age", "neq", "30"))), true},
		{"age between disjoint", rule(one(c("age", "between", "[20,30]")), one(c("age", "between", "[31,40]"))), true},
		{"city eq two values", rule(one(c("city", "eq", `"Ankara"`)), one(c("city", "eq", `"İzmir"`))), true},
		{"city eq and not_in", rule(one(c("city", "eq", `"Ankara"`)), one(c("city", "not_in", `["Ankara"]`))), true},
		{"city in overlapping", rule(one(c("city", "in", `["Ankara","İzmir"]`)), one(c("city", "in", `["İzmir"]`))), false},
		{"bool both", rule(one(c("cart_abandoned", "is_true", "null")), one(c("cart_abandoned", "is_false", "null"))), true},
		{"is_null and within", rule(one(c("last_purchase_at", "is_null", "null")), one(c("last_purchase_at", "within_last_days", "30"))), true},
		{"is_null and not_within is fine", rule(one(c("last_purchase_at", "is_null", "null")), one(c("last_purchase_at", "not_within_last_days", "30"))), false},
		{"is_null and is_not_null", rule(one(c("last_login_at", "is_null", "null")), one(c("last_login_at", "is_not_null", "null"))), true},
		{"within 30 and not within 30", rule(one(c("last_purchase_at", "within_last_days", "30")), one(c("last_purchase_at", "not_within_last_days", "30"))), true},
		{"within 30 and not within 7", rule(one(c("last_purchase_at", "within_last_days", "30")), one(c("last_purchase_at", "not_within_last_days", "7"))), false},
		{"before and after crossing", rule(one(c("signup_at", "before", `"2025-01-01"`)), one(c("signup_at", "after", `"2025-06-01"`))), true},
		{"before and after window", rule(one(c("signup_at", "after", `"2025-01-01"`)), one(c("signup_at", "before", `"2025-06-01"`))), false},
		{"OR group never proves emptiness", rule(one(c("age", "gt", "60"), c("city", "eq", `"Ankara"`)), one(c("age", "lt", "20"))), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs, _ := Validate(tt.r)
			got := slices.Contains(codes(errs), "contradiction")
			if got != tt.contradiction {
				t.Fatalf("contradiction = %v, want %v (errs %v)", got, tt.contradiction, errs)
			}
		})
	}
}

func TestValidateProposal_Fields_RAUD1(t *testing.T) {
	p := Proposal{Segment: Rule{Groups: []Group{}}, ChannelsRequested: "push", Language: "de",
		UnsupportedCriteria: make([]string, 11), Assumptions: []string{strings.Repeat("ş", 301)},
		Explanation: strings.Repeat("x", 1001)}
	_, errs, _ := ValidateProposal(p)
	want := []string{"invalid_enum_value", "invalid_enum_value", "list_too_long", "string_too_long", "string_too_long"}
	if got := codes(errs); !slices.Equal(got, want) {
		t.Fatalf("codes = %v, want %v", got, want)
	}
}

func goldenRule() Rule {
	return rule(
		one(c("city", "eq", `"İstanbul"`)),
		one(c("last_cart_add_at", "within_last_days", "30")),
		one(c("cart_abandoned", "is_true", "null")),
	)
}

func TestCompile_Golden_RAUD1(t *testing.T) {
	tests := []struct {
		name  string
		r     Rule
		first int
		sql   string
		args  []any
	}{
		{"official brief", goldenRule(), 1,
			"(u.city = $1) AND (u.last_cart_add_at >= now() - make_interval(days => $2)) AND (u.cart_abandoned IS TRUE)",
			[]any{"İstanbul", int32(30)}},
		{"or group and params offset", rule(one(c("city", "in", `["Ankara","İzmir"]`), c("age", "between", "[25, 34]")), one(c("last_purchase_at", "not_within_last_days", "90"))), 3,
			"(u.city = ANY($3) OR EXTRACT(YEAR FROM age(u.birth_date))::int BETWEEN $4 AND $5) AND ((u.last_purchase_at IS NULL OR u.last_purchase_at < now() - make_interval(days => $6)))",
			[]any{[]string{"Ankara", "İzmir"}, int64(25), int64(34), int32(90)}},
		{"numbers dates nulls", rule(one(c("total_spent", "gt", "1500.5")), one(c("loyalty_tier", "not_in", `["bronze"]`)), one(c("signup_at", "after", `"2025-01-31"`)), one(c("last_login_at", "is_not_null", "null"))), 1,
			"(u.total_spent > $1) AND (u.loyalty_tier <> ALL($2)) AND (u.signup_at >= ($3::date + 1)::timestamp AT TIME ZONE 'Europe/Istanbul') AND (u.last_login_at IS NOT NULL)",
			[]any{1500.5, []string{"bronze"}, time.Date(2025, 1, 31, 0, 0, 0, 0, time.UTC)}},
		{"everyone", Rule{}, 1, "TRUE", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vr, errs, _ := Validate(tt.r)
			if len(errs) != 0 {
				t.Fatal(errs)
			}
			sql, args, err := Compile(vr, tt.first)
			if err != nil {
				t.Fatal(err)
			}
			if sql != tt.sql {
				t.Fatalf("sql:\n got %s\nwant %s", sql, tt.sql)
			}
			if diff := cmp.Diff(tt.args, args); diff != "" {
				t.Fatalf("args (-want +got):\n%s", diff)
			}
		})
	}
}

// A value that tries to break out of a string literal is rejected by the enum whitelist, and numbers are
// bind parameters: the SQL text never contains a model value.
func TestCompile_ValueNeverInSQL_RAUD1(t *testing.T) {
	_, errs, _ := Validate(rule(one(c("city", "eq", `"x' OR 1=1 --"`))))
	if !slices.Contains(codes(errs), "invalid_enum_value") {
		t.Fatalf("hostile enum value must be rejected: %v", errs)
	}
	vr, _, _ := Validate(rule(one(c("age", "gte", "18")), one(c("last_login_at", "within_last_days", "777"))))
	sql, _, _ := Compile(vr, 1)
	if strings.Contains(sql, "18") || strings.Contains(sql, "777") {
		t.Fatalf("number literal leaked into SQL: %s", sql)
	}
}

func TestCompile_RejectsZeroValue_RAUD1(t *testing.T) {
	if _, _, err := Compile(ValidRule{}, 1); err == nil {
		t.Fatal("zero ValidRule must not compile")
	}
	vr, _, _ := Validate(rule(one(c("nope", "eq", `"x"`))))
	if _, _, err := Compile(vr, 1); err == nil {
		t.Fatal("invalid rule must not compile")
	}
	if Render(ValidRule{}, "en") != nil {
		t.Fatal("zero ValidRule must not render")
	}
}

func TestRender_Contract_RAUD1(t *testing.T) {
	vr, errs, _ := Validate(rule(
		one(c("city", "eq", `"İstanbul"`)),
		one(c("last_cart_add_at", "within_last_days", "30")),
		one(c("cart_abandoned", "is_true", "null")),
		one(c("last_purchase_at", "not_within_last_days", "30"), c("age", "between", "[20, 30]")),
	))
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	en := []string{"City is İstanbul", "Added to cart in the last 30 days", "Has an abandoned cart",
		"No purchase in the last 30 days (incl. never) or Age between 20 and 30"}
	tr := []string{"Şehir: İstanbul", "Son 30 günde sepete ürün ekledi", "Sepeti terk edilmiş",
		"Son 30 günde alışveriş yapmadı (hiç yapmamış dahil) veya Yaş 20 ile 30 arasında"}
	if diff := cmp.Diff(en, Render(vr, "en")); diff != "" {
		t.Fatalf("en (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(tr, Render(vr, "tr")); diff != "" {
		t.Fatalf("tr (-want +got):\n%s", diff)
	}
}

func TestValidRule_RuleIsACopy_RAUD1(t *testing.T) {
	vr, _, _ := Validate(goldenRule())
	r := vr.Rule()
	r.Groups[0].Conditions[0].Field = "mutated"
	r.Groups[0].Conditions[0].Value[1] = 'X'
	if got := vr.Rule().Groups[0].Conditions[0]; got.Field != "city" || string(got.Value) != `"İstanbul"` {
		t.Fatalf("ValidRule was mutated through Rule(): %+v", got)
	}
}
