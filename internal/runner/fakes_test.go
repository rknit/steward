package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// fakeClock advances only when a fake command runs.
type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

// fakeCmd is one scripted execution of a command.
type fakeCmd struct {
	exit           int
	stdout, stderr string
	took           time.Duration
	during         func() // runs while the command "executes", e.g. to simulate Ctrl-C
	unreached      bool   // a wrapper exits without running the command
	twice          bool   // a wrapper runs the command twice
	unfinished     bool   // a wrapper runs the command once but exits before it finishes
	wrapperExit    int    // the outermost wrapper's own exit code; exit is the command's
}

// fakeExec runs scripted commands. Each command string maps to a queue of executions; the last one repeats.
type fakeExec struct {
	clock     *fakeClock
	script    map[string][]fakeCmd
	calls     []string
	envs      [][]string // aligned with calls
	argvs     [][]string // aligned with calls
	cancelled []string
	steps     *fakeSteps
}

func (f *fakeExec) Run(ctx context.Context, dir string, env []string, argv []string, stdout, stderr io.Writer) Result {
	cmd := argv[len(argv)-1]
	if cmd == "" {
		panic("executor called with an empty command")
	}
	f.calls = append(f.calls, dir+": "+cmd)
	f.envs = append(f.envs, env)
	f.argvs = append(f.argvs, argv)
	queue := f.script[cmd]
	if len(queue) == 0 {
		panic("unscripted command: " + cmd)
	}
	c := queue[0]
	if len(queue) > 1 {
		f.script[cmd] = queue[1:]
	}
	wrapped := argv[0] == "fake-wrapped"
	if wrapped {
		key := argv[1]
		switch {
		case c.unreached:
			f.steps.reaches[key] = 0
		case c.twice:
			f.steps.reaches[key] = 2
		default:
			f.steps.reaches[key] = 1
		}
		if !c.unreached && !c.unfinished {
			f.steps.statuses[key] = c.exit
		}
	}
	f.clock.t = f.clock.t.Add(c.took)
	io.WriteString(stdout, c.stdout)
	io.WriteString(stderr, c.stderr)
	if c.during != nil {
		c.during()
	}
	if ctx.Err() != nil {
		f.cancelled = append(f.cancelled, cmd)
		var i Interrupt
		if errors.As(context.Cause(ctx), &i) {
			return Result{Signal: signalName(i.Signal)}
		}
		return Result{Signal: "SIGTERM"}
	}
	if wrapped {
		return Result{ExitCode: c.wrapperExit}
	}
	return Result{ExitCode: c.exit}
}

// fakeLog records markers and output; failWrite makes output writes fail.
type fakeLog struct {
	stdout, stderr bytes.Buffer
	failWrite      bool
	failMarker     bool
	closed         bool
}

type failingWriter struct {
	w    *bytes.Buffer
	fail *bool
}

func (w failingWriter) Write(p []byte) (int, error) {
	if *w.fail {
		return 0, errors.New("disk full")
	}
	return w.w.Write(p)
}

func (l *fakeLog) Stdout() io.Writer { return failingWriter{&l.stdout, &l.failWrite} }
func (l *fakeLog) Stderr() io.Writer { return failingWriter{&l.stderr, &l.failWrite} }
func (l *fakeLog) Marker(step, cmd string) error {
	if l.failMarker {
		return errors.New("disk full")
	}
	line := "--- stew: " + step + ": " + cmd + "\n"
	l.stdout.WriteString(line)
	l.stderr.WriteString(line)
	return nil
}
func (l *fakeLog) Close() error { l.closed = true; return nil }

// fakeSteps stands in for StepDir. Its argv "fake-wrapped <key> <cmd>" tells fakeExec to record reaches and
// the command's status for key.
type fakeSteps struct {
	prepared    []string   // keys
	wrappers    [][]string // aligned with prepared
	envs        [][]string // aligned with prepared
	reaches     map[string]int
	statuses    map[string]int // keys whose command finished
	failPrepare bool
	failCollect bool
}

