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

func setup(run, verify string) Phase {
	return Phase{Name: "setup", Used: "setup", Run: run, Verify: verify}
}
func build(run, verify string) Phase {
	return Phase{Name: "build", Used: "build", Run: run, Verify: verify}
}
func ci(name, used, run string) Phase {
	return Phase{Name: "ci." + name, Used: "ci." + used, Run: run, CI: true}
}

// runOne runs a single-phase plan for project "p" and returns its outcome.
func runOne(t *testing.T, ph Phase, script map[string][]fakeCmd) (Outcome, *harness) {
	t.Helper()
	h := newHarness(script)
	h.r.Run(context.Background(), Plan{
		Columns: []string{ph.Name},
		Jobs:    []Job{{Project: "p", Dir: "/w/p", Phases: []Phase{ph}}},
	})
	return h.rec.outcomes["p "+ph.Name], h
}

func stepNames(out Outcome) []string {
	var s []string
	for _, st := range out.Steps {
		s = append(s, st.Step)
	}
	return s
}

func TestPhaseAlgorithm(t *testing.T) {
	tests := []struct {
		name      string
		phase     Phase
		script    map[string][]fakeCmd
		status    Status
		calls     []string
		replayed  []string
		cause     string
		wantFiles bool
	}{
		{"both empty", build("", ""), nil, Skip, nil, nil, "", false},
		{"run ok", build("r", ""), map[string][]fakeCmd{"r": {ok("")}}, Done,
			[]string{"r"}, nil, "", true},
		{"run fails", build("r", ""), map[string][]fakeCmd{"r": {{exit: 3}}}, Fail,
			[]string{"r"}, []string{"run"}, "exit 3", true},
		{"verify passes", build("r", "v"), map[string][]fakeCmd{"v": {ok("")}}, Skip,
			[]string{"v"}, nil, "", true},
		{"verify fails, run ok, verify ok", build("r", "v"),
			map[string][]fakeCmd{"v": {bad(""), ok("")}, "r": {ok("")}}, Done,
			[]string{"v", "r", "v"}, nil, "", true},
		{"verify fails, run fails", build("r", "v"),
			map[string][]fakeCmd{"v": {bad("")}, "r": {{exit: 2}}}, Fail,
			[]string{"v", "r"}, []string{"run"}, "exit 2", true},
		{"verify fails after run", build("r", "v"),
			map[string][]fakeCmd{"v": {bad(""), {exit: 4}}, "r": {ok("")}}, Fail,
			[]string{"v", "r", "v"}, []string{"run", "verify after run"}, "exit 4", true},
		{"assertion passes", build("", "v"), map[string][]fakeCmd{"v": {ok("")}}, Skip,
			[]string{"v"}, nil, "", true},
		{"assertion fails", build("", "v"), map[string][]fakeCmd{"v": {{exit: 1}}}, Fail,
			[]string{"v"}, []string{"verify"}, "exit 1", true},
		{"ci passes", ci("full", "full", "t"), map[string][]fakeCmd{"t": {ok("")}}, Pass,
			[]string{"t"}, nil, "", true},
		{"ci empty", ci("full", "full", ""), nil, Skip, nil, nil, "", false},
		{"ci fails", ci("full", "full", "t"), map[string][]fakeCmd{"t": {{exit: 1}}}, Fail,
			[]string{"t"}, []string{"run"}, "exit 1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, h := runOne(t, tt.phase, tt.script)
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
			if _, opened := h.logs.logs["p-"+tt.phase.Used]; opened != tt.wantFiles {
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
	_, h := runOne(t, build("r", ""), map[string][]fakeCmd{"r": {ok("")}})
	if !slices.Equal(h.exec.calls, []string{"/w/p: r"}) {
		t.Errorf("calls = %q", h.exec.calls)
	}
}

func TestReplayAndLogs(t *testing.T) {
	out, h := runOne(t, build("r", "v"), map[string][]fakeCmd{
		"v": {{exit: 1, stdout: "pre-verify noise\n"}, {exit: 1, stdout: "vout\n", stderr: "verr\n"}},
		"r": {{stdout: "rout\n", stderr: "rerr\n"}},
	})
	if out.Status != Fail {
		t.Fatalf("status = %s", out.Status)
	}
	want := []StepOutput{
		{Step: "run", Cmd: "r", Output: []byte("rout\nrerr\n")},
		{Step: "verify after run", Cmd: "v", Output: []byte("vout\nverr\n")},
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

	l := h.logs.logs["p-build"]
	wantOut := "--- stew: verify: v\npre-verify noise\n--- stew: run: r\nrout\n--- stew: verify after run: v\nvout\n"
	wantErr := "--- stew: verify: v\n--- stew: run: r\nrerr\n--- stew: verify after run: v\nverr\n"
	if l.stdout.String() != wantOut {
		t.Errorf("stdout log = %q, want %q", l.stdout.String(), wantOut)
	}
	if l.stderr.String() != wantErr {
		t.Errorf("stderr log = %q, want %q", l.stderr.String(), wantErr)
	}
}

func TestDurationSpansAllSteps(t *testing.T) {
	out, _ := runOne(t, build("r", "v"), map[string][]fakeCmd{
		"v": {{exit: 1, took: 1 * time.Second}, {took: 3 * time.Second}},
		"r": {{took: 2 * time.Second}},
	})
	if out.Status != Done || out.Duration != 6*time.Second {
		t.Errorf("status %s, duration %v; want done, 6s", out.Status, out.Duration)
	}
}

func TestCILogUsesResolvedLevel(t *testing.T) {
	out, h := runOne(t, ci("pre-commit", "quick", "lint"), map[string][]fakeCmd{"lint": {ok("")}})
	if out.Status != Pass {
		t.Fatalf("status = %s", out.Status)
	}
	if _, ok := h.logs.logs["p-ci.quick"]; !ok {
		t.Errorf("logs = %v, want p-ci.quick", h.logs.logs)
	}
}

// diamond is app -> {api, backend} -> core, plus independent lib, all running setup and build.
func diamond() Plan {
	job := func(name string, deps ...string) Job {
		return Job{Project: name, Dir: "/w/" + name, Deps: deps, Phases: []Phase{
			setup(name+"-setup", ""), build(name+"-build", ""),
		}}
	}
	return Plan{
		Columns: []string{"setup", "build"},
		Jobs:    []Job{job("core"), job("api", "core"), job("backend", "core"), job("app", "backend", "api"), job("lib")},
	}
}

func okScript(names ...string) map[string][]fakeCmd {
	s := map[string][]fakeCmd{}
	for _, n := range names {
		s[n+"-setup"] = []fakeCmd{ok("")}
		s[n+"-build"] = []fakeCmd{ok("")}
	}
	return s
}

func TestBlockedByDirectDependencies(t *testing.T) {
	script := okScript("core", "api", "backend", "app", "lib")
	script["core-setup"] = []fakeCmd{{exit: 2}}
	h := newHarness(script)
	res := h.r.Run(context.Background(), diamond())

	want := []string{
		"start core setup", "end core setup fail",
		"blocked api setup by core",
		"blocked backend setup by core",
		"blocked app setup by api, backend",
		"start lib setup", "end lib setup done",
		"start lib build", "end lib build done",
	}
	if !slices.Equal(h.rec.events, want) {
		t.Errorf("events:\n%s\nwant:\n%s", strings.Join(h.rec.events, "\n"), strings.Join(want, "\n"))
	}

	cells := map[string][]Status{}
	for _, row := range res.Rows {
		for _, c := range row.Cells {
			cells[row.Project] = append(cells[row.Project], c.Status)
		}
	}
	wantCells := map[string][]Status{
		"core": {Fail, ""}, "api": {Blocked, ""}, "backend": {Blocked, ""}, "app": {Blocked, ""}, "lib": {Done, Done},
	}
	for p, w := range wantCells {
		if !slices.Equal(cells[p], w) {
			t.Errorf("cells[%s] = %q, want %q", p, cells[p], w)
		}
	}
	if !res.Failed || res.Interrupted != 0 || res.ExitCode() != 1 {
		t.Errorf("failed=%v interrupted=%v exit=%d", res.Failed, res.Interrupted, res.ExitCode())
	}
}

func TestFailureStopsOnlyThatProject(t *testing.T) {
	script := okScript("core", "api", "backend", "app", "lib")
	script["api-build"] = []fakeCmd{{exit: 1}}
	h := newHarness(script)
	res := h.r.Run(context.Background(), diamond())

	want := []string{
		"start core setup", "end core setup done", "start core build", "end core build done",
		"start api setup", "end api setup done", "start api build", "end api build fail",
		"start backend setup", "end backend setup done", "start backend build", "end backend build done",
		"blocked app setup by api",
		"start lib setup", "end lib setup done", "start lib build", "end lib build done",
	}
	if !slices.Equal(h.rec.events, want) {
		t.Errorf("events:\n%s\nwant:\n%s", strings.Join(h.rec.events, "\n"), strings.Join(want, "\n"))
	}
	if res.ExitCode() != 1 {
		t.Errorf("exit = %d", res.ExitCode())
	}
}

func TestAllPass(t *testing.T) {
	h := newHarness(okScript("core", "api", "backend", "app", "lib"))
	res := h.r.Run(context.Background(), diamond())
	if res.Failed || res.Interrupted != 0 || res.ExitCode() != 0 {
		t.Errorf("failed=%v interrupted=%v exit=%d", res.Failed, res.Interrupted, res.ExitCode())
	}
	if len(h.exec.calls) != 10 {
		t.Errorf("calls = %d, want 10", len(h.exec.calls))
	}
}

func TestFallbackCell(t *testing.T) {
	h := newHarness(map[string][]fakeCmd{"lint": {ok("")}})
	res := h.r.Run(context.Background(), Plan{
		Columns: []string{"setup", "build", "ci.pre-commit"},
		Jobs: []Job{
			{Project: "dep", Phases: []Phase{setup("", ""), build("", "")}},
			{Project: "p", Deps: []string{"dep"}, Phases: []Phase{setup("", ""), build("", ""), ci("pre-commit", "quick", "lint")}},
		},
	})
	if got := res.Rows[0].Cells; got[2] != (Cell{}) {
		t.Errorf("dependency-only CI cell = %+v, want zero", got[2])
	}
	if got := res.Rows[1].Cells[2]; got != (Cell{Status: Pass, Fallback: "quick"}) {
		t.Errorf("CI cell = %+v", got)
	}
}

func TestLogOpenFailure(t *testing.T) {
	h := newHarness(okScript("core", "api", "backend", "app", "lib"))
	h.logs.openErr["core-setup"] = true
	res := h.r.Run(context.Background(), diamond())

	out := h.rec.outcomes["core setup"]
	if out.Status != Fail || out.Cause != "log error: permission denied" {
		t.Errorf("outcome = %+v", out)
	}
	for _, c := range h.exec.calls {
		if strings.HasPrefix(c, "/w/core:") {
			t.Errorf("core command ran without a log: %s", c)
		}
	}
	if !slices.Contains(h.rec.events, "blocked api setup by core") || !slices.Contains(h.rec.events, "end lib build done") {
		t.Errorf("events = %q", h.rec.events)
	}
	if res.ExitCode() != 1 {
		t.Errorf("exit = %d", res.ExitCode())
	}
}

func TestLogWriteFailureCancelsCommand(t *testing.T) {
	script := okScript("core", "api", "backend", "app", "lib")
	script["core-setup"] = []fakeCmd{{stdout: "some output\n"}}
	h := newHarness(script)
	h.logs.setup["core-setup"] = func(l *fakeLog) { l.failWrite = true }
	h.r.Run(context.Background(), diamond())

	out := h.rec.outcomes["core setup"]
	if out.Status != Fail || out.Cause != "log error: disk full" {
		t.Errorf("outcome = %+v", out)
	}
	if !slices.Equal(h.exec.cancelled, []string{"core-setup"}) {
		t.Errorf("cancelled = %q, want the running command", h.exec.cancelled)
	}
	if len(out.Steps) != 1 || string(out.Steps[0].Output) != "some output\n" {
		t.Errorf("replay = %+v", out.Steps)
	}
	if !slices.Contains(h.rec.events, "blocked api setup by core") || !slices.Contains(h.rec.events, "end lib build done") {
		t.Errorf("events = %q", h.rec.events)
	}
}

func TestLogMarkerFailure(t *testing.T) {
	h := newHarness(okScript("core", "api", "backend", "app", "lib"))
	h.logs.setup["core-setup"] = func(l *fakeLog) { l.failMarker = true }
	h.r.Run(context.Background(), diamond())

	out := h.rec.outcomes["core setup"]
	if out.Status != Fail || out.Cause != "log error: disk full" {
		t.Errorf("outcome = %+v", out)
	}
	for _, c := range h.exec.calls {
		if strings.HasPrefix(c, "/w/core:") {
			t.Errorf("core command ran without a marker: %s", c)
		}
	}
}

func TestInterrupt(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	script := okScript("core", "api", "backend", "app", "lib")
	script["api-setup"] = []fakeCmd{{stdout: "partial\n", during: func() { cancel(ErrInterrupted) }}}
	h := newHarness(script)
	res := h.r.Run(ctx, diamond())

	want := []string{
		"start core setup", "end core setup done", "start core build", "end core build done",
		"start api setup", "end api setup interrupted",
	}
	if !slices.Equal(h.rec.events, want) {
		t.Errorf("events:\n%s\nwant:\n%s", strings.Join(h.rec.events, "\n"), strings.Join(want, "\n"))
	}
	out := h.rec.outcomes["api setup"]
	if out.Cause != "signal SIGINT" || len(out.Steps) != 1 || string(out.Steps[0].Output) != "partial\n" {
		t.Errorf("outcome = %+v", out)
	}
	if res.Interrupted != syscall.SIGINT || res.ExitCode() != 130 {
		t.Errorf("interrupted=%v exit=%d", res.Interrupted, res.ExitCode())
	}
	if len(res.Rows) != 5 {
		t.Errorf("rows = %d, want all 5 projects", len(res.Rows))
	}
	if res.Rows[1].Cells[1] != (Cell{}) || res.Rows[4].Cells[0] != (Cell{}) {
		t.Errorf("unreached cells not empty: %+v %+v", res.Rows[1].Cells, res.Rows[4].Cells)
	}
}

func TestInterruptSIGTERM(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	script := okScript("core", "api", "backend", "app", "lib")
	script["api-setup"] = []fakeCmd{{stdout: "partial\n", during: func() { cancel(Interrupt{Signal: syscall.SIGTERM}) }}}
	h := newHarness(script)
	res := h.r.Run(ctx, diamond())

	want := []string{
		"start core setup", "end core setup done", "start core build", "end core build done",
		"start api setup", "end api setup interrupted",
	}
	if !slices.Equal(h.rec.events, want) {
		t.Errorf("events:\n%s\nwant:\n%s", strings.Join(h.rec.events, "\n"), strings.Join(want, "\n"))
	}
	out := h.rec.outcomes["api setup"]
	if out.Cause != "signal SIGTERM" || len(out.Steps) != 1 || string(out.Steps[0].Output) != "partial\n" {
		t.Errorf("outcome = %+v", out)
	}
	if res.Interrupted != syscall.SIGTERM || res.ExitCode() != 143 {
		t.Errorf("interrupted=%v exit=%d", res.Interrupted, res.ExitCode())
	}
	if len(res.Rows) != 5 {
		t.Errorf("rows = %d, want all 5 projects", len(res.Rows))
	}
	if res.Rows[1].Cells[1] != (Cell{}) || res.Rows[4].Cells[0] != (Cell{}) {
		t.Errorf("unreached cells not empty: %+v %+v", res.Rows[1].Cells, res.Rows[4].Cells)
	}
}

func TestInterruptBetweenPhases(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	script := okScript("core", "api", "backend", "app", "lib")
	h := newHarness(script)
	// Cancel after core's setup finished, before its build starts.
	h.r.Report = &cancelAfter{recorder: h.rec, event: "end core setup done", cancel: func() { cancel(ErrInterrupted) }}
	res := h.r.Run(ctx, diamond())

	if want := []string{"start core setup", "end core setup done"}; !slices.Equal(h.rec.events, want) {
		t.Errorf("events = %q, want %q", h.rec.events, want)
	}
	if res.ExitCode() != 130 {
		t.Errorf("exit = %d", res.ExitCode())
	}
}

func TestInterruptBeforeBlockedJobs(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	script := okScript("core", "api", "backend", "app")
	script["core-setup"] = []fakeCmd{{exit: 2}}
	h := newHarness(script)
	// Ctrl-C lands while the failed phase is being reported; every later job would be blocked.
	h.r.Report = &cancelAfter{recorder: h.rec, event: "end core setup fail", cancel: func() { cancel(ErrInterrupted) }}
	plan := diamond()
	plan.Jobs = plan.Jobs[:4]
	res := h.r.Run(ctx, plan)

	if want := []string{"start core setup", "end core setup fail"}; !slices.Equal(h.rec.events, want) {
		t.Errorf("events = %q, want %q", h.rec.events, want)
	}
	for _, row := range res.Rows[1:] {
		for _, c := range row.Cells {
			if c != (Cell{}) {
				t.Errorf("%s: unreached cell = %+v, want zero", row.Project, c)
			}
		}
	}
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

func (c *cancelAfter) PhaseEnd(project string, ph Phase, out Outcome) {
	c.recorder.PhaseEnd(project, ph, out)
	if c.events[len(c.events)-1] == c.event {
		c.cancel()
	}
}

func TestResultCause(t *testing.T) {
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
	script := okScript("core", "api", "backend", "app", "lib")
	script["core-setup"] = []fakeCmd{{exit: 2}}
	h := newHarness(script)
	h.r.Run(context.Background(), diamond())

	want := []string{
		"end core setup fail",
		"blocked api setup by core",
		"blocked backend setup by core",
		"blocked app setup by api, backend",
		"end lib setup done",
		"end lib build done",
	}
	if !slices.Equal(h.record.calls, want) {
		t.Errorf("record calls:\n%s\nwant:\n%s", strings.Join(h.record.calls, "\n"), strings.Join(want, "\n"))
	}
}

func TestRecordErrorFailsPhase(t *testing.T) {
	h := newHarness(okScript("core", "api", "backend", "app", "lib"))
	h.record.failPhase["core setup"] = true
	res := h.r.Run(context.Background(), diamond())

	out := h.rec.outcomes["core setup"]
	if out.Status != Fail || out.Cause != "log error: read-only file system" || out.Duration != time.Second {
		t.Errorf("outcome = %+v", out)
	}
	if !slices.Contains(h.rec.events, "blocked api setup by core") || !slices.Contains(h.rec.events, "end lib build done") {
		t.Errorf("events = %q", h.rec.events)
	}
	if slices.Contains(h.rec.events, "start core build") {
		t.Errorf("core kept running after its record failed: %q", h.rec.events)
	}
	if res.ExitCode() != 1 {
		t.Errorf("exit = %d", res.ExitCode())
	}
}

func TestRecordErrorKeepsFailCause(t *testing.T) {
	script := okScript("core", "api", "backend", "app", "lib")
	script["core-setup"] = []fakeCmd{{exit: 2}}
	h := newHarness(script)
	h.record.failPhase["core setup"] = true
	h.r.Run(context.Background(), diamond())
	if out := h.rec.outcomes["core setup"]; out.Status != Fail || out.Cause != "exit 2" {
		t.Errorf("outcome = %+v", out)
	}
}

func TestRecordErrorKeepsInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	script := okScript("core", "api", "backend", "app", "lib")
	script["core-setup"] = []fakeCmd{{during: func() { cancel(ErrInterrupted) }}}
	h := newHarness(script)
	h.record.failPhase["core setup"] = true
	h.r.Run(ctx, diamond())
	if out := h.rec.outcomes["core setup"]; out.Status != Interrupted {
		t.Errorf("outcome = %+v", out)
	}
	if !slices.Equal(h.record.calls, []string{"end core setup interrupted"}) {
		t.Errorf("record calls after interrupt = %q", h.record.calls)
	}
}

func TestStewEnv(t *testing.T) {
	h := newHarness(map[string][]fakeCmd{"v": {bad(""), ok("")}, "r": {ok("")}, "q": {ok("")}})
	h.r.Run(context.Background(), Plan{
		Columns: []string{"setup", "ci.pre-commit"},
		Jobs: []Job{{Project: "my.lib", Dir: "/w/lib", Phases: []Phase{
			setup("r", "v"),
			ci("pre-commit", "quick", "q"),
		}}},
	})
	env := func(phase string) []string {
		return []string{
			"STEW_RUN_ID=20260101T000000Z-abcd",
			"STEW_ROOT=/w",
			"STEW_PROJECT=my.lib",
			"STEW_PHASE=" + phase,
			"STEW_TAG=my.lib." + phase,
		}
	}
	want := [][]string{env("setup"), env("setup"), env("setup"), env("ci.quick")}
	if !slices.EqualFunc(h.exec.envs, want, slices.Equal) {
		t.Errorf("envs = %q\nwant %q", h.exec.envs, want)
	}
}

// runWrapped runs a plan with one wrapped job "p" of phases on h and returns h's outcomes.
func runWrapped(ctx context.Context, h *harness, phases ...Phase) map[string]Outcome {
	var columns []string
	for _, ph := range phases {
		columns = append(columns, ph.Name)
	}
	h.r.Run(ctx, Plan{
		Columns: columns,
		Jobs:    []Job{{Project: "p", Dir: "/w/p", Wrappers: []string{"outer {{STEW_STEP}}", "inner {{STEW_STEP}}"}, Phases: phases}},
	})
	return h.rec.outcomes
}

// runWrappedOne is runOne with wrappers on the job.
func runWrappedOne(t *testing.T, ph Phase, script map[string][]fakeCmd) (Outcome, *harness) {
	t.Helper()
	h := newHarness(script)
	return runWrapped(context.Background(), h, ph)["p "+ph.Name], h
}

func TestNoWrappersRunsPlainShell(t *testing.T) {
	_, h := runOne(t, build("r", ""), map[string][]fakeCmd{"r": {ok("")}})
	if len(h.exec.argvs) != 1 || !slices.Equal(h.exec.argvs[0], []string{"sh", "-c", "r"}) {
		t.Errorf("argvs = %q", h.exec.argvs)
	}
	if len(h.steps.prepared) != 0 {
		t.Errorf("Prepare called without wrappers: %q", h.steps.prepared)
	}
}

func TestWrappedStepRunsPreparedArgv(t *testing.T) {
	out, h := runWrappedOne(t, build("r", ""), map[string][]fakeCmd{"r": {ok("")}})
	if out.Status != Done || len(h.exec.argvs) != 1 || !slices.Equal(h.exec.argvs[0], []string{"fake-wrapped", "p-build", "r"}) {
		t.Errorf("outcome = %+v, argvs = %q", out, h.exec.argvs)
	}
	want := []string{"outer {{STEW_STEP}}", "inner {{STEW_STEP}}"}
	if len(h.steps.wrappers) != 1 || !slices.Equal(h.steps.wrappers[0], want) || !slices.Equal(h.steps.envs[0], h.exec.envs[0]) {
		t.Errorf("Prepare wrappers = %q, envs = %q", h.steps.wrappers, h.steps.envs)
	}
}

func TestWrapperFailureFailsEveryStep(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ph     Phase
		script map[string][]fakeCmd
		want   string
	}{
		{"run unreached", build("r", ""), map[string][]fakeCmd{"r": {{unreached: true}}}, "wrapper did not run the command (exit 0)"},
		{"run twice", build("r", ""), map[string][]fakeCmd{"r": {{twice: true}}}, "wrapper ran the command 2 times (exit 0)"},
		{"verify after run unreached", build("r", "v"), map[string][]fakeCmd{"v": {bad(""), {unreached: true}}, "r": {ok("")}},
			"wrapper did not run the command (exit 0)"},
		{"verify after run twice", build("r", "v"), map[string][]fakeCmd{"v": {bad(""), {twice: true}}, "r": {ok("")}},
			"wrapper ran the command 2 times (exit 0)"},
	} {
		if out, _ := runWrappedOne(t, tc.ph, tc.script); out.Status != Fail || out.Cause != tc.want {
			t.Errorf("%s: outcome = %+v", tc.name, out)
		}
	}
}

func TestWrapperFailureInPreRunVerifyFailsWithoutRun(t *testing.T) {
	for _, tc := range []struct {
		v    fakeCmd
		want string
	}{
		{fakeCmd{wrapperExit: 1, unreached: true}, "wrapper did not run the command (exit 1)"},
		{fakeCmd{twice: true}, "wrapper ran the command 2 times (exit 0)"},
	} {
		out, h := runWrappedOne(t, build("r", "v"), map[string][]fakeCmd{"v": {tc.v}, "r": {ok("")}})
		if out.Status != Fail || out.Cause != tc.want {
			t.Errorf("outcome = %+v", out)
		}
		if !slices.Equal(stepNames(out), []string{"verify"}) || !slices.Equal(h.exec.calls, []string{"/w/p: v"}) {
			t.Errorf("steps = %q, calls = %q", stepNames(out), h.exec.calls)
		}
	}
}

func TestReachedFailingCommandHasPlainCause(t *testing.T) {
	out, _ := runWrappedOne(t, build("r", ""), map[string][]fakeCmd{"r": {bad("")}})
	if out.Status != Fail || out.Cause != "exit 1" {
		t.Errorf("outcome = %+v", out)
	}
}

func TestReachedStepResultIsTheCommandsStatus(t *testing.T) {
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
		out, _ := runWrappedOne(t, build("r", ""), map[string][]fakeCmd{"r": {tc.r}})
		if out.Status != tc.status || out.Cause != tc.cause {
			t.Errorf("%s: outcome = %+v", tc.name, out)
		}
	}
}

func TestUnfinishedCommandFailsEveryStep(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ph     Phase
		script map[string][]fakeCmd
		calls  []string
	}{
		{"run", build("r", ""), map[string][]fakeCmd{"r": {{unfinished: true}}}, []string{"/w/p: r"}},
		{"pre-run verify", build("r", "v"), map[string][]fakeCmd{"v": {{unfinished: true}}, "r": {ok("")}}, []string{"/w/p: v"}},
		{"verify after run", build("r", "v"), map[string][]fakeCmd{"v": {bad(""), {unfinished: true}}, "r": {ok("")}},
			[]string{"/w/p: v", "/w/p: r", "/w/p: v"}},
	} {
		out, h := runWrappedOne(t, tc.ph, tc.script)
		if out.Status != Fail || out.Cause != "wrapper exited before the command finished (exit 0)" {
			t.Errorf("%s: outcome = %+v", tc.name, out)
		}
		if !slices.Equal(h.exec.calls, tc.calls) {
			t.Errorf("%s: calls = %q", tc.name, h.exec.calls)
		}
	}
}

func TestUnfinishedInterruptIsInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	h := newHarness(map[string][]fakeCmd{"r": {{unfinished: true, during: func() { cancel(ErrInterrupted) }}}})
	out := runWrapped(ctx, h, build("r", ""))["p build"]
	if out.Status != Interrupted || out.Cause != "wrapper exited before the command finished (signal SIGINT)" {
		t.Errorf("outcome = %+v", out)
	}
}

func TestStepsErrorsAreLogErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		set  func(*fakeSteps)
	}{
		{"failPrepare", func(s *fakeSteps) { s.failPrepare = true }},
		{"failCollect", func(s *fakeSteps) { s.failCollect = true }},
	} {
		h := newHarness(map[string][]fakeCmd{"r": {ok("")}})
		tt.set(h.steps)
		if out := runWrapped(context.Background(), h, build("r", ""))["p build"]; out.Status != Fail || out.Cause != "log error: disk full" {
			t.Errorf("%s: outcome = %+v", tt.name, out)
		}
		if tt.name == "failPrepare" && len(h.exec.calls) != 0 {
			t.Errorf("failPrepare: exec.calls = %v, want none", h.exec.calls)
		}
	}
}

func TestUnreachedLogWriteErrorIsLogError(t *testing.T) {
	h := newHarness(map[string][]fakeCmd{"r": {{unreached: true, stdout: "x"}}})
	h.logs.setup["p-build"] = func(l *fakeLog) { l.failWrite = true }
	if out := runWrapped(context.Background(), h, build("r", ""))["p build"]; out.Status != Fail || out.Cause != "log error: disk full" {
		t.Errorf("outcome = %+v", out)
	}
}

func TestUnreachedInterruptIsInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	h := newHarness(map[string][]fakeCmd{"r": {{unreached: true, during: func() { cancel(ErrInterrupted) }}}})
	if out := runWrapped(ctx, h, build("r", ""))["p build"]; out.Status != Interrupted || out.Cause != "wrapper did not run the command (signal SIGINT)" {
		t.Errorf("outcome = %+v", out)
	}
}

func TestStepKeysDifferPerPhase(t *testing.T) {
	h := newHarness(map[string][]fakeCmd{"s": {ok("")}, "b": {ok("")}, "q": {ok("")}})
	runWrapped(context.Background(), h, setup("s", ""), build("b", ""), ci("pre-commit", "quick", "q"))
	if want := []string{"p-setup", "p-build", "p-ci.quick"}; !slices.Equal(h.steps.prepared, want) {
		t.Errorf("keys = %q, want %q", h.steps.prepared, want)
	}
}

func TestBlockedRecordErrorChangesNothing(t *testing.T) {
	script := okScript("core", "api", "backend", "app", "lib")
	script["core-setup"] = []fakeCmd{{exit: 2}}
	h := newHarness(script)
	h.record.failBlocked = true
	res := h.r.Run(context.Background(), diamond())
	if !slices.Contains(h.rec.events, "blocked app setup by api, backend") || !slices.Contains(h.rec.events, "end lib build done") {
		t.Errorf("events = %q", h.rec.events)
	}
	if res.ExitCode() != 1 {
		t.Errorf("exit = %d", res.ExitCode())
	}
}
