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
}

// fakeExec runs scripted commands. Each command string maps to a queue of executions; the last one repeats.
type fakeExec struct {
	clock     *fakeClock
	script    map[string][]fakeCmd
	calls     []string
	cancelled []string
}

func (f *fakeExec) Run(ctx context.Context, dir, cmd string, stdout, stderr io.Writer) Result {
	if cmd == "" {
		panic("executor called with an empty command")
	}
	f.calls = append(f.calls, dir+": "+cmd)
	queue := f.script[cmd]
	if len(queue) == 0 {
		panic("unscripted command: " + cmd)
	}
	c := queue[0]
	if len(queue) > 1 {
		f.script[cmd] = queue[1:]
	}
	f.clock.t = f.clock.t.Add(c.took)
	io.WriteString(stdout, c.stdout)
	io.WriteString(stderr, c.stderr)
	if c.during != nil {
		c.during()
	}
	if ctx.Err() != nil {
		f.cancelled = append(f.cancelled, cmd)
		if errors.Is(context.Cause(ctx), ErrInterrupted) {
			return Result{Signal: "SIGINT"}
		}
		return Result{Signal: "SIGTERM"}
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

// harness wires a Runner to the fakes.
type harness struct {
	clock *fakeClock
	exec  *fakeExec
	logs  *fakeLogs
	rec   *recorder
	r     *Runner
}

func newHarness(script map[string][]fakeCmd) *harness {
	clock := &fakeClock{t: time.Unix(0, 0)}
	h := &harness{
		clock: clock,
		exec:  &fakeExec{clock: clock, script: script},
		logs:  newFakeLogs(),
		rec:   &recorder{},
	}
	h.r = &Runner{Exec: h.exec, OpenLog: h.logs.Open, Report: h.rec, Now: clock.Now}
	return h
}
