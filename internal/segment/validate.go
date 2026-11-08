package segment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"smartcampaign/internal/catalog"
)

// Validate checks a rule against the catalog and the limits of segment-dsl.md §3–§5. It is the only
// constructor of a ValidRule. Errors block; warnings (duplicate_condition, targets_everyone) never do.
// Paths start with "segment" so they point into the proposal.
func Validate(r Rule) (ValidRule, []Issue, []Issue) {
	v := validator{}
	v.rule(r)
	if len(v.errs) > 0 {
		return ValidRule{}, v.errs, v.warns
	}
	return ValidRule{rule: r, conds: v.conds, ok: true}, nil, v.warns
}

// ValidateProposal validates the rule plus the proposal's own fields (channels, language, note lists).
func ValidateProposal(p Proposal) (ValidRule, []Issue, []Issue) {
	vr, errs, warns := Validate(p.Segment)
	errs = append(errs, checkEnum("channels_requested", p.ChannelsRequested, []string{"sms", "email", "both"})...)
	errs = append(errs, checkEnum("language", p.Language, catalog.Locales)...)
	errs = append(errs, checkNotes("unsupported_criteria", p.UnsupportedCriteria)...)
	errs = append(errs, checkNotes("assumptions", p.Assumptions)...)
	if n := utf8.RuneCountInString(p.Explanation); n > catalog.MaxExplanationRunes {
		errs = append(errs, Issue{Path: "explanation", Code: "string_too_long",
			Message: fmt.Sprintf("explanation has %d characters; the limit is %d", n, catalog.MaxExplanationRunes)})
	}
	if len(errs) > 0 {
		return ValidRule{}, errs, warns
	}
	return vr, nil, warns
}

func checkEnum(path, val string, allowed []string) []Issue {
	if slices.Contains(allowed, val) {
		return nil
	}
	return []Issue{{Path: path, Code: "invalid_enum_value", Message: path + " must be one of: " + strings.Join(allowed, ", ")}}
}

func checkNotes(path string, items []string) []Issue {
	var out []Issue
	if len(items) > catalog.MaxNoteItems {
		out = append(out, Issue{Path: path, Code: "list_too_long",
			Message: fmt.Sprintf("%s has %d items; the limit is %d", path, len(items), catalog.MaxNoteItems)})
	}
	for i, s := range items {
		if n := utf8.RuneCountInString(s); n > catalog.MaxNoteRunes {
			out = append(out, Issue{Path: fmt.Sprintf("%s[%d]", path, i), Code: "string_too_long",
				Message: fmt.Sprintf("item has %d characters; the limit is %d", n, catalog.MaxNoteRunes)})
		}
	}
	return out
}

type validator struct {
	errs  []Issue
	warns []Issue
	conds [][]cond
}

func (v *validator) errf(path, code, format string, args ...any) {
	v.errs = append(v.errs, Issue{Path: path, Code: code, Message: fmt.Sprintf(format, args...)})
}

func (v *validator) rule(r Rule) {
	const root = "segment"
	if len(r.Groups) == 0 {
		v.warns = append(v.warns, Issue{Path: root, Code: "targets_everyone",
			Message: "the rule has no conditions, so it targets every user (consent still applies)"})
	}
	if len(r.Groups) > catalog.MaxGroups {
		v.errf(root+".groups", "too_many_groups", "the rule has %d groups; the limit is %d", len(r.Groups), catalog.MaxGroups)
		return // do not spend work on a payload that is already over the limit
	}
	total := 0
	for _, g := range r.Groups {
		total += len(g.Conditions)
	}
	if total > catalog.MaxConditionsTotal {
		v.errf(root+".groups", "too_many_conditions", "the rule has %d conditions in total; the limit is %d", total, catalog.MaxConditionsTotal)
		return
	}
	seen := map[string]string{} // canonical condition → first path
	for gi, g := range r.Groups {
		gpath := fmt.Sprintf("%s.groups[%d]", root, gi)
		if len(g.Conditions) == 0 {
			v.errf(gpath+".conditions", "empty_group", "a group needs at least one condition")
		}
		if len(g.Conditions) > catalog.MaxConditionsPerGroup {
			v.errf(gpath+".conditions", "too_many_conditions", "the group has %d conditions; the limit is %d", len(g.Conditions), catalog.MaxConditionsPerGroup)
			continue
		}
		var conds []cond
		for ci, c := range g.Conditions {
			cpath := fmt.Sprintf("%s.conditions[%d]", gpath, ci)
			cd, ok := v.condition(c, cpath)
			if !ok {
				continue
			}
			if first, dup := seen[cd.key]; dup {
				v.warns = append(v.warns, Issue{Path: cpath, Code: "duplicate_condition", Message: "same condition as " + first})
			} else {
				seen[cd.key] = cpath
			}
			conds = append(conds, cd)
		}
		v.conds = append(v.conds, conds)
	}
	if len(v.errs) == 0 {
		v.contradictions()
	}
}

