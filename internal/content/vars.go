package content

import (
	"fmt"
	"slices"
	"strings"

	"smartcampaign/internal/catalog"
)

// ParseVariables lists the allowed variables used in tmpl (order of first appearance, no duplicates) and
// reports unknown_variable / malformed_variable issues (docs/contracts/testvectors/template_vars.json):
// a variable is "{{", optional whitespace, a name without braces or whitespace, optional whitespace, "}}".
// Braces are reserved for variables, so any brace left after removing well-formed tokens is malformed.
// Issue paths are empty; Validate sets them.
func ParseVariables(tmpl string) ([]string, []Issue) {
	vars := []string{}
	var issues []Issue
	unknown := map[string]bool{}
	for _, m := range variableRE.FindAllStringSubmatch(tmpl, -1) {
		name := m[1]
		if isAllowed(name) {
			if !slices.Contains(vars, name) {
				vars = append(vars, name)
			}
			continue
		}
		if !unknown[name] {
			unknown[name] = true
			issues = append(issues, Issue{Code: "unknown_variable",
				Message: fmt.Sprintf("unknown variable {{%s}}; allowed: %s", truncate(name, 40), allowedList())})
		}
	}
	rest := variableRE.ReplaceAllString(tmpl, "")
	if strings.ContainsAny(rest, "{}") {
		issues = append(issues, Issue{Code: "malformed_variable",
			Message: "unbalanced or misplaced braces; write variables as {{first_name}} or {{city}}, and do not use { or } otherwise"})
	}
	return vars, issues
}

func allowedList() string {
	out := make([]string, len(catalog.Variables))
	for i, v := range catalog.Variables {
		out[i] = "{{" + v + "}}"
	}
	return strings.Join(out, ", ")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
