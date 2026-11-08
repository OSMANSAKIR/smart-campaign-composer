package segment

import (
	"fmt"
	"strings"

	"smartcampaign/internal/catalog"
)

// Render returns the human-readable rendering per group for the given locale (tr|en; anything else → en).
// It reads only validated, typed values (whitelisted enums, numbers, dates), so no model free text reaches
// the primary display (segment-dsl.md §6). Conditions inside a group are joined by "or"/"veya"; the caller
// joins groups by "and"/"ve". A rule without groups renders as one "all users" line.
func Render(v ValidRule, locale string) []string {
	if !v.ok {
		return nil
	}
	tr := locale == "tr"
	if len(v.conds) == 0 {
		if tr {
			return []string{"Tüm kullanıcılar (izin kuralları yine uygulanır)"}
		}
		return []string{"All users (consent still applies)"}
	}
	or := " or "
	if tr {
		or = " veya "
	}
	out := make([]string, 0, len(v.conds))
	for _, g := range v.conds {
		parts := make([]string, 0, len(g))
		for _, cd := range g {
			parts = append(parts, renderCond(cd, tr))
		}
		out = append(out, strings.Join(parts, or))
	}
	return out
}

// Joiner returns the word that joins rendered groups ("and" / "ve").
func Joiner(locale string) string {
	if locale == "tr" {
		return "ve"
	}
	return "and"
}

func label(f catalog.Field, tr bool) string {
	if tr {
		return f.Label.TR
	}
	return f.Label.EN
}

// datePhrases are natural sentences for the date fields' relative and null operators.
// Index: [field][op] → {en, tr}; %d is the number of days.
var datePhrases = map[string]map[catalog.Op][2]string{
	"last_cart_add_at": {
		catalog.OpWithinLastDays:    {"Added to cart in the last %d days", "Son %d günde sepete ürün ekledi"},
		catalog.OpNotWithinLastDays: {"No add to cart in the last %d days (incl. never)", "Son %d günde sepete ürün eklemedi (hiç eklememiş dahil)"},
		catalog.OpIsNull:            {"Never added to cart", "Hiç sepete ürün eklemedi"},
		catalog.OpIsNotNull:         {"Has added to cart at least once", "En az bir kez sepete ürün ekledi"},
	},
	"last_purchase_at": {
		catalog.OpWithinLastDays:    {"Purchased in the last %d days", "Son %d günde alışveriş yaptı"},
		catalog.OpNotWithinLastDays: {"No purchase in the last %d days (incl. never)", "Son %d günde alışveriş yapmadı (hiç yapmamış dahil)"},
		catalog.OpIsNull:            {"Never purchased", "Hiç alışveriş yapmadı"},
		catalog.OpIsNotNull:         {"Has purchased at least once", "En az bir kez alışveriş yaptı"},
	},
	"last_login_at": {
		catalog.OpWithinLastDays:    {"Logged in in the last %d days", "Son %d günde giriş yaptı"},
		catalog.OpNotWithinLastDays: {"No login in the last %d days (incl. never)", "Son %d günde giriş yapmadı (hiç yapmamış dahil)"},
		catalog.OpIsNull:            {"Never logged in", "Hiç giriş yapmadı"},
		catalog.OpIsNotNull:         {"Has logged in at least once", "En az bir kez giriş yaptı"},
	},
	"signup_at": {
		catalog.OpWithinLastDays:    {"Signed up in the last %d days", "Son %d günde üye oldu"},
		catalog.OpNotWithinLastDays: {"Signed up more than %d days ago", "%d günden daha önce üye oldu"},
		catalog.OpIsNull:            {"Sign-up date unknown", "Üyelik tarihi bilinmiyor"},
		catalog.OpIsNotNull:         {"Sign-up date known", "Üyelik tarihi biliniyor"},
	},
}

var numberSymbols = map[catalog.Op]string{
	catalog.OpGt: ">", catalog.OpGte: "≥", catalog.OpLt: "<", catalog.OpLte: "≤", catalog.OpNeq: "≠",
}