func (f *fakeSteps) Prepare(key string, wrappers, env []string, cmd string) ([]string, error) {
	if f.failPrepare {
		return nil, errors.New("disk full")
	}
	f.prepared = append(f.prepared, key)
	f.wrappers = append(f.wrappers, wrappers)
	f.envs = append(f.envs, env)
	return []string{"fake-wrapped", key, cmd}, nil
}

func (f *fakeSteps) Collect(key string) (reaches, status int, finished bool, err error) {
	if f.failCollect {
		return 0, 0, false, errors.New("disk full")
	}
	reaches = f.reaches[key]
	status, finished = f.statuses[key]
	delete(f.reaches, key)
	delete(f.statuses, key)
	return reaches, status, finished, nil
}

// fakeLogs hands out fakeLogs by "<project>-<phase>".
type fakeLogs struct {
	logs    map[string]*fakeLog
	openErr map[string]bool
	setup   map[string]func(*fakeLog)
}

func newFakeLogs() *fakeLogs {
	return &fakeLogs{logs: map[string]*fakeLog{}, openErr: map[string]bool{}, setup: map[string]func(*fakeLog){}}
}

func (f *fakeLogs) Open(project, phase string) (PhaseLog, error) {
	key := project + "-" + phase
	if f.openErr[key] {
		return nil, errors.New("permission denied")
	}
	if _, ok := f.logs[key]; ok {
		panic("log opened twice: " + key)
	}
	l := &fakeLog{}
	if s := f.setup[key]; s != nil {
		s(l)
	}
	f.logs[key] = l
	return l, nil
}

// recorder records reporter events as readable lines.
type recorder struct {
	events   []string
	outcomes map[string]Outcome
}

func (r *recorder) PhaseStart(project string, ph Phase) {
	r.events = append(r.events, fmt.Sprintf("start %s %s", project, ph.Name))
}

func (r *recorder) PhaseEnd(project string, ph Phase, out Outcome) {
	if r.outcomes == nil {
		r.outcomes = map[string]Outcome{}
	}
	r.outcomes[project+" "+ph.Name] = out
	r.events = append(r.events, fmt.Sprintf("end %s %s %s", project, ph.Name, out.Status))
}

func (r *recorder) Blocked(project string, ph Phase, by []string) {
	r.events = append(r.events, fmt.Sprintf("blocked %s %s by %s", project, ph.Name, strings.Join(by, ", ")))
}

// fakeRecord records Recorder calls. failPhase makes PhaseEnd fail for "<project> <phase>".
type fakeRecord struct {
	calls       []string
	failPhase   map[string]bool
	failBlocked bool
}

func (f *fakeRecord) PhaseEnd(project string, ph Phase, out Outcome) error {
	f.calls = append(f.calls, fmt.Sprintf("end %s %s %s", project, ph.Name, out.Status))
	if f.failPhase[project+" "+ph.Name] {
		return errors.New("read-only file system")
	}
	return nil
}

func (f *fakeRecord) Blocked(project string, ph Phase, by []string) error {
	f.calls = append(f.calls, fmt.Sprintf("blocked %s %s by %s", project, ph.Name, strings.Join(by, ", ")))
	if f.failBlocked {
		return errors.New("read-only file system")
	}
	return nil
}

// harness wires a Runner to the fakes.
type harness struct {
	clock  *fakeClock
	exec   *fakeExec
	steps  *fakeSteps
	logs   *fakeLogs
	rec    *recorder
	record *fakeRecord
	r      *Runner
}

func newHarness(script map[string][]fakeCmd) *harness {
	clock := &fakeClock{t: time.Unix(0, 0)}
	steps := &fakeSteps{reaches: map[string]int{}, statuses: map[string]int{}}
	h := &harness{
		clock:  clock,
		exec:   &fakeExec{clock: clock, script: script, steps: steps},
		steps:  steps,
		logs:   newFakeLogs(),
		rec:    &recorder{},
		record: &fakeRecord{failPhase: map[string]bool{}},
	}
	h.r = &Runner{
		Exec: h.exec, Steps: h.steps, OpenLog: h.logs.Open, Report: h.rec, Record: h.record, Now: clock.Now,
		RunID: "20260101T000000Z-abcd", Root: "/w",
	}
	return h
}
