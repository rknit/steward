package report

import (
	"io"
	"strconv"
	"strings"

	"github.com/rknit/steward/internal/runner"
)

// DryRun writes the plan's execution order with each section's direct requires. matched holds the keys
// a pattern matched; the others are marked as pulled in by requires.
func DryRun(w io.Writer, plan runner.Plan, matched map[string]bool) {
	table := [][]string{{"#", "key", "requires"}}
	pulled := false
	for i, s := range plan.Sections {
		key := s.Key()
		if matched[key] {
			key += "*"
		} else {
			pulled = true
		}
		requires := "-"
		if len(s.Requires) > 0 {
			requires = strings.Join(s.Requires, ", ")
		}
		table = append(table, []string{strconv.Itoa(i + 1), key, requires})
	}
	Table(w, table)
	if pulled {
		io.WriteString(w, "* matched by a pattern; others are pulled in by requires\n")
	}
}
