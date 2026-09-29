package runner

import (
	"context"
	"errors"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func ok(stdout string) fakeCmd  { return fakeCmd{stdout: stdout, took: time.Second} }
func bad(stderr string) fakeCmd { return fakeCmd{exit: 1, stderr: stderr, took: time.Second} }

func stepNames(out Outcome) []string {
	var s []string
	for _, st := range out.Steps {
		s = append(s, st.Step)
	}
	return s
}

func sec(project, name, run string, requires ...string) Section {
	return Section{Project: project, Name: name, Dir: "/w/" + project, Run: run, Requires: requires}
}

// plan builds a plan from sections already in execution order.
func plan(sections ...Section) Plan {
	var p Plan
	seenCol, seenRow := map[string]bool{}, map[string]bool{}
	for _, s := range sections {
		if !seenCol[s.Name] {
			seenCol[s.Name] = true
			p.Columns = append(p.Columns, s.Name)
		}
		if !seenRow[s.Project] {
			seenRow[s.Project] = true
			p.Projects = append(p.Projects, s.Project)
		}
	}
	p.Sections = sections
	return p
}

// runOne runs a single section "p:build" and returns its outcome.
func runOne(t *testing.T, s Section, script map[string][]fakeCmd) (Outcome, *harness) {
	t.Helper()
	s.Project, s.Name, s.Dir = "p", "build", "/w/p"
	h := newHarness(script)
	h.r.Run(context.Background(), plan(s))
	return h.rec.outcomes["p:build"], h
}

func TestSectionAlgorithm(t *testing.T) {
	t.Parallel()
	type S = Section
	tests := []struct {
		name      string
		s         Section
		script    map[string][]fakeCmd
		status    Status
		calls     []string
		replayed  []string
		cause     string
		wantFiles bool
	}{
		{"all empty", S{}, nil, Skip, nil, nil, "", false},
		{"only skip_if", S{SkipIf: "k"}, nil, Skip, nil, nil, "", false},
		{"run ok", S{Run: "r"}, map[string][]fakeCmd{"r": {ok("")}}, Done, []string{"r"}, nil, "", true},
		{"run fails", S{Run: "r"}, map[string][]fakeCmd{"r": {{exit: 3}}}, Fail,
			[]string{"r"}, []string{"run"}, "exit 3", true},
		{"skip_if passes", S{Run: "r", SkipIf: "k"}, map[string][]fakeCmd{"k": {ok("")}}, Skip,
			[]string{"k"}, nil, "", true},
		{"skip_if fails, run ok", S{Run: "r", SkipIf: "k"},
			map[string][]fakeCmd{"k": {bad("")}, "r": {ok("")}}, Done, []string{"k", "r"}, nil, "", true},
		{"skip_if fails, run fails", S{Run: "r", SkipIf: "k"},
			map[string][]fakeCmd{"k": {bad("")}, "r": {{exit: 2}}}, Fail,
			[]string{"k", "r"}, []string{"run"}, "exit 2", true},
		{"run ok, verify ok", S{Run: "r", Verify: "v"},
			map[string][]fakeCmd{"r": {ok("")}, "v": {ok("")}}, Done, []string{"r", "v"}, nil, "", true},
		{"verify fails", S{Run: "r", Verify: "v"},
			map[string][]fakeCmd{"r": {ok("")}, "v": {{exit: 4}}}, Fail,
			[]string{"r", "v"}, []string{"run", "verify"}, "exit 4", true},
		{"run fails, verify not run", S{Run: "r", Verify: "v"},
			map[string][]fakeCmd{"r": {{exit: 1}}}, Fail, []string{"r"}, []string{"run"}, "exit 1", true},
		{"assertion passes", S{Verify: "v"}, map[string][]fakeCmd{"v": {ok("")}}, Done,
			[]string{"v"}, nil, "", true},
		{"assertion fails", S{Verify: "v"}, map[string][]fakeCmd{"v": {{exit: 1}}}, Fail,
			[]string{"v"}, []string{"verify"}, "exit 1", true},
		{"skip_if passes before assertion", S{SkipIf: "k", Verify: "v"}, map[string][]fakeCmd{"k": {ok("")}}, Skip,
			[]string{"k"}, nil, "", true},
		{"all three", S{SkipIf: "k", Run: "r", Verify: "v"},
			map[string][]fakeCmd{"k": {bad("")}, "r": {ok("")}, "v": {ok("")}}, Done,
			[]string{"k", "r", "v"}, nil, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, h := runOne(t, tt.s, tt.script)
			if out.Status != tt.status {
				t.Errorf("status = %s, want %s", out.Status, tt.status)
			}
			var calls []string
			for _, c := range h.exec.calls {
				calls = append(calls, strings.TrimPrefix(c, "/w/p: "))
			}
			if !slices.Equal(calls, tt.calls) {
				t.Errorf("calls = %q, want %q", calls, tt.calls)
			}
			if got := stepNames(out); !slices.Equal(got, tt.replayed) {
				t.Errorf("replayed = %q, want %q", got, tt.replayed)
			}
			if out.Cause != tt.cause {
				t.Errorf("cause = %q, want %q", out.Cause, tt.cause)
			}
			if _, opened := h.logs.logs["p:build"]; opened != tt.wantFiles {
				t.Errorf("log opened = %v, want %v", opened, tt.wantFiles)
			}
			for key, l := range h.logs.logs {
				if !l.closed {
					t.Errorf("log %s not closed", key)
				}
			}
		})
	}
}

