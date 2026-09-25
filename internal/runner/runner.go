package runner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"sync"
	"time"
)

// Runner executes plans. All fields are required.
type Runner struct {
	Exec    Executor
	OpenLog func(project, phase string) (PhaseLog, error)
	Report  Reporter
	Now     func() time.Time
}

// Run executes the plan in order. It never returns early on failure: independent projects keep running.
// After an interrupt (ctx cancelled with ErrInterrupted) no new phase starts.
func (r *Runner) Run(ctx context.Context, plan Plan) *Results {
	res := &Results{Columns: plan.Columns}
	column := make(map[string]int, len(plan.Columns))
	for i, c := range plan.Columns {
		column[c] = i
	}
	position := make(map[string]int, len(plan.Jobs))
	for i, job := range plan.Jobs {
		position[job.Project] = i
	}
	stopped := make(map[string]bool) // projects that failed or were blocked

	for _, job := range plan.Jobs {
		res.Rows = append(res.Rows, Row{Project: job.Project, Cells: make([]Cell, len(plan.Columns))})
		cells := res.Rows[len(res.Rows)-1].Cells
		if interrupted(ctx) {
			res.Interrupted = true
		}
		if res.Interrupted {
			continue
		}

		var by []string
		for _, dep := range job.Deps {
			if stopped[dep] {
				by = append(by, dep)
			}
		}
		if len(by) > 0 {
			slices.SortFunc(by, func(a, b string) int { return position[a] - position[b] })
			first := job.Phases[0]
			r.Report.Blocked(job.Project, first, by)
			cells[column[first.Name]] = Cell{Status: Blocked}
			stopped[job.Project] = true
			res.Failed = true
			continue
		}

		for _, ph := range job.Phases {
			if interrupted(ctx) {
				res.Interrupted = true
				break
			}
			r.Report.PhaseStart(job.Project, ph)
			start := r.Now()
			out := r.phase(ctx, job, ph)
			out.Duration = r.Now().Sub(start)
			r.Report.PhaseEnd(job.Project, ph, out)
			cells[column[ph.Name]] = Cell{Status: out.Status, Fallback: ph.Fallback()}

			if out.Status == Interrupted {
				res.Interrupted = true
				break
			}
			if out.Status == Fail {
				stopped[job.Project] = true
				res.Failed = true
				break
			}
		}
	}
	return res
}

func interrupted(ctx context.Context) bool {
	return errors.Is(context.Cause(ctx), ErrInterrupted)
}

// phase runs the phase algorithm. Duration is filled in by the caller.
func (r *Runner) phase(ctx context.Context, job Job, ph Phase) (out Outcome) {
	success := Done
	if ph.CI {
		success = Pass
	}
	if ph.Run == "" && ph.Verify == "" {
		return Outcome{Status: Skip}
	}

	log, err := r.OpenLog(job.Project, ph.Used)
	if err != nil {
		return Outcome{Status: Fail, Cause: "log error: " + err.Error()}
	}
	defer func() {
		if err := log.Close(); err != nil && out.Status != Fail && out.Status != Interrupted {
			out = Outcome{Status: Fail, Steps: out.Steps, Cause: "log error: " + err.Error()}
		}
	}()

	var steps []StepOutput
	// run executes one step. done reports that the phase ended early, with outcome end.
	run := func(step, cmd string) (res Result, done bool, end Outcome) {
		res, output, logErr := r.step(ctx, job.Dir, log, step, cmd)
		steps = append(steps, StepOutput{Step: step, Cmd: cmd, Output: output})
		switch {
		case interrupted(ctx):
			return res, true, Outcome{Status: Interrupted, Steps: steps, Cause: res.Cause()}
		case logErr != nil:
			return res, true, Outcome{Status: Fail, Steps: steps, Cause: "log error: " + logErr.Error()}
		}
		return res, false, Outcome{}
	}

	if ph.Verify != "" {
		res, done, end := run("verify", ph.Verify)
		switch {
		case done:
			return end
		case res.OK():
			return Outcome{Status: Skip}
		case ph.Run == "":
			return Outcome{Status: Fail, Steps: steps, Cause: res.Cause()}
		}
		steps = steps[:0] // a failed pre-run verify only means "not done yet"; don't replay it
	}

	res, done, end := run("run", ph.Run)
	if done {
		return end
	}
	if !res.OK() {
		return Outcome{Status: Fail, Steps: steps, Cause: res.Cause()}
	}

	if ph.Verify != "" {
		res, done, end := run("verify after run", ph.Verify)
		if done {
			return end
		}
		if !res.OK() {
			return Outcome{Status: Fail, Steps: steps, Cause: res.Cause()}
		}
	}
	return Outcome{Status: success}
}

// step runs one command, writing its output to the log files and to a replay buffer.
// A log write error cancels the command and is returned as logErr.
func (r *Runner) step(ctx context.Context, dir string, log PhaseLog, step, cmd string) (res Result, output []byte, logErr error) {
	if err := log.Marker(step, cmd); err != nil {
		return Result{}, nil, err
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
	res = r.Exec.Run(stepCtx, dir, cmd, stdout, stderr)

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