func (v *validator) condition(c Condition, path string) (cond, bool) {
	f, ok := catalog.Lookup(c.Field)
	if !ok {
		v.errf(path+".field", "unknown_field", "unknown field; allowed: %s", strings.Join(allFieldNames(), ", "))
		return cond{}, false
	}
	op := catalog.Op(c.Op)
	if !isKnownOp(op) {
		v.errf(path+".op", "unknown_operator", "unknown operator; operators for %s (%s): %s", f.Name, f.Type, opList(f.Ops))
		return cond{}, false
	}
	if !slices.Contains(f.Ops, op) {
		v.errf(path+".op", "op_type_mismatch", "operator %s does not apply to %s field %s; allowed: %s", op, f.Type, f.Name, opList(f.Ops))
		return cond{}, false
	}
	val, err := decodeValue(c.Value)
	vpath := path + ".value"
	if err != nil {
		v.errf(vpath, "invalid_value", "value is not valid JSON")
		return cond{}, false
	}
	cd := cond{field: f, op: op}
	nerr := len(v.errs)
	switch {
	case f.Type == catalog.Number && op == catalog.OpBetween:
		arr, ok := val.([]any)
		if !ok || len(arr) != 2 {
			v.errf(vpath, "wrong_type", "between needs [low, high], an array of two numbers")
			break
		}
		lo, okLo := v.number(f, arr[0], vpath+"[0]")
		hi, okHi := v.number(f, arr[1], vpath+"[1]")
		if okLo && okHi && lo > hi {
			v.errf(vpath, "invalid_value", "between needs low <= high")
		}
		cd.lo, cd.hi = lo, hi
		cd.key = fmt.Sprintf("%s|%s|%v|%v", f.Name, op, lo, hi)
	case f.Type == catalog.Number:
		cd.num, _ = v.number(f, val, vpath)
		cd.key = fmt.Sprintf("%s|%s|%v", f.Name, op, cd.num)
	case f.Type == catalog.Enum && (op == catalog.OpIn || op == catalog.OpNotIn):
		arr, ok := val.([]any)
		if !ok {
			v.errf(vpath, "wrong_type", "%s needs an array of strings", op)
			break
		}
		if len(arr) == 0 {
			v.errf(vpath, "empty_list", "%s needs at least one value", op)
		}
		if len(arr) > catalog.MaxListItems {
			v.errf(vpath, "list_too_long", "the list has %d values; the limit is %d", len(arr), catalog.MaxListItems)
			break
		}
		set := map[string]bool{}
		for i, item := range arr {
			s, ok := v.enumValue(f, item, fmt.Sprintf("%s[%d]", vpath, i))
			if !ok {
				continue
			}
			if set[s] {
				v.errf(fmt.Sprintf("%s[%d]", vpath, i), "duplicate_value", "the value is listed twice")
				continue
			}
			set[s] = true
			cd.strs = append(cd.strs, s)
		}
		sorted := slices.Clone(cd.strs)
		slices.Sort(sorted)
		cd.key = fmt.Sprintf("%s|%s|%q", f.Name, op, sorted)
	case f.Type == catalog.Enum:
		cd.str, _ = v.enumValue(f, val, vpath)
		cd.key = fmt.Sprintf("%s|%s|%q", f.Name, op, cd.str)
	case f.Type == catalog.Bool, op == catalog.OpIsNull, op == catalog.OpIsNotNull:
		if val != nil {
			v.errf(vpath, "invalid_value", "operator %s takes no value; use null", op)
		}
		cd.key = fmt.Sprintf("%s|%s", f.Name, op)
	case op == catalog.OpWithinLastDays || op == catalog.OpNotWithinLastDays:
		d, ok := v.integer(val, vpath, catalog.MinDays, catalog.MaxDays, "days")
		cd.days = d
		if ok {
			cd.key = fmt.Sprintf("%s|%s|%d", f.Name, op, d)
		}
	case op == catalog.OpBefore || op == catalog.OpAfter:
		s, ok := val.(string)
		if !ok {
			v.errf(vpath, "wrong_type", "%s needs a date string YYYY-MM-DD", op)
			break
		}
		if !validDate(s) {
			v.errf(vpath, "invalid_value", "date must be a valid YYYY-MM-DD between %s and %s", catalog.MinDate, catalog.MaxDate)
			break
		}
		cd.date = s
		cd.key = fmt.Sprintf("%s|%s|%s", f.Name, op, s)
	default:
		// Unreachable while catalog.OpsByType and this switch agree; fail closed if they drift.
		v.errf(path+".op", "unknown_operator", "operator %s is not supported by the validator", op)
	}
	return cd, len(v.errs) == nerr
}

