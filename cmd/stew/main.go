// Command stew is a stack-agnostic monorepo orchestrator.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
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
func run(args []string, stdout, stderr io.Writer) int {
	root := newRootCmd(stdout)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.Execute()
	if err == nil {
		return 0
	}
	var ee *exitError
	if !errors.As(err, &ee) {
		ee = &exitError{code: 2, err: err}
	}
	if ee.err != nil {
		fmt.Fprintf(stderr, "stew: %v\n", ee.err)
	}
	return ee.code
}

func newRootCmd(stdout io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "stew",
		Short:         "Stack-agnostic monorepo orchestrator",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.AddCommand(
		newInitCmd(stdout),
		newAddCmd(stdout),
		newPhaseCmd(stdout, "setup", "Set up projects and their dependencies"),
		newPhaseCmd(stdout, "build", "Set up and build projects and their dependencies"),
		newPhaseCmd(stdout, "ci", "Set up and build dependencies, then run CI for projects"),
		newGitCmd(stdout),
	)
	return root
}
