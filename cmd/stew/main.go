// Command stew is a stack-agnostic monorepo orchestrator.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rknit/steward/internal/workspace"
)

func main() {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "stew: %v\n", err)
		os.Exit(1)
	}
	proc := process{
		dir: dir, env: os.Environ(), loc: time.Local,
		stdin: os.Stdin, interactive: isTerminal(os.Stdin) && isTerminal(os.Stdout),
		stdout: os.Stdout, stderr: os.Stderr,
	}
	os.Exit(run(proc, os.Args[1:]))
}

// process is the process state a command reads: its working directory, environment, time zone, input, and output.
// main passes stew's own, and tests run commands in-process with a test script's, which never ask for input.
// stew exec and stop signals still use the real process.
type process struct {
	dir            string
	env            []string
	loc            *time.Location
	stdin          io.Reader
	interactive    bool // stdin and stdout are terminals, so stew may ask a question
	stdout, stderr io.Writer
}

// getenv returns the value of key in proc's environment.
func (proc process) getenv(key string) (string, bool) {
	value, ok := "", false
	for _, kv := range proc.env {
		if k, v, _ := strings.Cut(kv, "="); k == key {
			value, ok = v, true
		}
	}
	return value, ok
}

// tempDir is $TMPDIR, or /tmp when it is empty.
func (proc process) tempDir() string {
	if dir, _ := proc.getenv("TMPDIR"); dir != "" {
		return dir
	}
	return "/tmp"
}

// exitError carries an exit code. A nil err means the command already reported its failure.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit %d", e.code)
	}
	return e.err.Error()
}

// rejected is a refused or failed operation (exit 1).
func rejected(err error) error { return &exitError{code: 1, err: err} }

// invalid is a usage or workspace configuration error (exit 2).
func invalid(err error) error { return &exitError{code: 2, err: err} }

// run executes stew with args and returns the exit code. Errors not wrapped in exitError come from
// cobra (unknown command, bad flag, wrong argument count) and are usage errors.
func run(proc process, args []string) int {
	root := newRootCmd(proc, args)
	root.SetArgs(args)
	root.SetOut(proc.stdout)
	root.SetErr(proc.stderr)
	err := root.Execute()
	if err == nil {
		return 0
	}
	var ee *exitError
	if !errors.As(err, &ee) {
		ee = &exitError{code: 2, err: err}
	}
	if ee.err != nil {
		fmt.Fprintf(proc.stderr, "stew: %v\n", ee.err)
	}
	return ee.code
}

func newRootCmd(proc process, argv []string) *cobra.Command {
	root := &cobra.Command{
		Use:           "stew",
		Short:         "Stack-agnostic monorepo orchestrator",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.AddCommand(
		newInitCmd(proc),
		newAddCmd(proc),
		newRemoveCmd(proc),
		newListCmd(proc),
		newRunCmd(proc, argv),
		newAliasCmd(proc, argv, "setup", "setup", "Run the setup section of projects, after the sections it requires"),
		newAliasCmd(proc, argv, "build", "build", "Run the build section of projects, after the sections it requires"),
		newAliasCmd(proc, argv, "ci", "ci", "Run the ci.<level> section of projects, after the sections it requires"),
		newAliasCmd(proc, argv, "setup-worktree", "worktree.setup",
			"Run the worktree.setup section of projects, after the sections it requires"),
		newGitCmd(proc),
		newRunsCmd(proc),
		newExecCmd(proc),
		newTrustCmd(proc),
		newSkillsCmd(proc),
	)
	return root
}

// loadWorkspace finds the workspace root above dir and loads it.
func loadWorkspace(dir string) (*workspace.Workspace, error) {
	root, err := workspace.FindRoot(dir)
	if err != nil {
		return nil, invalid(err)
	}
	ws, err := workspace.Load(root)
	if err != nil {
		return nil, invalid(err)
	}
	return ws, nil
}