func (v *validator) number(f catalog.Field, val any, path string) (float64, bool) {
	n, ok := val.(json.Number)
	if !ok {
		v.errf(path, "wrong_type", "%s needs a number", f.Name)
		return 0, false
	}
	x, err := strconv.ParseFloat(string(n), 64)
	if err != nil || math.IsInf(x, 0) || math.IsNaN(x) {
		v.errf(path, "out_of_range", "%s must be between %s and %s", f.Name, fmtNum(f.Min), fmtNum(f.Max))
		return 0, false
	}
	if f.Integer && x != math.Trunc(x) {
		v.errf(path, "not_integer", "%s needs a whole number", f.Name)
		return 0, false
	}
	if x < f.Min || x > f.Max {
		v.errf(path, "out_of_range", "%s must be between %s and %s", f.Name, fmtNum(f.Min), fmtNum(f.Max))
		return 0, false
	}
	return x, true
}

func (v *validator) integer(val any, path string, lo, hi int, what string) (int, bool) {
	n, ok := val.(json.Number)
	if !ok {
		v.errf(path, "wrong_type", "%s needs a whole number", what)
		return 0, false
	}
	x, err := strconv.ParseFloat(string(n), 64)
	if err != nil || math.IsInf(x, 0) {
		v.errf(path, "out_of_range", "%s must be between %d and %d", what, lo, hi)
		return 0, false
	}
	if x != math.Trunc(x) {
		v.errf(path, "not_integer", "%s needs a whole number", what)
		return 0, false
	}
	if x < float64(lo) || x > float64(hi) {
		v.errf(path, "out_of_range", "%s must be between %d and %d", what, lo, hi)
		return 0, false
	}
	return int(x), true
}

func (v *validator) enumValue(f catalog.Field, val any, path string) (string, bool) {
	s, ok := val.(string)
	if !ok {
		v.errf(path, "wrong_type", "%s needs a string", f.Name)
		return "", false
	}
	if utf8.RuneCountInString(s) > catalog.MaxStringRunes {
		v.errf(path, "string_too_long", "value is longer than %d characters", catalog.MaxStringRunes)
		return "", false
	}
	// Exact match only (case and diacritics): the backend never silently normalizes model values.
	if !slices.Contains(f.Values, s) {
		v.errf(path, "invalid_enum_value", "%s must be one of: %s", f.Name, strings.Join(f.Values, ", "))
		return "", false
	}
	return s, true
}

func decodeValue(raw json.RawMessage) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil // absent value in a Rule built in code is the same as null
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func validDate(s string) bool {
	if len(s) != 10 {
		return false
	}
	if _, err := time.Parse(time.DateOnly, s); err != nil {
		return false
	}
	return s >= catalog.MinDate && s <= catalog.MaxDate
}

func isKnownOp(op catalog.Op) bool {
	for _, ops := range catalog.OpsByType {
		if slices.Contains(ops, op) {
			return true
		}
	}
	return false
}

func allFieldNames() []string {
	var out []string
	for _, f := range catalog.Fields() {
		out = append(out, f.Name)
	}
	return out
}

func opList(ops []catalog.Op) string {
	s := make([]string, len(ops))
	for i, o := range ops {
		s[i] = string(o)
	}
	return strings.Join(s, ", ")
}

func fmtNum(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }
