package runner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// Runner executes plans. Steps is required only when a section has wrappers; every other field is required.
type Runner struct {
	Exec    Executor
	Steps   Steps
	OpenLog func(project, section string) (SectionLog, error)
	Report  Reporter
	Record  Recorder
	Now     func() time.Time
	RunID   string // exported to commands as STEW_RUN_ID
	Root    string // workspace root, exported to commands as STEW_ROOT
	Jobs    int    // most sections running at once; at least 1
}

// env is what every command of section s gets on top of stew's own environment.
func (r *Runner) env(s Section) []string {
	return []string{
		"STEW_RUN_ID=" + r.RunID,
		"STEW_ROOT=" + r.Root,
		"STEW_PROJECT=" + s.Project,
		"STEW_SECTION=" + s.Name,
		"STEW_TAG=" + s.Key(),
	}
}

// Run executes the plan. Whenever a section ends, and once at the start, it scans the sections not yet started, in
// plan order, and starts each ready one while fewer than Jobs run; with Jobs 1 the plan runs in order. It never
// returns early on failure: sections that do not require a failed one keep running. After an interrupt (ctx
// cancelled with an Interrupt cause: Ctrl-C, SIGTERM, or SIGHUP) no new section starts, and Run returns once the
// running ones end. Only Run's own goroutine calls Report and Record.
func (r *Runner) Run(ctx context.Context, plan Plan) *Results {
	if r.Jobs < 1 {
		panic("runner: Jobs must be at least 1")
	}
	res := &Results{Columns: plan.Columns}
	column := make(map[string]int, len(plan.Columns))
	for i, c := range plan.Columns {
		column[c] = i
	}
	row := make(map[string]int, len(plan.Projects))
	for i, p := range plan.Projects {
		row[p] = i
		res.Rows = append(res.Rows, Row{Project: p, Cells: make([]Status, len(plan.Columns))})
	}
	cell := func(s Section) *Status { return &res.Rows[row[s.Project]].Cells[column[s.Name]] }

	type ending struct {
		i   int
		out Outcome
	}
	endings := make(chan ending)
	status := make(map[string]Status, len(plan.Sections)) // keys of sections that ended or were blocked
	started := make([]bool, len(plan.Sections))           // started or blocked
	running := 0

	scan := func() {
		for i, s := range plan.Sections {
			if started[i] {
				continue
			}
			if sig, ok := interruptSignal(ctx); ok {
				res.Interrupted = sig
				return
			}
			if running >= r.Jobs {
				return
			}
			if slices.ContainsFunc(s.Requires, func(req string) bool { _, ended := status[req]; return !ended }) {
				continue
			}
			started[i] = true
			if r.block(s, status) {
				*cell(s) = Blocked
				status[s.Key()] = Blocked
				res.Failed = true
				continue
			}
			running++
			r.Report.SectionStart(s)
			go func() {
				begin := r.Now()
				out := r.section(ctx, i, s)
				out.Duration = r.Now().Sub(begin)
				endings <- ending{i, out}
			}()
		}
	}

	scan()
	for running > 0 {
		e := <-endings
		running--
		s, out := plan.Sections[e.i], e.out
		if err := r.Record.SectionEnd(s, out); err != nil {
			out = LogErrorOutcome(out, err)
		}
		r.Report.SectionEnd(s, out)
		*cell(s) = out.Status
		status[s.Key()] = out.Status
		switch out.Status {
		case Interrupted:
			if sig, ok := interruptSignal(ctx); ok {
				res.Interrupted = sig
			}
		case Fail:
			res.Failed = true
		}
		scan()
	}
	return res
}

// block records s as blocked, and reports it when a direct requirement failed, if any requirement failed or was
// blocked. It reports whether s is blocked.
func (r *Runner) block(s Section, status map[string]Status) bool {
	var by, failed []string
	for _, req := range s.Requires {
		switch status[req] {
		case Fail:
			by = append(by, req)
			failed = append(failed, req)
		case Blocked:
			by = append(by, req)
		}
	}
	if len(by) == 0 {
		return false
	}
	_ = r.Record.Blocked(s, by)
	if len(failed) > 0 {
		r.Report.Blocked(s, failed)
	}
	return true
}

// interruptSignal reports the signal that stopped the run, if the context's cancellation cause is an Interrupt.
func interruptSignal(ctx context.Context) (syscall.Signal, bool) {
	var i Interrupt
	if errors.As(context.Cause(ctx), &i) {
		return i.Signal, true
	}
	return 0, false
}

