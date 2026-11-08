package segment

import (
	"fmt"
	"math"
	"slices"
	"time"

	"smartcampaign/internal/catalog"
)

// contradictions reports rules that provably match nobody, without looking at data (need.md N-ORC-004):
// only single-condition groups are considered, because they are ANDed facts about one field. Multi-condition
// groups are ORs and are never used to prove emptiness.
func (v *validator) contradictions() {
	type fact struct {
		c    cond
		path string
	}
	byField := map[string][]fact{}
	var order []string
	for gi, g := range v.conds {
		if len(g) != 1 {
			continue
		}
		name := g[0].field.Name
		if _, ok := byField[name]; !ok {
			order = append(order, name)
		}
		byField[name] = append(byField[name], fact{g[0], fmt.Sprintf("segment.groups[%d]", gi)})
	}
	for _, name := range order {
		facts := byField[name]
		if len(facts) < 2 {
			continue
		}
		f := facts[0].c.field
		var st fieldState
		st.init(f)
		for i, fc := range facts {
			st.apply(fc.c)
			if i > 0 && st.empty() {
				v.errf(fc.path, "contradiction",
					"this group contradicts %s: no %s value can satisfy both, so the audience is provably empty", facts[0].path, f.Name)
				break
			}
		}
	}
}

// fieldState accumulates ANDed constraints on one field.
type fieldState struct {
	typ catalog.Type

	// number: interval with inclusive flags, plus excluded points (neq)
	lo, hi       float64
	loInc, hiInc bool
	integer      bool
	excluded     []float64

	// enum: values still possible
	allowed map[string]bool

	// bool
	wantTrue, wantFalse bool

	// date
	requireNull, requireNotNull bool
	within                      int // smallest within_last_days N (0 = none)
	notWithin                   int // largest not_within_last_days M (0 = none)
	beforeDate, afterDate       time.Time
	hasBefore, hasAfter         bool
}

func (s *fieldState) init(f catalog.Field) {
	s.typ = f.Type
	s.lo, s.hi, s.loInc, s.hiInc = math.Inf(-1), math.Inf(1), false, false
	s.integer = f.Integer
	if f.Type == catalog.Enum {
		s.allowed = map[string]bool{}
		for _, val := range f.Values {
			s.allowed[val] = true
		}
	}
}

func (s *fieldState) lower(x float64, inc bool) {
	if x > s.lo || (x == s.lo && !inc) {
		s.lo, s.loInc = x, inc
	}
}

func (s *fieldState) upper(x float64, inc bool) {
	if x < s.hi || (x == s.hi && !inc) {
		s.hi, s.hiInc = x, inc
	}
}

func (s *fieldState) apply(c cond) {
	switch c.op {
	case catalog.OpEq:
		if s.typ == catalog.Enum {
			s.keepOnly([]string{c.str})
			return
		}
		s.lower(c.num, true)
		s.upper(c.num, true)
	case catalog.OpNeq:
		if s.typ == catalog.Enum {
			delete(s.allowed, c.str)
			return
		}
		s.excluded = append(s.excluded, c.num)
	case catalog.OpGt:
		s.lower(c.num, false)
	case catalog.OpGte:
		s.lower(c.num, true)
	case catalog.OpLt:
		s.upper(c.num, false)
	case catalog.OpLte:
		s.upper(c.num, true)
	case catalog.OpBetween:
		s.lower(c.lo, true)
		s.upper(c.hi, true)
	case catalog.OpIn:
		s.keepOnly(c.strs)
	case catalog.OpNotIn:
		for _, x := range c.strs {
			delete(s.allowed, x)
		}
	case catalog.OpIsTrue:
		s.wantTrue = true
	case catalog.OpIsFalse:
		s.wantFalse = true
	case catalog.OpIsNull:
		s.requireNull = true
	case catalog.OpIsNotNull:
		s.requireNotNull = true
	case catalog.OpWithinLastDays:
		s.requireNotNull = true
		if s.within == 0 || c.days < s.within {
			s.within = c.days
		}
	case catalog.OpNotWithinLastDays:
		// Includes NULL, so it never conflicts with is_null.
		s.notWithin = max(s.notWithin, c.days)
	case catalog.OpBefore:
		s.requireNotNull = true
		d, _ := time.Parse(time.DateOnly, c.date)
		if !s.hasBefore || d.Before(s.beforeDate) {
			s.beforeDate, s.hasBefore = d, true
		}
	case catalog.OpAfter:
		s.requireNotNull = true
		d, _ := time.Parse(time.DateOnly, c.date)
		if !s.hasAfter || d.After(s.afterDate) {
			s.afterDate, s.hasAfter = d, true
		}
	}
}

func (s *fieldState) keepOnly(vals []string) {
	for k := range s.allowed {
		if !slices.Contains(vals, k) {
			delete(s.allowed, k)
		}
	}
}

func (s *fieldState) empty() bool {
	switch s.typ {
	case catalog.Number:
		lo, hi, loInc, hiInc := s.lo, s.hi, s.loInc, s.hiInc
		if s.integer { // integer fields: tighten open bounds to the next whole number
			if !math.IsInf(lo, 0) {
				if loInc {
					lo = math.Ceil(lo)
				} else {
					lo = math.Floor(lo) + 1
				}
				loInc = true
			}
			if !math.IsInf(hi, 0) {
				if hiInc {
					hi = math.Floor(hi)
				} else {
					hi = math.Ceil(hi) - 1
				}
				hiInc = true
			}
		}
		if lo > hi || (lo == hi && (!loInc || !hiInc)) {
			return true
		}
		return lo == hi && slices.Contains(s.excluded, lo)
	case catalog.Enum:
		return len(s.allowed) == 0
	case catalog.Bool:
		return s.wantTrue && s.wantFalse
	case catalog.Date:
		if s.requireNull && s.requireNotNull {
			return true
		}
		// within N: col >= now-N; not_within M: col IS NULL OR col < now-M → overlap needs N > M.
		if s.within > 0 && s.notWithin > 0 && s.notWithin >= s.within {
			return true
		}
		// before d: col < d; after a: col >= a+1 day → overlap needs a+1 < d.
		if s.hasBefore && s.hasAfter && !s.afterDate.AddDate(0, 0, 1).Before(s.beforeDate) {
			return true
		}
	}
	return false
}