func TestCommandsRunInProjectDir(t *testing.T) {
	t.Parallel()
	_, h := runOne(t, Section{Run: "r"}, map[string][]fakeCmd{"r": {ok("")}})
	if !slices.Equal(h.exec.calls, []string{"/w/p: r"}) {
		t.Errorf("calls = %q", h.exec.calls)
	}
}

func TestReplayAndLogs(t *testing.T) {
	t.Parallel()
	out, h := runOne(t, Section{Run: "r", SkipIf: "v", Verify: "v"}, map[string][]fakeCmd{
		"v": {{exit: 1, stdout: "pre-verify noise\n"}, {exit: 1, stdout: "vout\n", stderr: "verr\n"}},
		"r": {{stdout: "rout\n", stderr: "rerr\n"}},
	})
	if out.Status != Fail {
		t.Fatalf("status = %s", out.Status)
	}
	want := []StepOutput{
		{Step: "run", Cmd: "r", Output: []byte("rout\nrerr\n")},
		{Step: "verify", Cmd: "v", Output: []byte("vout\nverr\n")},
	}
	if len(out.Steps) != len(want) {
		t.Fatalf("steps = %+v", out.Steps)
	}
	for i := range want {
		if out.Steps[i].Step != want[i].Step || out.Steps[i].Cmd != want[i].Cmd ||
			string(out.Steps[i].Output) != string(want[i].Output) {
			t.Errorf("step %d = %+v, want %+v", i, out.Steps[i], want[i])
		}
	}

	l := h.logs.logs["p:build"]
	wantOut := "--- stew: skip_if: v\npre-verify noise\n--- stew: run: r\nrout\n--- stew: verify: v\nvout\n"
	wantErr := "--- stew: skip_if: v\n--- stew: run: r\nrerr\n--- stew: verify: v\nverr\n"
	if l.stdout.String() != wantOut {
		t.Errorf("stdout log = %q, want %q", l.stdout.String(), wantOut)
	}
	if l.stderr.String() != wantErr {
		t.Errorf("stderr log = %q, want %q", l.stderr.String(), wantErr)
	}
}

func TestDurationSpansAllSteps(t *testing.T) {
	t.Parallel()
	out, _ := runOne(t, Section{Run: "r", SkipIf: "v", Verify: "v"}, map[string][]fakeCmd{
		"v": {{exit: 1, took: 1 * time.Second}, {took: 3 * time.Second}},
		"r": {{took: 2 * time.Second}},
	})
	if out.Status != Done || out.Duration != 6*time.Second {
		t.Errorf("status %s, duration %v; want done, 6s", out.Status, out.Duration)
	}
}

// example is the spec's worked example, in execution order. api:build fails.
func example() Plan {
	return plan(
		sec("api", "setup", "as"),
		sec("core", "lint", "cl"),
		sec("core", "setup", "cs"),
		sec("core", "build", "cb", "core:setup"),
		sec("api", "build", "ab", "api:setup", "core:build"),
		sec("api", "image", "ai", "api:build"),
		sec("api", "test", "at", "api:build"),
		sec("docs", "build", "db", "core:build"),
		sec("web", "build", "wb", "api:build", "api:test"),
		sec("web", "e2e", "we", "api:image", "web:build"),
	)
}

