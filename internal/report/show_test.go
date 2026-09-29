package report

import (
	"bytes"
	"testing"
	"time"

	"github.com/rknit/steward/internal/runner"
)

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func TestShowSpecExample(t *testing.T) {
	t.Parallel()
	sections := []ShownSection{
		{Project: "api", Section: "setup", Outcome: runner.Outcome{Status: runner.Done, Duration: ms(3100)},
			Log: []byte("--- stew: run: npm ci\n")},
		{Project: "api", Section: "build", Outcome: runner.Outcome{Status: runner.Fail, Duration: ms(4200), Cause: "exit 1"},
			Log: []byte("--- stew: run: npm run build\nsrc/db.ts(12,5): error TS2322\n")},
		{Project: "api", Section: "test", Outcome: runner.Outcome{Status: runner.Blocked}, BlockedBy: []string{"api:build"}},
		{Project: "web", Section: "build", Outcome: runner.Outcome{Status: runner.Blocked}, BlockedBy: []string{"api:build"}},
		{Project: "web", Section: "e2e", Outcome: runner.Outcome{Status: runner.Blocked}},
	}
	res := &runner.Results{
		Columns: []string{"setup", "build", "test", "e2e"},
		Rows: []runner.Row{
			{Project: "api", Cells: []runner.Status{runner.Done, runner.Fail, runner.Blocked, ""}},
			{Project: "web", Cells: []runner.Status{"", runner.Blocked, "", runner.Blocked}},
		},
	}
	var b bytes.Buffer
	Show(&b, "20260925T043601Z-3f9a", []string{"ci", "--level", "pre-commit"}, "tool exec . {{STEW_STEP}}",
		[]ProjectWrapper{{"api", "other-tool run {{STEW_STEP}}"}}, sections,
		&ShownSummary{Results: res, Total: ms(75004), Finished: true, Logs: ".stew/runs/20260925T043601Z-3f9a"})

	want := `run 20260925T043601Z-3f9a: stew ci --level pre-commit
wrapper: tool exec . {{STEW_STEP}}
wrapper api: other-tool run {{STEW_STEP}}
==> api: setup ... done (3.1s)
--- stew: run: npm ci
==> api: build ... fail (4.2s)
--- stew: run: npm run build
src/db.ts(12,5): error TS2322
(exit 1)
==> api: test ... blocked by api:build
==> web: build ... blocked by api:build
┌─────────┬───────┬─────────┬─────────┬─────────┐
│ project │ setup │ build   │ test    │ e2e     │
├─────────┼───────┼─────────┼─────────┼─────────┤
│ api     │ done  │ fail    │ blocked │ -       │
│ web     │ -     │ blocked │ -       │ blocked │
└─────────┴───────┴─────────┴─────────┴─────────┘
total: 1m15s
logs: .stew/runs/20260925T043601Z-3f9a
`
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestShowUnfinishedAndInterrupted(t *testing.T) {
	t.Parallel()
	sections := []ShownSection{
		{Project: "core", Section: "build", Outcome: runner.Outcome{Status: runner.Interrupted, Cause: "signal SIGINT"},
			Log: []byte("--- stew: run: make\nhalf")},
		{Project: "core", Section: "ci.full", Outcome: runner.Outcome{Status: Unfinished}, Log: []byte("--- stew: run: lint\npartial")},
	}
	res := &runner.Results{
		Columns: []string{"build", "ci.full"},
		Rows:    []runner.Row{{Project: "core", Cells: []runner.Status{runner.Interrupted, Unfinished}}},
	}
	var b bytes.Buffer
	Show(&b, "20260101T000001Z-0001", []string{"ci", "-l", "pre-commit"}, "", nil, sections,
		&ShownSummary{Results: res, Logs: ".stew/runs/20260101T000001Z-0001"})

	want := `run 20260101T000001Z-0001: stew ci -l pre-commit
==> core: build ... interrupted
--- stew: run: make
half
(signal SIGINT)
==> core: ci.full ... unfinished
--- stew: run: lint
partial
┌─────────┬─────────────┬────────────┐
│ project │ build       │ ci.full    │
├─────────┼─────────────┼────────────┤
│ core    │ interrupted │ unfinished │
└─────────┴─────────────┴────────────┘
total: unfinished
logs: .stew/runs/20260101T000001Z-0001
`
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestShowWithoutSummaryAndNoLog(t *testing.T) {
	t.Parallel()
	sections := []ShownSection{
		{Project: "lib", Section: "setup", Outcome: runner.Outcome{Status: runner.Skip, Duration: ms(0)}},
		{Project: "api", Section: "build", Outcome: runner.Outcome{Status: runner.Fail, Duration: ms(1200), Cause: "log error: disk full"}},
	}
	var b bytes.Buffer
	Show(&b, "20260101T000001Z-0001", []string{"build"}, "", nil, sections, nil)
	want := "run 20260101T000001Z-0001: stew build\n" +
		"==> lib: setup ... skip (0.0s)\n" +
		"==> api: build ... fail (1.2s)\n" +
		"(log error: disk full)\n"
	if b.String() != want {
		t.Errorf("got:\n%q\nwant:\n%q", b.String(), want)
	}
}

func TestShowBlockedWithoutBlockedByIsHidden(t *testing.T) {
	t.Parallel()
	sections := []ShownSection{
		{Project: "web", Section: "e2e", Outcome: runner.Outcome{Status: runner.Blocked}},
	}
	var b bytes.Buffer
	Show(&b, "20260101T000001Z-0001", []string{"build"}, "", nil, sections, nil)
	want := "run 20260101T000001Z-0001: stew build\n"
	if b.String() != want {
		t.Errorf("got:\n%q\nwant:\n%q", b.String(), want)
	}
}

func TestShowWrapperHeader(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	Show(&b, "20260925T043601Z-3f9a", []string{"ci"}, "tool exec . {{STEW_STEP}}", nil, nil, nil)
	if got, want := b.String(), "run 20260925T043601Z-3f9a: stew ci\nwrapper: tool exec . {{STEW_STEP}}\n"; got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	b.Reset()
	Show(&b, "20260925T043601Z-3f9a", []string{"ci"}, "set -x\ntool exec . {{STEW_STEP}}", nil, nil, nil)
	if got, want := b.String(), "run 20260925T043601Z-3f9a: stew ci\nwrapper: $'set -x\\ntool exec . {{STEW_STEP}}'\n"; got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestShowProjectWrapperHeader(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	Show(&b, "20260925T043601Z-3f9a", []string{"ci"}, "tool exec . {{STEW_STEP}}",
		[]ProjectWrapper{{"core", "a run {{STEW_STEP}}"}, {"api", "b run {{STEW_STEP}}"}}, nil, nil)
	want := "run 20260925T043601Z-3f9a: stew ci\nwrapper: tool exec . {{STEW_STEP}}\n" +
		"wrapper core: a run {{STEW_STEP}}\nwrapper api: b run {{STEW_STEP}}\n"
	if b.String() != want {
		t.Errorf("got %q\nwant %q", b.String(), want)
	}
	b.Reset()
	Show(&b, "20260925T043601Z-3f9a", []string{"ci"}, "", []ProjectWrapper{{"api", "set -x\nb run {{STEW_STEP}}"}}, nil, nil)
	want = "run 20260925T043601Z-3f9a: stew ci\nwrapper api: $'set -x\\nb run {{STEW_STEP}}'\n"
	if b.String() != want {
		t.Errorf("got %q\nwant %q", b.String(), want)
	}
}

func TestShellQuote(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	if got, want := Command([]string{"ci", "--level", "pre-commit", "my app"}), "stew ci --level pre-commit 'my app'"; got != want {
		t.Errorf("Command = %q, want %q", got, want)
	}
	if got := Command(nil); got != "stew" {
		t.Errorf("Command(nil) = %q, want %q", got, "stew")
	}
}