func pick(tr bool, en, trs string) string {
	if tr {
		return trs
	}
	return en
}

func renderCond(cd cond, tr bool) string {
	f, lbl := cd.field, label(cd.field, tr)
	unit := ""
	switch f.Unit {
	case "TRY":
		unit = pick(tr, " TRY", " TL")
	case "years":
		unit = ""
	}
	switch f.Type {
	case catalog.Number:
		switch cd.op {
		case catalog.OpEq:
			return pick(tr, fmt.Sprintf("%s is %s%s", lbl, fmtNum(cd.num), unit), fmt.Sprintf("%s: %s%s", lbl, fmtNum(cd.num), unit))
		case catalog.OpBetween:
			return pick(tr, fmt.Sprintf("%s between %s and %s%s", lbl, fmtNum(cd.lo), fmtNum(cd.hi), unit),
				fmt.Sprintf("%s %s ile %s%s arasında", lbl, fmtNum(cd.lo), fmtNum(cd.hi), unit))
		default:
			return fmt.Sprintf("%s %s %s%s", lbl, numberSymbols[cd.op], fmtNum(cd.num), unit)
		}
	case catalog.Enum:
		list := strings.Join(cd.strs, ", ")
		switch cd.op {
		case catalog.OpEq:
			return pick(tr, fmt.Sprintf("%s is %s", lbl, cd.str), fmt.Sprintf("%s: %s", lbl, cd.str))
		case catalog.OpNeq:
			return pick(tr, fmt.Sprintf("%s is not %s", lbl, cd.str), fmt.Sprintf("%s: %s değil", lbl, cd.str))
		case catalog.OpIn:
			return pick(tr, fmt.Sprintf("%s is one of %s", lbl, list), fmt.Sprintf("%s: %s (herhangi biri)", lbl, list))
		case catalog.OpNotIn:
			return pick(tr, fmt.Sprintf("%s is none of %s", lbl, list), fmt.Sprintf("%s: %s dışında", lbl, list))
		}
	case catalog.Bool:
		if f.Name == "cart_abandoned" {
			if cd.op == catalog.OpIsTrue {
				return pick(tr, "Has an abandoned cart", "Sepeti terk edilmiş")
			}
			return pick(tr, "Has no abandoned cart", "Terk edilmiş sepeti yok")
		}
		if cd.op == catalog.OpIsTrue {
			return pick(tr, lbl+": yes", lbl+": evet")
		}
		return pick(tr, lbl+": no", lbl+": hayır")
	case catalog.Date:
		switch cd.op {
		case catalog.OpBefore:
			return pick(tr, fmt.Sprintf("%s before %s", lbl, cd.date), fmt.Sprintf("%s: %s öncesi", lbl, cd.date))
		case catalog.OpAfter:
			return pick(tr, fmt.Sprintf("%s after %s", lbl, cd.date), fmt.Sprintf("%s: %s sonrası", lbl, cd.date))
		}
		if p, ok := datePhrases[f.Name][cd.op]; ok {
			s := pick(tr, p[0], p[1])
			if strings.Contains(s, "%d") {
				return fmt.Sprintf(s, cd.days)
			}
			return s
		}
		switch cd.op {
		case catalog.OpWithinLastDays:
			return pick(tr, fmt.Sprintf("%s within the last %d days", lbl, cd.days), fmt.Sprintf("%s: son %d gün içinde", lbl, cd.days))
		case catalog.OpNotWithinLastDays:
			return pick(tr, fmt.Sprintf("%s not within the last %d days (incl. never)", lbl, cd.days),
				fmt.Sprintf("%s: son %d gün içinde değil (hiç olmamış dahil)", lbl, cd.days))
		case catalog.OpIsNull:
			return pick(tr, lbl+": never", lbl+": hiç yok")
		case catalog.OpIsNotNull:
			return pick(tr, lbl+": any time", lbl+": var")
		}
	}
	return fmt.Sprintf("%s %s", f.Name, cd.op)
}