func interrupted(ctx context.Context) bool {
	_, ok := interruptSignal(ctx)
	return ok
}

// section runs the section algorithm for s, the i-th section of the plan. Duration is filled in by the caller.
func (r *Runner) section(ctx context.Context, i int, s Section) (out Outcome) {
	if s.Run == "" && s.Verify == "" {
		return Outcome{Status: Skip}
	}

	log, err := r.OpenLog(s.Project, s.Name)
	if err != nil {
		return Outcome{Status: Fail, Cause: "log error: " + err.Error()}
	}
	defer func() {
		if err := log.Close(); err != nil && out.Status != Fail && out.Status != Interrupted {
			out = Outcome{Status: Fail, Steps: out.Steps, Cause: "log error: " + err.Error()}
		}
	}()

	env := r.env(s)
	key := strconv.Itoa(i) + "-" + s.Project + "-" + s.Name
	var steps []StepOutput
	// run executes one step. done reports that the section ended early, with outcome end.
	run := func(step, cmd string) (res Result, done bool, end Outcome) {
		res, output, logErr := r.step(ctx, s.Dir, key, s.Wrappers, env, log, step, cmd)
		steps = append(steps, StepOutput{Step: step, Cmd: cmd, Output: output})
		switch {
		case interrupted(ctx):
			return res, true, Outcome{Status: Interrupted, Steps: steps, Cause: res.Cause()}
		case logErr != nil:
			return res, true, Outcome{Status: Fail, Steps: steps, Cause: "log error: " + logErr.Error()}
		}
		return res, false, Outcome{}
	}

	if s.SkipIf != "" {
		res, done, end := run("skip_if", s.SkipIf)
		switch {
		case done:
			return end
		case res.OK():
			return Outcome{Status: Skip}
		case res.WrapperFailed():
			return Outcome{Status: Fail, Steps: steps, Cause: res.Cause()}
		}
		steps = steps[:0] // a failed skip_if only means "not done yet"; don't replay it
	}

	for _, st := range []struct{ name, cmd string }{{"run", s.Run}, {"verify", s.Verify}} {
		if st.cmd == "" {
			continue
		}
		res, done, end := run(st.name, st.cmd)
		if done {
			return end
		}
		if !res.OK() {
			return Outcome{Status: Fail, Steps: steps, Cause: res.Cause()}
		}
	}
	return Outcome{Status: Done}
}

// step runs one command, writing its output to the log files and to a replay buffer.
// A log write error cancels the command and is returned as logErr.
func (r *Runner) step(ctx context.Context, dir, key string, wrappers, env []string, log SectionLog, step, cmd string) (res Result, output []byte, logErr error) {
	if err := log.Marker(step, cmd); err != nil {
		return Result{}, nil, err
	}
	argv := []string{"sh", "-c", cmd}
	if len(wrappers) > 0 {
		var err error
		if argv, err = r.Steps.Prepare(key, wrappers, env, cmd); err != nil {
			return Result{}, nil, err
		}
	}

	stepCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var (
		mu     sync.Mutex
		failed error
	)
	fail := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		if failed == nil {
			failed = err
			cancel(err)
		}
	}

	replay := &lockedBuffer{}
	stdout := &teeWriter{log: log.Stdout(), replay: replay, fail: fail}
	stderr := &teeWriter{log: log.Stderr(), replay: replay, fail: fail}
	res = r.Exec.Run(stepCtx, dir, env, argv, stdout, stderr)
	if len(wrappers) > 0 {
		n, status, finished, err := r.Steps.Collect(key)
		if err != nil {
			fail(err)
		}
		if n == 1 && finished {
			res = Result{ExitCode: status}
		}
		res.Wrapped, res.Reaches, res.Unfinished = true, n, n == 1 && !finished
	}

	mu.Lock()
	defer mu.Unlock()
	return res, replay.Bytes(), failed
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf.Bytes())
}

// teeWriter writes to the replay buffer and the log file. A log write error is reported through fail.
type teeWriter struct {
	log    io.Writer
	replay io.Writer
	fail   func(error)
}

func (t *teeWriter) Write(p []byte) (int, error) {
	t.replay.Write(p)
	if _, err := t.log.Write(p); err != nil {
		t.fail(err)
		return 0, err
	}
	return len(p), nil
}
