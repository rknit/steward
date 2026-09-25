// Package runner executes a plan of project phases: the phase algorithm, blocking, and interrupt handling.
// It knows nothing about TOML or terminals.
package runner

import (
	"context"
	"fmt"
	"io"
	"syscall"
	"time"
)

// Status is a phase's result word.
type Status string

// Phase statuses. The zero Status means the phase did not run.
const (
	Done        Status = "done"
	Pass        Status = "pass"
	Skip        Status = "skip"
	Fail        Status = "fail"
	Blocked     Status = "blocked"
	Interrupted Status = "interrupted"
)

// Interrupt is the context cause for a signal that stops the whole run (Ctrl-C, SIGTERM, SIGHUP).
type Interrupt struct{ Signal syscall.Signal }

// Error implements error.
func (i Interrupt) Error() string { return "interrupted by " + signalName(i.Signal) }

// ErrInterrupted is the cause for Ctrl-C.
var ErrInterrupted = Interrupt{Signal: syscall.SIGINT}

// Phase is one phase of one project, with its commands already resolved.
type Phase struct {
	Name   string // column and display name: "setup", "build", "ci.pre-commit"
	Used   string // what actually runs, also the log file name: "setup", "build", "ci.quick"
	Run    string
	Verify string // always "" for CI phases
	CI     bool
}

// Fallback returns the CI level that ran instead of the requested one ("quick"), or "" if none.
func (p Phase) Fallback() string {
	if p.Used == p.Name {
		return ""
	}
	return p.Used[len("ci."):]
}

// Job is one project's work in a plan.
type Job struct {
	Project string
	Dir     string   // absolute directory the commands run in
	Deps    []string // direct dependency names
	Phases  []Phase
}

// Plan is the ordered work for one stew run.
type Plan struct {
	Columns []string // phase names in summary column order
	Jobs    []Job    // dependency order
}

// StepOutput is one step's captured stdout and stderr, in write order.
type StepOutput struct {
	Step   string // "verify", "run", or "verify after run"
	Cmd    string
	Output []byte
}

// Outcome is how a phase ended.
type Outcome struct {
	Status   Status
	Duration time.Duration
	Steps    []StepOutput // replayed steps; set only for Fail and Interrupted
	Cause    string       // "exit 2", "signal SIGINT", "cannot start: ...", "log error: ..."; Fail and Interrupted only
}

// LogErrorOutcome turns a Recorder save error into the outcome it makes the phase report.
// It returns out unchanged if its Status is already Fail or Interrupted; otherwise it returns
// a Fail outcome with the same Duration and Cause "log error: " + err.Error().
func LogErrorOutcome(out Outcome, err error) Outcome {
	if out.Status == Fail || out.Status == Interrupted {
		return out
	}
	return Outcome{Status: Fail, Duration: out.Duration, Cause: "log error: " + err.Error()}
}

// Reporter receives progress events.
type Reporter interface {
	PhaseStart(project string, ph Phase)
	PhaseEnd(project string, ph Phase, out Outcome)
	Blocked(project string, ph Phase, by []string)
}

// Recorder persists phase results. It is called before the matching Reporter event.
// A PhaseEnd error turns a phase that has not already failed or been interrupted into a log error failure.
type Recorder interface {
	PhaseEnd(project string, ph Phase, out Outcome) error
	Blocked(project string, ph Phase, by []string) error
}

// Result is how one command ended.
type Result struct {
	ExitCode int
	Signal   string // e.g. "SIGKILL"; set when the command was killed by a signal
	Err      error  // set when the command could not start
}

// OK reports whether the command exited 0.
func (r Result) OK() bool {
	return r.Err == nil && r.Signal == "" && r.ExitCode == 0
}

// Cause describes the result for the content area's last line.
func (r Result) Cause() string {
	switch {
	case r.Err != nil:
		return "cannot start: " + r.Err.Error()
	case r.Signal != "":
		return "signal " + r.Signal
	default:
		return fmt.Sprintf("exit %d", r.ExitCode)
	}
}

// Executor runs one shell command in dir, with env ("KEY=value") added to stew's own environment and replacing
// any inherited value of the same key. When ctx is done it stops the command: cause Interrupt{SIGINT}
// sends SIGINT to the group and waits; cause Interrupt{SIGTERM} or Interrupt{SIGHUP} sends that signal to
// the group, then SIGKILL after KillDelay; any other cause sends SIGTERM, then SIGKILL after KillDelay.
type Executor interface {
	Run(ctx context.Context, dir string, env []string, cmd string, stdout, stderr io.Writer) Result
}

// PhaseLog is the on-disk log of one phase.
type PhaseLog interface {
	Stdout() io.Writer
	Stderr() io.Writer
	Marker(step, cmd string) error
	Close() error
}

// Cell is one summary table cell. The zero Cell prints as "-".
type Cell struct {
	Status   Status
	Fallback string // CI level used instead of the requested one, e.g. "quick"
}

// Row is one project's summary cells, aligned with Results.Columns.
type Row struct {
	Project string
	Cells   []Cell
}

// Results is the outcome of a whole run.
type Results struct {
	Columns     []string
	Rows        []Row
	Failed      bool           // some phase failed or was blocked
	Interrupted syscall.Signal // 0 unless a signal stopped the run
}

// ExitCode maps results to stew's exit code: 128+signal if interrupted (SIGINT 130, SIGTERM 143, SIGHUP 129),
// 1 failed or blocked, 0 success.
func (r *Results) ExitCode() int {
	switch {
	case r.Interrupted != 0:
		return 128 + int(r.Interrupted)
	case r.Failed:
		return 1
	}
	return 0
}