func exampleScript() map[string][]fakeCmd {
	script := map[string][]fakeCmd{"ab": {bad("boom\n")}}
	for _, c := range []string{"as", "cl", "cs", "cb", "ai", "at", "db", "wb", "we"} {
		script[c] = []fakeCmd{ok("")}
	}
	return script
}

func TestBlockingIsPrunedPerSection(t *testing.T) {
	t.Parallel()
	h := newHarness(exampleScript())
	res := h.r.Run(context.Background(), example())
	wantEvents := []string{
		"start api:setup", "end api:setup done",
		"start core:lint", "end core:lint done",
		"start core:setup", "end core:setup done",
		"start core:build", "end core:build done",
		"start api:build", "end api:build fail",
		"blocked api:image by api:build",
		"blocked api:test by api:build",
		"start docs:build", "end docs:build done",
		"blocked web:build by api:build",
	}
	if !slices.Equal(h.rec.events, wantEvents) {
		t.Errorf("events:\n%s\nwant:\n%s", strings.Join(h.rec.events, "\n"), strings.Join(wantEvents, "\n"))
	}
	wantRecord := []string{
		"end api:setup done", "end core:lint done", "end core:setup done", "end core:build done",
		"end api:build fail",
		"blocked api:image by api:build",
		"blocked api:test by api:build",
		"end docs:build done",
		"blocked web:build by api:build, api:test",
		"blocked web:e2e by api:image, web:build",
	}
	if !slices.Equal(h.record.calls, wantRecord) {
		t.Errorf("record:\n%s\nwant:\n%s", strings.Join(h.record.calls, "\n"), strings.Join(wantRecord, "\n"))
	}
	if !res.Failed || res.ExitCode() != 1 {
		t.Errorf("failed = %v, exit = %d", res.Failed, res.ExitCode())
	}
	if !slices.Equal(res.Columns, []string{"setup", "lint", "build", "image", "test", "e2e"}) {
		t.Errorf("columns = %q", res.Columns)
	}
	want := map[string][]Status{
		"api":  {Done, "", Fail, Blocked, Blocked, ""},
		"core": {Done, Done, Done, "", "", ""},
		"docs": {"", "", Done, "", "", ""},
		"web":  {"", "", Blocked, "", "", Blocked},
	}
	for _, row := range res.Rows {
		if !slices.Equal(row.Cells, want[row.Project]) {
			t.Errorf("%s cells = %q, want %q", row.Project, row.Cells, want[row.Project])
		}
	}
}

func TestAllDone(t *testing.T) {
	t.Parallel()
	script := exampleScript()
	script["ab"] = []fakeCmd{ok("")}
	h := newHarness(script)
	res := h.r.Run(context.Background(), example())
	if res.Failed || res.ExitCode() != 0 || len(h.exec.calls) != 10 {
		t.Errorf("failed = %v, calls = %q", res.Failed, h.exec.calls)
	}
}

func TestInterruptStopsBeforeNextSection(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancelCause(context.Background())
	script := exampleScript()
	script["cs"] = []fakeCmd{{took: time.Second, during: func() { cancel(ErrInterrupted) }}}
	h := newHarness(script)
	res := h.r.Run(ctx, example())
	if res.ExitCode() != 130 {
		t.Errorf("exit = %d", res.ExitCode())
	}
	last := h.rec.events[len(h.rec.events)-1]
	if last != "end core:setup interrupted" {
		t.Errorf("last event = %q", last)
	}
	if len(res.Rows) != 4 {
		t.Errorf("rows = %d, want all 4 projects", len(res.Rows))
	}
}

func TestLogOpenFailure(t *testing.T) {
	t.Parallel()
	script := exampleScript()
	h := newHarness(script)
	h.logs.openErr["api:setup"] = true
	res := h.r.Run(context.Background(), example())

	out := h.rec.outcomes["api:setup"]
	if out.Status != Fail || out.Cause != "log error: permission denied" {
		t.Errorf("outcome = %+v", out)
	}
	if !slices.Equal(h.exec.calls, notAPICalls) {
		t.Errorf("calls = %q, want only the sections that do not need api:setup: %q", h.exec.calls, notAPICalls)
	}
	if !slices.Contains(h.rec.events, "blocked api:build by api:setup") || !slices.Contains(h.rec.events, "end core:build done") {
		t.Errorf("events = %q", h.rec.events)
	}
	if res.ExitCode() != 1 {
		t.Errorf("exit = %d", res.ExitCode())
	}
}

