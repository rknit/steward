package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/rknit/steward/internal/job"
	"github.com/rknit/steward/internal/runner"
)

const execKey = "exec"

func newExecCmd(proc process) *cobra.Command {
	return &cobra.Command{
		Use:   "exec [project] <command>",
		Short: "Run a shell command in the current directory, inside the workspace wrapper and the named project's wrapper",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := loadWorkspace(proc)
			if err != nil {
				return err
			}
			env := []string{"STEW_ROOT=" + ws.Root}
			wraps := []string{ws.Wrapper}
			wrapDir := ws.Root
			if len(args) == 2 {
				p, ok := ws.Project(args[0])
				if !ok {
					return invalid(fmt.Errorf("unknown project %q", args[0]))
				}
				env = append(env, "STEW_PROJECT="+p.Name)
				wraps = append(wraps, p.Wrapper)
				wrapDir = filepath.Join(ws.Root, filepath.FromSlash(p.Path))
			}
			err = withStops(func(ctx context.Context, force <-chan struct{}) error {
				return ensureTrust(ctx, force, proc, ws)
			})
			if err != nil {
				return err
			}
			return execJob(proc, wrapDir, wrappers(wraps...), env, args[len(args)-1])
		},
	}
}

// execJob runs command as sh -c in proc.dir, as a job of its own (see job.Run). Wrappers, outermost first, run in
// wrapDir as they do for a section of that project, and the step script returns to proc.dir before it runs command.
// stew exits with the command's status, 128+n after signal n.
func execJob(proc process, wrapDir string, wraps, env []string, command string) error {
	if len(wraps) == 0 {
		status, err := job.Run(proc.dir, []string{"sh", "-c", command}, jobEnv(proc, env))
		if err != nil {
			return rejected(fmt.Errorf("cannot run: %w", err))
		}
		return statusError(exitStatus(status))
	}

	d, err := runner.NewStepDir(proc.tempDir())
	if err != nil {
		return rejected(fmt.Errorf("cannot create step directory: %w", err))
	}
	defer d.Remove()
	inCwd := "cd -- " + runner.ShellQuote(proc.dir) + " || exit\n" + command
	argv, err := d.Prepare(execKey, wraps, env, inCwd)
	if err != nil {
		return rejected(fmt.Errorf("cannot prepare step: %w", err))
	}
	status, err := job.Run(wrapDir, argv, jobEnv(proc, env))
	if err != nil {
		return rejected(fmt.Errorf("cannot run: %w", err))
	}
	outer := exitStatus(status)
	reaches, stepStatus, finished, err := d.Collect(execKey)
	if err != nil {
		return &exitError{code: max(outer, 1), err: fmt.Errorf("cannot read step files: %w", err)}
	}
	if reaches == 1 && finished {
		return statusError(stepStatus)
	}
	// A signal that ends the job, such as Ctrl-C while a wrapper starts or while the command runs, is not a wrapper
	// failure. It can end the step script before it records the command's status.
	if reaches <= 1 && outer > 128 {
		return statusError(outer)
	}
	res := runner.WaitResult(status)
	res.Wrapped, res.Reaches, res.Unfinished = true, reaches, reaches == 1
	return &exitError{code: max(outer, 1), err: errors.New(res.Cause())}
}

// runVars are the variables a section run exports. stew exec replaces inherited ones, so a command run from inside
// a section does not see that section's values.
var runVars = []string{"STEW_RUN_ID", "STEW_ROOT", "STEW_PROJECT", "STEW_SECTION", "STEW_TAG"}

// jobEnv is proc's environment without inherited run variables, plus env.
func jobEnv(proc process, env []string) []string {
	inherited := slices.DeleteFunc(slices.Clone(proc.env), func(kv string) bool {
		key, _, _ := strings.Cut(kv, "=")
		return slices.Contains(runVars, key)
	})
	return append(inherited, env...)
}

// exitStatus is how a shell reports ws: the exit code, or 128+n after signal n.
func exitStatus(ws syscall.WaitStatus) int {
	if ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ws.ExitStatus()
}

func statusError(status int) error {
	if status == 0 {
		return nil
	}
	return &exitError{code: status}
}
