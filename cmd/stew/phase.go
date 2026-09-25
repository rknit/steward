package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/rknit/steward/internal/report"
	"github.com/rknit/steward/internal/runlog"
	"github.com/rknit/steward/internal/runner"
	"github.com/rknit/steward/internal/workspace"
)

// killDelay is how long a stopped command gets between SIGTERM and SIGKILL.
const killDelay = 5 * time.Second

func newPhaseCmd(stdout io.Writer, argv []string, command, short string) *cobra.Command {
	var level string
	cmd := &cobra.Command{
		Use:   command + " [name...]",
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPhases(stdout, argv, command, args, level)
		},
	}
	if command == "ci" {
		cmd.Flags().StringVarP(&level, "level", "l", "full", "CI level: full, quick, or pre-commit")
	}
	return cmd
}

func runPhases(stdout io.Writer, argv []string, command string, names []string, levelFlag string) error {
	level := workspace.LevelFull
	if command == "ci" {
		l, err := workspace.ParseLevel(levelFlag)
		if err != nil {
			return invalid(err)
		}
		level = l
	}
	_, ws, err := loadWorkspace()
	if err != nil {
		return err
	}
	plan, err := buildPlan(ws, command, names, level)
	if err != nil {
		return invalid(err)
	}

	// The first stop signal interrupts the run; a second one force-kills the command stew is waiting for.
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	finished := make(chan struct{})
	defer close(finished)
	force := make(chan struct{})
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	go func() {
		select {
		case sig := <-sigs:
			cancel(runner.Interrupt{Signal: sig.(syscall.Signal)})
		case <-finished:
			return
		}
		select {
		case <-sigs:
			close(force)
		case <-finished:
		}
	}()

	if err := runner.AdoptOrphans(); err != nil {
		return rejected(fmt.Errorf("cannot adopt orphaned processes: %w", err))
	}
	var steps runner.Steps
	if slices.ContainsFunc(plan.Jobs, func(j runner.Job) bool { return len(j.Wrappers) > 0 }) {
		d, err := runner.NewStepDir()
		if err != nil {
			return rejected(fmt.Errorf("cannot create step directory: %w", err))
		}
		defer d.Remove()
		steps = d
	}

	logs, err := runlog.Create(filepath.Join(ws.Root, workspace.DirName), time.Now(), rand.Reader)
	if err != nil {
		return rejected(fmt.Errorf("cannot create run log directory: %w", err))
	}
	defer logs.Close()
	start := time.Now()
	projects := make([]string, len(plan.Jobs))
	projectWrapper := map[string]string{}
	for i, job := range plan.Jobs {
		projects[i] = job.Project
		p, ok := ws.Project(job.Project)
		if !ok {
			panic("stew: plan job for unregistered project " + job.Project)
		}
		if p.Wrapper != "" {
			projectWrapper[p.Name] = p.Wrapper
		}
	}
	if len(projectWrapper) == 0 {
		projectWrapper = nil
	}
	if err := logs.Start(argv, ws.Wrapper, projectWrapper, plan.Columns, projects); err != nil {
		return rejected(fmt.Errorf("log error: %w", err))
	}

	r := &runner.Runner{
		Exec:  runner.Shell{KillDelay: killDelay, Force: force},
		Steps: steps,
		OpenLog: func(project, phase string) (runner.PhaseLog, error) {
			l, err := logs.OpenPhase(project, phase)
			if err != nil {
				return nil, err
			}
			return l, nil
		},
		Report: newReporter(stdout),
		Record: logs,
		Now:    time.Now,
		RunID:  logs.ID,
		Root:   ws.Root,
	}
	res := r.Run(ctx, plan)
	total := time.Since(start)
	report.Summary(stdout, res, total, path.Join(workspace.DirName, "runs", logs.ID))
	code := res.ExitCode()
	if err := logs.Finish(total); err != nil {
		return &exitError{code: max(code, 1), err: fmt.Errorf("log error: %w", err)}
	}
	if code != 0 {
		return &exitError{code: code}
	}
	return nil
}

// newReporter animates when stdout is a terminal and prints plain lines otherwise.
func newReporter(stdout io.Writer) runner.Reporter {
	if isTerminal(stdout) {
		return report.NewTTY(stdout)
	}
	return &report.Plain{W: stdout}
}

// isTerminal reports whether w is a character device, such as a terminal.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