func TestLogWriteFailureCancelsCommand(t *testing.T) {
	t.Parallel()
	script := exampleScript()
	script["as"] = []fakeCmd{{stdout: "some output\n"}}
	h := newHarness(script)
	h.logs.setup["api:setup"] = func(l *fakeLog) { l.failWrite = true }
	h.r.Run(context.Background(), example())

	out := h.rec.outcomes["api:setup"]
	if out.Status != Fail || out.Cause != "log error: disk full" {
		t.Errorf("outcome = %+v", out)
	}
	if !slices.Equal(h.exec.cancelled, []string{"as"}) {
		t.Errorf("cancelled = %q, want the running command", h.exec.cancelled)
	}
	if len(out.Steps) != 1 || string(out.Steps[0].Output) != "some output\n" {
		t.Errorf("replay = %+v", out.Steps)
	}
	if !slices.Contains(h.rec.events, "blocked api:build by api:setup") || !slices.Contains(h.rec.events, "end core:build done") {
		t.Errorf("events = %q", h.rec.events)
	}
}

func TestLogMarkerFailure(t *testing.T) {
	t.Parallel()
	script := exampleScript()
	h := newHarness(script)
	h.logs.setup["api:setup"] = func(l *fakeLog) { l.failMarker = true }
	h.r.Run(context.Background(), example())

	out := h.rec.outcomes["api:setup"]
	if out.Status != Fail || out.Cause != "log error: disk full" {
		t.Errorf("outcome = %+v", out)
	}
	if !slices.Equal(h.exec.calls, notAPICalls) {
		t.Errorf("calls = %q, want only the sections that do not need api:setup: %q", h.exec.calls, notAPICalls)
	}
}

// notAPICalls are the example's commands once api:setup fails before running: everything below it is blocked.
var notAPICalls = []string{"/w/core: cl", "/w/core: cs", "/w/core: cb", "/w/docs: db"}

func TestInterrupt(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	script := exampleScript()
	script["as"] = []fakeCmd{{stdout: "partial\n", during: func() { cancel(ErrInterrupted) }}}
	h := newHarness(script)
	res := h.r.Run(ctx, example())

	want := []string{"start api:setup", "end api:setup interrupted"}
	if !slices.Equal(h.rec.events, want) {
		t.Errorf("events:\n%s\nwant:\n%s", strings.Join(h.rec.events, "\n"), strings.Join(want, "\n"))
	}
	out := h.rec.outcomes["api:setup"]
	if out.Cause != "signal SIGINT" || len(out.Steps) != 1 || string(out.Steps[0].Output) != "partial\n" {
		t.Errorf("outcome = %+v", out)
	}
	if res.Interrupted != syscall.SIGINT || res.ExitCode() != 130 {
		t.Errorf("interrupted=%v exit=%d", res.Interrupted, res.ExitCode())
	}
	if len(res.Rows) != 4 {
		t.Errorf("rows = %d, want all 4 projects", len(res.Rows))
	}
	assertOnlyCells(t, res, "api:setup")
}

// assertOnlyCells fails for every summary cell that is set but not named in ended ("<project>:<column>").
func assertOnlyCells(t *testing.T, res *Results, ended ...string) {
	t.Helper()
	for _, row := range res.Rows {
		for i, c := range row.Cells {
			key := row.Project + ":" + res.Columns[i]
			if c != "" && !slices.Contains(ended, key) {
				t.Errorf("%s: unreached cell = %q, want zero", key, c)
			}
		}
	}
}

func TestInterruptSIGTERM(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	script := exampleScript()
	script["as"] = []fakeCmd{{stdout: "partial\n", during: func() { cancel(Interrupt{Signal: syscall.SIGTERM}) }}}
	h := newHarness(script)
	res := h.r.Run(ctx, example())

	want := []string{"start api:setup", "end api:setup interrupted"}
	if !slices.Equal(h.rec.events, want) {
		t.Errorf("events:\n%s\nwant:\n%s", strings.Join(h.rec.events, "\n"), strings.Join(want, "\n"))
	}
	out := h.rec.outcomes["api:setup"]
	if out.Cause != "signal SIGTERM" || len(out.Steps) != 1 || string(out.Steps[0].Output) != "partial\n" {
		t.Errorf("outcome = %+v", out)
	}
	if res.Interrupted != syscall.SIGTERM || res.ExitCode() != 143 {
		t.Errorf("interrupted=%v exit=%d", res.Interrupted, res.ExitCode())
	}
	assertOnlyCells(t, res, "api:setup")
}

