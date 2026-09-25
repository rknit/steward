package report

import (
	"bytes"
	"testing"
	"time"

	"github.com/rknit/steward/internal/runner"
)

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func TestShowSpecExample(t *testing.T) {
	ciFB := runner.Phase{Name: "ci.pre-commit", Used: "ci.quick", CI: true}
	phases := []ShownPhase{
		{Project: "core", Phase: setup, Outcome: runner.Outcome{Status: runner.Skip, Duration: ms(104)},
			Log: []byte("--- stew: verify: test -d node_modules\n")},
		{Project: "core", Phase: build, Outcome: runner.Outcome{Status: runner.Skip, Duration: ms(31)},
			Log: []byte("--- stew: verify: test -f dist/index.js\n")},
		{Project: "core", Phase: ciFB, Outcome: runner.Outcome{Status: runner.Pass, Duration: ms(8210)},
			Log: []byte("--- stew: run: npm run lint\nlint ok\n")},
		{Project: "api", Phase: setup, Outcome: runner.Outcome{Status: runner.Skip, Duration: ms(95)},
			Log: []byte("--- stew: verify: test -d node_modules\n")},
		{Project: "api", Phase: build, Outcome: runner.Outcome{Status: runner.Fail, Duration: ms(63012), Cause: "exit 1"},
			Log: []byte("--- stew: verify: test -f dist/index.js\n--- stew: run: npm run build\n\n" +
				"> api@1.0.0 build\n> tsc\n\n" +
				"src/db.ts(12,5): error TS2322: Type 'string' is not assignable to type 'number'.\n" +
				"src/api.ts(40,1): error TS2304: Cannot find name 'handler'.\ndone in 4.1s\n" +
				"--- stew: verify after run: test -f dist/index.js\n")},
		{Project: "web", Phase: setup, Outcome: runner.Outcome{Status: runner.Blocked}, BlockedBy: []string{"api"}},
	}
	res := &runner.Results{
		Columns: []string{"setup", "build", "ci.pre-commit"},
		Rows: []runner.Row{
			{Project: "core", Cells: []runner.Cell{{Status: runner.Skip}, {Status: runner.Skip}, {Status: runner.Pass, Fallback: "quick"}}},
			{Project: "api", Cells: []runner.Cell{{Status: runner.Skip}, {Status: runner.Fail}, {}}},
			{Project: "web", Cells: []runner.Cell{{Status: runner.Blocked}, {}, {}}},
		},
	}
	var b bytes.Buffer
	Show(&b, "20260925T043601Z-3f9a", []string{"ci", "--level", "pre-commit"}, phases,
		&ShownSummary{Results: res, Total: ms(75004), Finished: true, Logs: ".stew/runs/20260925T043601Z-3f9a"})

	want := `run 20260925T043601Z-3f9a: stew ci --level pre-commit
==> core: setup ... skip (0.1s)
--- stew: verify: test -d node_modules
==> core: build ... skip (0.0s)
--- stew: verify: test -f dist/index.js
==> core: ci.pre-commit -> ci.quick ... pass (8.2s)
--- stew: run: npm run lint
lint ok
==> api: setup ... skip (0.1s)
--- stew: verify: test -d node_modules
==> api: build ... fail (1m3s)
--- stew: verify: test -f dist/index.js
--- stew: run: npm run build

> api@1.0.0 build
> tsc

src/db.ts(12,5): error TS2322: Type 'string' is not assignable to type 'number'.
src/api.ts(40,1): error TS2304: Cannot find name 'handler'.
done in 4.1s
--- stew: verify after run: test -f dist/index.js
(exit 1)
==> web: setup ... blocked by api
┌─────────┬─────────┬───────┬───────────────┐
│ project │ setup   │ build │ ci.pre-commit │
├─────────┼─────────┼───────┼───────────────┤
│ core    │ skip    │ skip  │ pass (quick)  │
│ api     │ skip    │ fail  │ -             │
│ web     │ blocked │ -     │ -             │
└─────────┴─────────┴───────┴───────────────┘
total: 1m15s
logs: .stew/runs/20260925T043601Z-3f9a
`
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestShowUnfinishedAndInterrupted(t *testing.T) {
	ciFB := runner.Phase{Name: "ci.pre-commit", Used: "ci.quick", CI: true}
	phases := []ShownPhase{
		{Project: "core", Phase: build, Outcome: runner.Outcome{Status: runner.Interrupted, Cause: "signal SIGINT"},
			Log: []byte("--- stew: run: make\nhalf")},
		{Project: "core", Phase: ciFB, Outcome: runner.Outcome{Status: Unfinished}, Log: []byte("--- stew: run: lint\npartial")},
	}
	res := &runner.Results{
		Columns: []string{"build", "ci.pre-commit"},
		Rows:    []runner.Row{{Project: "core", Cells: []runner.Cell{{Status: runner.Interrupted}, {Status: Unfinished, Fallback: "quick"}}}},
	}
	var b bytes.Buffer
	Show(&b, "20260101T000001Z-0001", []string{"ci", "-l", "pre-commit"}, phases,
		&ShownSummary{Results: res, Logs: ".stew/runs/20260101T000001Z-0001"})

	want := `run 20260101T000001Z-0001: stew ci -l pre-commit
==> core: build ... interrupted
--- stew: run: make
half
(signal SIGINT)
==> core: ci.pre-commit -> ci.quick ... unfinished
--- stew: run: lint
partial
┌─────────┬─────────────┬────────────────────┐
│ project │ build       │ ci.pre-commit      │
├─────────┼─────────────┼────────────────────┤
│ core    │ interrupted │ unfinished (quick) │
└─────────┴─────────────┴────────────────────┘
total: unfinished
logs: .stew/runs/20260101T000001Z-0001
`
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestShowWithoutSummaryAndNoLog(t *testing.T) {
	phases := []ShownPhase{
		{Project: "lib", Phase: setup, Outcome: runner.Outcome{Status: runner.Skip, Duration: ms(0)}},
		{Project: "api", Phase: build, Outcome: runner.Outcome{Status: runner.Fail, Duration: ms(1200), Cause: "log error: disk full"}},
	}
	var b bytes.Buffer
	Show(&b, "20260101T000001Z-0001", []string{"build"}, phases, nil)
	want := "run 20260101T000001Z-0001: stew build\n" +
		"==> lib: setup ... skip (0.0s)\n" +
		"==> api: build ... fail (1.2s)\n" +
		"(log error: disk full)\n"
	if b.String() != want {
		t.Errorf("got:\n%q\nwant:\n%q", b.String(), want)
	}
}

func TestShellQuote(t *testing.T) {
	tests := map[string]string{
		"build":         "build",
		"--level":       "--level",
		"my-app.v2/x_y": "my-app.v2/x_y",
		"":              "''",
		"my app":        "'my app'",
		"it's":          `'it'\''s'`,
		"$HOME":         "'$HOME'",
		"a*":            "'a*'",
		"ünïcode":       "'ünïcode'",
		"a\tb":          `$'a\tb'`,
		"it's\n":        `$'it\'s\n'`,
		"a\\b\r":        `$'a\\b\r'`,
		"\x1b[0m ü":     `$'\x1b[0m ü'`,
		"\u0085":        `$'\xc2\x85'`,
		"\xff\x00":      `$'\xff\x00'`,
		"\uFFFD":        "'\uFFFD'",
	}
	for in, want := range tests {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCommand(t *testing.T) {
	if got, want := Command([]string{"ci", "--level", "pre-commit", "my app"}), "stew ci --level pre-commit 'my app'"; got != want {
		t.Errorf("Command = %q, want %q", got, want)
	}
	if got := Command(nil); got != "stew" {
		t.Errorf("Command(nil) = %q, want %q", got, "stew")
	}
}
