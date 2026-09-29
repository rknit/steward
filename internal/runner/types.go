// Package runner executes a plan of sections: the section algorithm, blocking, and interrupt handling.
// It knows nothing about TOML or terminals.
package runner

import (
	"context"
	"fmt"
	"io"
	"syscall"
	"time"
)

// Status is a section's result word.
type Status string

// Section statuses. The zero Status means the section did not run.
const (
	Done        Status = "done"
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

// Section is one section of one project, with its commands resolved.
type Section struct {
	Project  string
	Name     string   // section name, also the summary column: "build", "ci.full"
	Dir      string   // absolute directory the commands run in
	Wrappers []string // non-empty wrappers, outermost first; each contains StepPlaceholder once
	Run      string
	SkipIf   string
	Verify   string
	Requires []string // keys of the sections it requires, in execution order
}

// Key returns "<project>:<section>".
func (s Section) Key() string { return s.Project + ":" + s.Name }

// Plan is the ordered work for one stew run.
type Plan struct {
	Columns  []string  // section names, in order of first appearance
	Projects []string  // summary rows, in order of each project's first section
	Sections []Section // execution order; every requirement comes before its dependents
}

// StepOutput is one step's captured stdout and stderr, in write order.
type StepOutput struct {
	Step   string // "skip_if", "run", or "verify"
	Cmd    string
	Output []byte
}

// Outcome is how a section ended.
type Outcome struct {
	Status   Status
	Duration time.Duration
	Steps    []StepOutput // replayed steps; set only for Fail and Interrupted
	Cause    string       // "exit 2", "signal SIGINT", "cannot start: ...", "log error: ..."; Fail and Interrupted only
}

// LogErrorOutcome turns a Recorder save error into the outcome it makes the section report.
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
	SectionStart(s Section)
	SectionEnd(s Section, out Outcome)
	// Blocked reports a section blocked by requirements that failed. It is not called for a section
	// whose failed-or-blocked requirements were all blocked, which keeps the output pruned.
	Blocked(s Section, failed []string)
}

// Recorder persists section results. It is called before the matching Reporter event.
// A SectionEnd error turns a section that has not already failed or been interrupted into a log error failure.
type Recorder interface {
	SectionEnd(s Section, out Outcome) error
	Blocked(s Section, by []string) error
}

// Result is how one command ended.
type Result struct {
	ExitCode int
	Signal   string // e.g. "SIGKILL"; set when the command was killed by a signal
	Err      error  // set when the command could not start
	Wrapped  bool   // the command ran through wrappers
	Reaches  int    // with Wrapped: how many times the wrappers ran the command
	// Unfinished is set with Wrapped and Reaches 1 when the wrappers exited before the command finished.
	// Then the other fields are the outermost wrapper's result; after a finished single reach, they are the command's.
	Unfinished bool
}

// WrapperFailed reports whether wrappers ran the command other than exactly once, or exited before it finished.
func (r Result) WrapperFailed() bool { return r.Wrapped && (r.Reaches != 1 || r.Unfinished) }

// OK reports whether the command ran once and exited 0.
func (r Result) OK() bool {
	return !r.WrapperFailed() && r.Err == nil && r.Signal == "" && r.ExitCode == 0
}

// Cause describes the result for the content area's last line.
func (r Result) Cause() string {
	var cause string
	switch {
	case r.Err != nil:
		cause = "cannot start: " + r.Err.Error()
	case r.Signal != "":
		cause = "signal " + r.Signal
	default:
		cause = fmt.Sprintf("exit %d", r.ExitCode)
	}
	switch {
	case r.Wrapped && r.Reaches == 0:
		return "wrapper did not run the command (" + cause + ")"
	case r.Wrapped && r.Reaches > 1:
		return fmt.Sprintf("wrapper ran the command %d times (%s)", r.Reaches, cause)
	case r.Wrapped && r.Unfinished:
		return "wrapper exited before the command finished (" + cause + ")"
	}
	return cause
}

// Executor runs argv in dir, with env ("KEY=value") added to stew's own environment and replacing
// any inherited value of the same key. When ctx is done it stops the command: cause Interrupt{SIGINT}
// sends SIGINT to the group and waits; cause Interrupt{SIGTERM} or Interrupt{SIGHUP} sends that signal to
// the group, then SIGKILL after KillDelay; any other cause sends SIGTERM, then SIGKILL after KillDelay.
type Executor interface {
	Run(ctx context.Context, dir string, env []string, argv []string, stdout, stderr io.Writer) Result
}

// SectionLog is the on-disk log of one section.
type SectionLog interface {
	Stdout() io.Writer
	Stderr() io.Writer
	Marker(step, cmd string) error
	Close() error
}

// Steps prepares what a wrapped step runs, then reports how many times its command ran and how it exited.
type Steps interface {
	// Prepare writes the step's scripts for key ("<plan index>-<project>-<section>") and returns the argv that runs cmd
	// inside wrappers, outermost first.
	Prepare(key string, wrappers, env []string, cmd string) ([]string, error)
	// Collect returns how many times the step's command ran and, when its last run finished, its exit status.
	// Then it removes the step's files.
	Collect(key string) (reaches, status int, finished bool, err error)
}

// Row is one project's summary cells, aligned with Results.Columns. The zero Status prints as "-".
type Row struct {
	Project string
	Cells   []Status
}

// Results is the outcome of a whole run.
type Results struct {
	Columns     []string
	Rows        []Row
	Failed      bool           // some section failed or was blocked
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