func TestInterruptBetweenSections(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	script := exampleScript()
	h := newHarness(script)
	// Cancel after api:setup finished, before core:lint starts.
	h.r.Report = &cancelAfter{recorder: h.rec, event: "end api:setup done", cancel: func() { cancel(ErrInterrupted) }}
	res := h.r.Run(ctx, example())

	if want := []string{"start api:setup", "end api:setup done"}; !slices.Equal(h.rec.events, want) {
		t.Errorf("events = %q, want %q", h.rec.events, want)
	}
	if res.ExitCode() != 130 {
		t.Errorf("exit = %d", res.ExitCode())
	}
}

func TestInterruptBeforeBlockedSections(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	script := exampleScript()
	h := newHarness(script)
	// Ctrl-C lands while the failed section is being reported; every later section would be blocked.
	h.r.Report = &cancelAfter{recorder: h.rec, event: "end api:build fail", cancel: func() { cancel(ErrInterrupted) }}
	res := h.r.Run(ctx, example())

	wantEvents := []string{
		"start api:setup", "end api:setup done",
		"start core:lint", "end core:lint done",
		"start core:setup", "end core:setup done",
		"start core:build", "end core:build done",
		"start api:build", "end api:build fail",
	}
	if !slices.Equal(h.rec.events, wantEvents) {
		t.Errorf("events = %q, want %q", h.rec.events, wantEvents)
	}
	assertOnlyCells(t, res, "api:setup", "api:build", "core:lint", "core:setup", "core:build")
	if res.Interrupted != syscall.SIGINT || res.ExitCode() != 130 {
		t.Errorf("interrupted=%v exit=%d, want true, 130", res.Interrupted, res.ExitCode())
	}
}

// cancelAfter calls cancel once the recorder has seen event.
type cancelAfter struct {
	*recorder
	event  string
	cancel func()
}

func (c *cancelAfter) SectionEnd(s Section, out Outcome) {
	c.recorder.SectionEnd(s, out)
	if c.events[len(c.events)-1] == c.event {
		c.cancel()
	}
}

func TestResultCause(t *testing.T) {
	t.Parallel()
	tests := []struct {
		r    Result
		want string
	}{
		{Result{ExitCode: 2}, "exit 2"},
		{Result{Signal: "SIGKILL"}, "signal SIGKILL"},
		{Result{Err: errors.New("no such dir")}, "cannot start: no such dir"},
	}
	for _, tt := range tests {
		if got := tt.r.Cause(); got != tt.want {
			t.Errorf("Cause(%+v) = %q, want %q", tt.r, got, tt.want)
		}
	}
}

func TestRecorderCalls(t *testing.T) {
	t.Parallel()
	h := newHarness(exampleScript())
	h.r.Run(context.Background(), example())

	want := []string{
		"end api:setup done", "end core:lint done", "end core:setup done", "end core:build done",
		"end api:build fail",
		"blocked api:image by api:build",
		"blocked api:test by api:build",
		"end docs:build done",
		"blocked web:build by api:build, api:test",
		"blocked web:e2e by api:image, web:build",
	}
	if !slices.Equal(h.record.calls, want) {
		t.Errorf("record calls:\n%s\nwant:\n%s", strings.Join(h.record.calls, "\n"), strings.Join(want, "\n"))
	}
}

func TestRecordErrorFailsSection(t *testing.T) {
	t.Parallel()
	h := newHarness(exampleScript())
	h.record.failSection["api:setup"] = true
	res := h.r.Run(context.Background(), example())

	out := h.rec.outcomes["api:setup"]
	if out.Status != Fail || out.Cause != "log error: read-only file system" || out.Duration != time.Second {
		t.Errorf("outcome = %+v", out)
	}
	if !slices.Contains(h.rec.events, "blocked api:build by api:setup") || !slices.Contains(h.rec.events, "end core:build done") {
		t.Errorf("events = %q", h.rec.events)
	}
	if slices.Contains(h.rec.events, "start api:build") {
		t.Errorf("api kept running after api:setup's record failed: %q", h.rec.events)
	}
	if res.ExitCode() != 1 {
		t.Errorf("exit = %d", res.ExitCode())
	}
}

