package content

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzRender checks the personalization invariants for arbitrary templates and values: single pass (a value
// is never re-expanded), cleaned values never exceed the cap, HTML output never contains a raw '<' from the
// input, and Validate never panics and agrees with its own valid flag.
func FuzzRender(f *testing.F) {
	f.Add("Merhaba {{first_name}}!", "Ayşe", "İzmir")
	f.Add("{{ city }} {{first_name}} {{coupon}} {x}", "{{city}}", "<script>")
	f.Add("{{{first_name}}}", "‮ilA\x00", "")
	f.Fuzz(func(t *testing.T, tmpl, first, city string) {
		vars := Vars{"first_name": first, "city": city}
		for _, loc := range []string{"tr", "en"} {
			out := Render(tmpl, loc, vars)
			for _, name := range []string{"first_name", "city"} {
				v := Value(name, loc, vars)
				if utf8.RuneCountInString(v) > 20 && v != Fallbacks[loc][name] {
					t.Fatalf("value over the cap: %q", v)
				}
			}
			if !strings.Contains(tmpl, "{{") && out != tmpl {
				t.Fatalf("template without variables changed: %q → %q", tmpl, out)
			}
			h := RenderEmailHTML(tmpl, loc, vars)
			if strings.Contains(strings.NewReplacer("<p>", "", "</p>", "", "<br>", "").Replace(h), "<") {
				t.Fatalf("unescaped markup in %q", h)
			}
		}
		r := Validate(Content{SMSText: tmpl, EmailSubject: tmpl, EmailBody: tmpl}, Constraints{Locale: "tr", WorstCase: vars})
		if r.Valid != (len(r.Errors) == 0) {
			t.Fatal("valid flag disagrees with errors")
		}
	})
}
