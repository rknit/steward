package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
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

// fakeLogs hands out fakeLogs by "<project>:<section>".
type fakeLogs struct {
	logs    map[string]*fakeLog
	openErr map[string]bool
	setup   map[string]func(*fakeLog)
}

func newFakeLogs() *fakeLogs {
	return &fakeLogs{logs: map[string]*fakeLog{}, openErr: map[string]bool{}, setup: map[string]func(*fakeLog){}}
}

func (f *fakeLogs) Open(project, section string) (SectionLog, error) {
	key := project + ":" + section
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

func (r *recorder) SectionStart(s Section) {
	r.events = append(r.events, "start "+s.Key())
}

func (r *recorder) SectionEnd(s Section, out Outcome) {
	if r.outcomes == nil {
		r.outcomes = map[string]Outcome{}
	}
	r.outcomes[s.Key()] = out
	r.events = append(r.events, fmt.Sprintf("end %s %s", s.Key(), out.Status))
}

func (r *recorder) Blocked(s Section, failed []string) {
	r.events = append(r.events, fmt.Sprintf("blocked %s by %s", s.Key(), strings.Join(failed, ", ")))
}

// fakeRecord records Recorder calls. failSection makes SectionEnd fail for a section's key.
type fakeRecord struct {
	calls       []string
	failSection map[string]bool
	failBlocked bool
}

func (f *fakeRecord) SectionEnd(s Section, out Outcome) error {
	f.calls = append(f.calls, fmt.Sprintf("end %s %s", s.Key(), out.Status))
	if f.failSection[s.Key()] {
		return errors.New("read-only file system")
	}
	return nil
}

func (f *fakeRecord) Blocked(s Section, by []string) error {
	f.calls = append(f.calls, fmt.Sprintf("blocked %s by %s", s.Key(), strings.Join(by, ", ")))
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
		record: &fakeRecord{failSection: map[string]bool{}},
	}
	h.r = &Runner{
		Exec: h.exec, Steps: h.steps, OpenLog: h.logs.Open, Report: h.rec, Record: h.record, Now: clock.Now,
		RunID: "20260101T000000Z-abcd", Root: "/w", Jobs: 1,
	}
	return h
}

// gateExec holds each command until the test finishes it, or until ctx is done. Commands are told apart by their
// command string, so each must be unique in a plan.
type gateExec struct {
	mu    sync.Mutex
	gates map[string]chan Result
}

func (g *gateExec) gate(cmd string) chan Result {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.gates == nil {
		g.gates = map[string]chan Result{}
	}
	if g.gates[cmd] == nil {
		g.gates[cmd] = make(chan Result, 1)
	}
	return g.gates[cmd]
}

func (g *gateExec) Run(ctx context.Context, dir string, env []string, argv []string, stdout, stderr io.Writer) Result {
	select {
	case res := <-g.gate(argv[len(argv)-1]):
		return res
	case <-ctx.Done():
		var i Interrupt
		if errors.As(context.Cause(ctx), &i) {
			return Result{Signal: signalName(i.Signal)}
		}
		return Result{Signal: "SIGTERM"}
	}
}

// finish lets the command end with res.
func (g *gateExec) finish(cmd string, res Result) { g.gate(cmd) <- res }

// eventReporter sends reporter events, in order, as readable lines.
type eventReporter struct{ ch chan<- string }

func (e *eventReporter) SectionStart(s Section) { e.ch <- "start " + s.Key() }
func (e *eventReporter) SectionEnd(s Section, out Outcome) {
	e.ch <- "end " + s.Key() + " " + string(out.Status)
}
func (e *eventReporter) Blocked(s Section, failed []string) {
	e.ch <- "blocked " + s.Key() + " by " + strings.Join(failed, ", ")
}

// gateHarness runs a plan on gated commands in the background and streams its reporter events.
type gateHarness struct {
	exec   *gateExec
	events chan string
	done   chan *Results
}

func startGated(ctx context.Context, jobs int, p Plan) *gateHarness {
	g := &gateHarness{exec: &gateExec{}, events: make(chan string, 64), done: make(chan *Results, 1)}
	r := &Runner{
		Exec:    g.exec,
		OpenLog: func(string, string) (SectionLog, error) { return &fakeLog{}, nil },
		Report:  &eventReporter{ch: g.events},
		Record:  &fakeRecord{failSection: map[string]bool{}},
		Now:     time.Now,
		RunID:   "20260101T000000Z-abcd",
		Root:    "/w",
		Jobs:    jobs,
	}
	go func() { g.done <- r.Run(ctx, p) }()
	return g
}

// expect fails unless the next events are want, in order.
func (g *gateHarness) expect(t *testing.T, want ...string) {
	t.Helper()
	for _, w := range want {
		select {
		case got := <-g.events:
			if got != w {
				t.Fatalf("event = %q, want %q", got, w)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("no event within 10s, want %q", w)
		}
	}
}

// expectAnyOrder fails unless the next len(want) events are want, in any order.
func (g *gateHarness) expectAnyOrder(t *testing.T, want ...string) {
	t.Helper()
	var got []string
	for range want {
		select {
		case e := <-g.events:
			got = append(got, e)
		case <-time.After(10 * time.Second):
			t.Fatalf("events within 10s = %q, want %q", got, want)
		}
	}
	slices.Sort(got)
	want = slices.Sorted(slices.Values(want))
	if !slices.Equal(got, want) {
		t.Fatalf("events = %q, want %q in any order", got, want)
	}
}

// wait returns the results once Run returns, and fails if an event was not read. Run sends every event before it
// returns, so none can arrive later.
func (g *gateHarness) wait(t *testing.T) *Results {
	t.Helper()
	select {
	case res := <-g.done:
		if len(g.events) > 0 {
			t.Fatalf("unread event %q", <-g.events)
		}
		return res
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return within 10s")
	}
	return nil
}