func TestRecordErrorKeepsFailCause(t *testing.T) {
	t.Parallel()
	script := exampleScript()
	script["as"] = []fakeCmd{{exit: 2}}
	h := newHarness(script)
	h.record.failSection["api:setup"] = true
	h.r.Run(context.Background(), example())
	if out := h.rec.outcomes["api:setup"]; out.Status != Fail || out.Cause != "exit 2" {
		t.Errorf("outcome = %+v", out)
	}
}

func TestRecordErrorKeepsInterrupted(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	script := exampleScript()
	script["as"] = []fakeCmd{{during: func() { cancel(ErrInterrupted) }}}
	h := newHarness(script)
	h.record.failSection["api:setup"] = true
	h.r.Run(ctx, example())
	if out := h.rec.outcomes["api:setup"]; out.Status != Interrupted {
		t.Errorf("outcome = %+v", out)
	}
	if !slices.Equal(h.record.calls, []string{"end api:setup interrupted"}) {
		t.Errorf("record calls after interrupt = %q", h.record.calls)
	}
}

func TestStewEnv(t *testing.T) {
	t.Parallel()
	h := newHarness(map[string][]fakeCmd{"v": {bad(""), ok("")}, "r": {ok("")}, "q": {ok("")}})
	h.r.Run(context.Background(), plan(
		Section{Project: "my.lib", Name: "setup", Dir: "/w/lib", Run: "r", SkipIf: "v", Verify: "v"},
		Section{Project: "my.lib", Name: "ci.quick", Dir: "/w/lib", Run: "q"},
	))
	env := func(section string) []string {
		return []string{
			"STEW_RUN_ID=20260101T000000Z-abcd",
			"STEW_ROOT=/w",
			"STEW_PROJECT=my.lib",
			"STEW_SECTION=" + section,
			"STEW_TAG=my.lib:" + section,
		}
	}
	want := [][]string{env("setup"), env("setup"), env("setup"), env("ci.quick")}
	if !slices.EqualFunc(h.exec.envs, want, slices.Equal) {
		t.Errorf("envs = %q\nwant %q", h.exec.envs, want)
	}
}

// runWrapped runs a plan with one wrapped project "p" of sections on h and returns h's outcomes.
func runWrapped(ctx context.Context, h *harness, sections ...Section) map[string]Outcome {
	for i := range sections {
		sections[i].Project = "p"
		sections[i].Dir = "/w/p"
		sections[i].Wrappers = []string{"outer {{STEW_STEP}}", "inner {{STEW_STEP}}"}
	}
	h.r.Run(ctx, plan(sections...))
	return h.rec.outcomes
}

// runWrappedOne is runOne with wrappers on the section.
func runWrappedOne(t *testing.T, s Section, script map[string][]fakeCmd) (Outcome, *harness) {
	t.Helper()
	s.Name = "build"
	h := newHarness(script)
	return runWrapped(context.Background(), h, s)["p:build"], h
}

func TestNoWrappersRunsPlainShell(t *testing.T) {
	t.Parallel()
	_, h := runOne(t, Section{Run: "r"}, map[string][]fakeCmd{"r": {ok("")}})
	if len(h.exec.argvs) != 1 || !slices.Equal(h.exec.argvs[0], []string{"sh", "-c", "r"}) {
		t.Errorf("argvs = %q", h.exec.argvs)
	}
	if len(h.steps.prepared) != 0 {
		t.Errorf("Prepare called without wrappers: %q", h.steps.prepared)
	}
}

func TestWrappedStepRunsPreparedArgv(t *testing.T) {
	t.Parallel()
	out, h := runWrappedOne(t, Section{Run: "r"}, map[string][]fakeCmd{"r": {ok("")}})
	if out.Status != Done || len(h.exec.argvs) != 1 || !slices.Equal(h.exec.argvs[0], []string{"fake-wrapped", "0-p-build", "r"}) {
		t.Errorf("outcome = %+v, argvs = %q", out, h.exec.argvs)
	}
	want := []string{"outer {{STEW_STEP}}", "inner {{STEW_STEP}}"}
	if len(h.steps.wrappers) != 1 || !slices.Equal(h.steps.wrappers[0], want) || !slices.Equal(h.steps.envs[0], h.exec.envs[0]) {
		t.Errorf("Prepare wrappers = %q, envs = %q", h.steps.wrappers, h.steps.envs)
	}
}

