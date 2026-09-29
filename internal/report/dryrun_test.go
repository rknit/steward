package report

import (
	"bytes"
	"testing"

	"github.com/rknit/steward/internal/runner"
)

func TestDryRun(t *testing.T) {
	t.Parallel()
	plan := runner.Plan{Sections: []runner.Section{
		{Project: "api", Name: "setup"},
		{Project: "core", Name: "setup"},
		{Project: "core", Name: "build", Requires: []string{"core:setup"}},
		{Project: "api", Name: "build", Requires: []string{"api:setup", "core:build"}},
		{Project: "api", Name: "ci.full", Requires: []string{"api:build"}},
	}}
	var b bytes.Buffer
	DryRun(&b, plan, map[string]bool{"api:ci.full": true})
	want := `┌───┬──────────────┬───────────────────────┐
│ # │ key          │ requires              │
├───┼──────────────┼───────────────────────┤
│ 1 │ api:setup    │ -                     │
│ 2 │ core:setup   │ -                     │
│ 3 │ core:build   │ core:setup            │
│ 4 │ api:build    │ api:setup, core:build │
│ 5 │ api:ci.full* │ api:build             │
└───┴──────────────┴───────────────────────┘
* matched by a pattern; others are pulled in by requires
`
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestDryRunAllMatchedHasNoFooter(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	DryRun(&b, runner.Plan{Sections: []runner.Section{{Project: "a", Name: "build"}}}, map[string]bool{"a:build": true})
	want := "┌───┬──────────┬──────────┐\n│ # │ key      │ requires │\n├───┼──────────┼──────────┤\n" +
		"│ 1 │ a:build* │ -        │\n└───┴──────────┴──────────┘\n"
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}
