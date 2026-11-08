package segment

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"smartcampaign/internal/catalog"
	"smartcampaign/internal/seed"
	"smartcampaign/internal/store/storetest"
)

// TestCount_EveryAcceptedRuleRunsOnPostgres_RAUD1: compiling is not enough; every rule the validator accepts
// must also execute on Postgres (types, casts, parameter binding) and give a sane audience. Sources: every
// field × allowed operator, 300 random multi-group rules, the fuzz seeds, and the hostile corpus cases that
// validation lets through (values that are hostile text but legal, e.g. an injection string as a city).
func TestCount_EveryAcceptedRuleRunsOnPostgres_RAUD1(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	if _, err := seed.Run(ctx, pool, seed.Generate(500, 20261108, time.Now())); err != nil {
		t.Fatal(err)
	}
	type src struct {
		name string
		raw  []byte // a whole proposal
		r    *Rule
	}
	var all []src

	// 1. every field × allowed operator
	var conds []Condition
	for _, f := range catalog.Fields() {
		for _, op := range f.Ops {
			cond := c(f.Name, string(op), goodValue(f, op))
			conds = append(conds, cond)
			r := rule(one(cond))
			all = append(all, src{name: "single " + f.Name + "/" + string(op), r: &r})
		}
	}
	// 2. random CNF combinations of those conditions
	rng := rand.New(rand.NewPCG(20261109, 1))
	for i := range 300 {
		var groups [][]Condition
		for range 1 + rng.IntN(4) {
			var g []Condition
			for range 1 + rng.IntN(3) {
				g = append(g, conds[rng.IntN(len(conds))])
			}
			groups = append(groups, g)
		}
		r := rule(groups...)
		all = append(all, src{name: fmt.Sprintf("random %d", i), r: &r})
	}
	// 3. fuzz seeds and 4. hostile corpus
	for i, raw := range [][]byte{[]byte(goldenProposal),
		[]byte(`{"segment":{"groups":[{"conditions":[{"field":"city","op":"in","value":["Ankara","x'; DROP TABLE users; --"]}]}]},"unsupported_criteria":[],"assumptions":[],"channels_requested":"sms","language":"en","explanation":""}`)} {
		all = append(all, src{name: fmt.Sprintf("fuzz seed %d", i), raw: raw})
	}
	for _, h := range loadHostile(t) {
		all = append(all, src{name: "hostile " + h.ID, raw: h.raw()})
	}

	accepted, executed := 0, 0
	for _, s := range all {
		var vr ValidRule
		var errs []Issue
		if s.r != nil {
			vr, errs, _ = Validate(*s.r)
		} else {
			vr, errs, _ = parseAndValidate(s.raw)
		}
		if len(errs) > 0 {
			continue // rejected inputs never reach SQL; the red-team tests assert the rejections
		}
		accepted++
		a, err := Count(ctx, pool, vr)
		if err != nil {
			b, _ := json.Marshal(vr.Rule())
			t.Fatalf("%s: accepted rule failed on Postgres: %v\n%s", s.name, err, b)
		}
		if a.Total < 0 || a.Total > 507 || a.SMSReachable > a.Total || a.EmailReachable > a.Total ||
			a.Both > min(a.SMSReachable, a.EmailReachable) {
			t.Fatalf("%s: inconsistent audience %+v", s.name, a)
		}
		executed++
	}
	t.Logf("%d inputs, %d accepted, %d executed on Postgres", len(all), accepted, executed)
	if accepted < 300 || !slices.ContainsFunc(all, func(s src) bool { return s.raw != nil }) {
		t.Fatalf("too few accepted rules (%d) to mean anything", accepted)
	}
}