func TestWrapperFailureFailsEveryStep(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		s      Section
		script map[string][]fakeCmd
		want   string
	}{
		{"run unreached", Section{Run: "r"}, map[string][]fakeCmd{"r": {{unreached: true}}}, "wrapper did not run the command (exit 0)"},
		{"run twice", Section{Run: "r"}, map[string][]fakeCmd{"r": {{twice: true}}}, "wrapper ran the command 2 times (exit 0)"},
		{"verify unreached", Section{Run: "r", Verify: "v"}, map[string][]fakeCmd{"v": {{unreached: true}}, "r": {ok("")}},
			"wrapper did not run the command (exit 0)"},
		{"verify twice", Section{Run: "r", Verify: "v"}, map[string][]fakeCmd{"v": {{twice: true}}, "r": {ok("")}},
			"wrapper ran the command 2 times (exit 0)"},
		{"verify unreached after skip_if", Section{Run: "r", SkipIf: "v", Verify: "v"},
			map[string][]fakeCmd{"v": {bad(""), {unreached: true}}, "r": {ok("")}}, "wrapper did not run the command (exit 0)"},
		{"verify twice after skip_if", Section{Run: "r", SkipIf: "v", Verify: "v"},
			map[string][]fakeCmd{"v": {bad(""), {twice: true}}, "r": {ok("")}}, "wrapper ran the command 2 times (exit 0)"},
	} {
		if out, _ := runWrappedOne(t, tc.s, tc.script); out.Status != Fail || out.Cause != tc.want {
			t.Errorf("%s: outcome = %+v", tc.name, out)
		}
	}
}

func TestWrapperFailureInSkipIfFailsWithoutRun(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		v    fakeCmd
		want string
	}{
		{fakeCmd{wrapperExit: 1, unreached: true}, "wrapper did not run the command (exit 1)"},
		{fakeCmd{twice: true}, "wrapper ran the command 2 times (exit 0)"},
	} {
		out, h := runWrappedOne(t, Section{Run: "r", SkipIf: "v"}, map[string][]fakeCmd{"v": {tc.v}, "r": {ok("")}})
		if out.Status != Fail || out.Cause != tc.want {
			t.Errorf("outcome = %+v", out)
		}
		if !slices.Equal(stepNames(out), []string{"skip_if"}) || !slices.Equal(h.exec.calls, []string{"/w/p: v"}) {
			t.Errorf("steps = %q, calls = %q", stepNames(out), h.exec.calls)
		}
	}
}

func TestReachedFailingCommandHasPlainCause(t *testing.T) {
	t.Parallel()
	out, _ := runWrappedOne(t, Section{Run: "r"}, map[string][]fakeCmd{"r": {bad("")}})
	if out.Status != Fail || out.Cause != "exit 1" {
		t.Errorf("outcome = %+v", out)
	}
}

func TestReachedStepResultIsTheCommandsStatus(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		r      fakeCmd
		status Status
		cause  string
	}{
		{"failing command, wrapper exits 0", fakeCmd{exit: 7}, Fail, "exit 7"},
		{"passing command, wrapper exits 1", fakeCmd{wrapperExit: 1}, Done, ""},
		{"failing command, wrapper exits 2", fakeCmd{exit: 3, wrapperExit: 2}, Fail, "exit 3"},
	} {
		out, _ := runWrappedOne(t, Section{Run: "r"}, map[string][]fakeCmd{"r": {tc.r}})
		if out.Status != tc.status || out.Cause != tc.cause {
			t.Errorf("%s: outcome = %+v", tc.name, out)
		}
	}
}

func TestUnfinishedCommandFailsEveryStep(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		s      Section
		script map[string][]fakeCmd
		calls  []string
	}{
		{"run", Section{Run: "r"}, map[string][]fakeCmd{"r": {{unfinished: true}}}, []string{"/w/p: r"}},
		{"skip_if", Section{Run: "r", SkipIf: "v"}, map[string][]fakeCmd{"v": {{unfinished: true}}, "r": {ok("")}}, []string{"/w/p: v"}},
		{"verify", Section{Run: "r", Verify: "v"}, map[string][]fakeCmd{"v": {{unfinished: true}}, "r": {ok("")}},
			[]string{"/w/p: r", "/w/p: v"}},
		{"verify after skip_if", Section{Run: "r", SkipIf: "v", Verify: "v"},
			map[string][]fakeCmd{"v": {bad(""), {unfinished: true}}, "r": {ok("")}},
			[]string{"/w/p: v", "/w/p: r", "/w/p: v"}},
	} {
		out, h := runWrappedOne(t, tc.s, tc.script)
		if out.Status != Fail || out.Cause != "wrapper exited before the command finished (exit 0)" {
			t.Errorf("%s: outcome = %+v", tc.name, out)
		}
		if !slices.Equal(h.exec.calls, tc.calls) {
			t.Errorf("%s: calls = %q", tc.name, h.exec.calls)
		}
	}
}

