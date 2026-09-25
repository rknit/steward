package report

import (
	"bytes"
	"testing"
	"time"

	"github.com/rknit/steward/internal/runner"
)

func TestSummary(t *testing.T) {
	c := func(s runner.Status) runner.Cell { return runner.Cell{Status: s} }
	res := &runner.Results{
		Columns: []string{"setup", "build", "ci.pre-commit"},
		Rows: []runner.Row{
			{Project: "core", Cells: []runner.Cell{c(runner.Skip), c(runner.Done), {Status: runner.Pass, Fallback: "quick"}}},
			{Project: "lib", Cells: []runner.Cell{c(runner.Skip), c(runner.Done), {}}},
			{Project: "api", Cells: []runner.Cell{c(runner.Done), c(runner.Fail), {}}},
			{Project: "web", Cells: []runner.Cell{c(runner.Blocked), {}, {}}},
		},
	}
	var b bytes.Buffer
	Summary(&b, res, 75*time.Second, ".stew/runs/20260925T043601Z-3f9a")
	want := `┌─────────┬─────────┬───────┬───────────────┐
│ project │ setup   │ build │ ci.pre-commit │
├─────────┼─────────┼───────┼───────────────┤
│ core    │ skip    │ done  │ pass (quick)  │
│ lib     │ skip    │ done  │ -             │
│ api     │ done    │ fail  │ -             │
│ web     │ blocked │ -     │ -             │
└─────────┴─────────┴───────┴───────────────┘
total: 1m15s
logs: .stew/runs/20260925T043601Z-3f9a
`
	if got := b.String(); got != want {
		t.Errorf("summary:\n%s\nwant:\n%s", got, want)
	}
}

func TestSummaryWidensForLongNames(t *testing.T) {
	res := &runner.Results{
		Columns: []string{"setup"},
		Rows:    []runner.Row{{Project: "a-very-long-project", Cells: []runner.Cell{{Status: runner.Interrupted}}}},
	}
	var b bytes.Buffer
	Summary(&b, res, 0, "x")
	want := `┌─────────────────────┬─────────────┐
│ project             │ setup       │
├─────────────────────┼─────────────┤
│ a-very-long-project │ interrupted │
└─────────────────────┴─────────────┘
total: 0.0s
logs: x
`
	if got := b.String(); got != want {
		t.Errorf("summary:\n%s\nwant:\n%s", got, want)
	}
}
