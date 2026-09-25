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
	"time"

	"github.com/spf13/cobra"

	"github.com/rknit/steward/internal/report"
	"github.com/rknit/steward/internal/runlog"
	"github.com/rknit/steward/internal/runner"
	"github.com/rknit/steward/internal/workspace"
)

// killDelay is how long a stopped command gets between SIGTERM and SIGKILL.
const killDelay = 5 * time.Second

func newPhaseCmd(stdout io.Writer, command, short string) *cobra.Command {
	var level string
	cmd := &cobra.Command{
		Use:   command + " [name...]",
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPhases(stdout, command, args, level)
		},
	}
	if command == "ci" {
		cmd.Flags().StringVarP(&level, "level", "l", "full", "CI level: full, quick, or pre-commit")
	}
	return cmd
}

func runPhases(stdout io.Writer, command string, names []string, levelFlag string) error {
	level := workspace.LevelFull
	if command == "ci" {
		l, err := workspace.ParseLevel(levelFlag)
		if err != nil {
			return invalid(err)
		}
		level = l
	}
	cwd, err := os.Getwd()
	if err != nil {
		return rejected(err)
	}
	root, err := workspace.FindRoot(cwd)
	if err != nil {
		return invalid(err)
	}
	ws, err := workspace.Load(root)
	if err != nil {
		return invalid(err)
	}
	plan, err := buildPlan(ws, command, names, level)
	if err != nil {
		return invalid(err)
	}

	logs, err := runlog.Create(filepath.Join(root, workspace.DirName), time.Now(), rand.Reader)
	if err != nil {
		return rejected(fmt.Errorf("cannot create run log directory: %w", err))
	}
	start := time.Now()

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt)
	defer signal.Stop(sigs)
	go func() {
		select {
		case <-sigs:
			cancel(runner.ErrInterrupted)
		case <-ctx.Done():
		}
	}()

	r := &runner.Runner{
		Exec: runner.Shell{KillDelay: killDelay},
		OpenLog: func(project, phase string) (runner.PhaseLog, error) {
			l, err := logs.OpenPhase(project, phase)
			if err != nil {
				return nil, err
			}
			return l, nil
		},
		Report: newReporter(stdout),
		Now:    time.Now,
	}
	res := r.Run(ctx, plan)
	report.Summary(stdout, res, time.Since(start), path.Join(workspace.DirName, "runs", logs.ID))
	if code := res.ExitCode(); code != 0 {
		return &exitError{code: code}
	}
	return nil
}

// newReporter animates when stdout is a terminal and prints plain lines otherwise.
func newReporter(stdout io.Writer) runner.Reporter {
	if f, ok := stdout.(*os.File); ok {
		if info, err := f.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
			return report.NewTTY(stdout)
		}
	}
	return &report.Plain{W: stdout}
}