func TestUnfinishedInterruptIsInterrupted(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancelCause(context.Background())
	h := newHarness(map[string][]fakeCmd{"r": {{unfinished: true, during: func() { cancel(ErrInterrupted) }}}})
	out := runWrapped(ctx, h, Section{Name: "build", Run: "r"})["p:build"]
	if out.Status != Interrupted || out.Cause != "wrapper exited before the command finished (signal SIGINT)" {
		t.Errorf("outcome = %+v", out)
	}
}

func TestStepsErrorsAreLogErrors(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		set  func(*fakeSteps)
	}{
		{"failPrepare", func(s *fakeSteps) { s.failPrepare = true }},
		{"failCollect", func(s *fakeSteps) { s.failCollect = true }},
	} {
		h := newHarness(map[string][]fakeCmd{"r": {ok("")}})
		tt.set(h.steps)
		if out := runWrapped(context.Background(), h, Section{Name: "build", Run: "r"})["p:build"]; out.Status != Fail || out.Cause != "log error: disk full" {
			t.Errorf("%s: outcome = %+v", tt.name, out)
		}
		if tt.name == "failPrepare" && len(h.exec.calls) != 0 {
			t.Errorf("failPrepare: exec.calls = %v, want none", h.exec.calls)
		}
	}
}

func TestUnreachedLogWriteErrorIsLogError(t *testing.T) {
	t.Parallel()
	h := newHarness(map[string][]fakeCmd{"r": {{unreached: true, stdout: "x"}}})
	h.logs.setup["p:build"] = func(l *fakeLog) { l.failWrite = true }
	if out := runWrapped(context.Background(), h, Section{Name: "build", Run: "r"})["p:build"]; out.Status != Fail || out.Cause != "log error: disk full" {
		t.Errorf("outcome = %+v", out)
	}
}

func TestUnreachedInterruptIsInterrupted(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancelCause(context.Background())
	h := newHarness(map[string][]fakeCmd{"r": {{unreached: true, during: func() { cancel(ErrInterrupted) }}}})
	if out := runWrapped(ctx, h, Section{Name: "build", Run: "r"})["p:build"]; out.Status != Interrupted || out.Cause != "wrapper did not run the command (signal SIGINT)" {
		t.Errorf("outcome = %+v", out)
	}
}

func TestStepKeysDifferPerSection(t *testing.T) {
	t.Parallel()
	h := newHarness(map[string][]fakeCmd{"s": {ok("")}, "b": {ok("")}, "q": {ok("")}})
	runWrapped(context.Background(), h,
		Section{Name: "setup", Run: "s"}, Section{Name: "build", Run: "b"}, Section{Name: "ci.quick", Run: "q"})
	if want := []string{"0-p-setup", "1-p-build", "2-p-ci.quick"}; !slices.Equal(h.steps.prepared, want) {
		t.Errorf("keys = %q, want %q", h.steps.prepared, want)
	}
}

func TestBlockedRecordErrorChangesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(map[string][]fakeCmd{"x": {{exit: 2}}, "y": {ok("")}})
	h.record.failBlocked = true
	res := h.r.Run(context.Background(), plan(sec("a", "build", "x"), sec("b", "build", "y", "a:build")))
	if !slices.Contains(h.rec.events, "blocked b:build by a:build") {
		t.Errorf("events = %q", h.rec.events)
	}
	if res.ExitCode() != 1 {
		t.Errorf("exit = %d", res.ExitCode())
	}
	col := 0
	for i, c := range res.Columns {
		if c == "build" {
			col = i
		}
	}
	for _, row := range res.Rows {
		if row.Project == "b" && row.Cells[col] != Blocked {
			t.Errorf("b cell = %q, want Blocked", row.Cells[col])
		}
	}
}
