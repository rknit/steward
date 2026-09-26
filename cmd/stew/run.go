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
	"regexp"
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

func newRunCmd(stdout io.Writer, argv []string) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "run <regex>...",
		Short: "Run sections whose <project>:<section> key matches, after the sections they require",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, ws, err := loadWorkspace()
			if err != nil {
				return err
			}
			return runSections(stdout, argv, ws, args, dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would run, in order, without running it")
	return cmd
}

// newAliasCmd returns a command that runs one section in the named projects, or in every project that has it.
// For "ci" the section is ci.<level>.
func newAliasCmd(stdout io.Writer, argv []string, section, short string) *cobra.Command {
	var level string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   section + " [project...]",
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := section
			if section == "ci" {
				name = "ci." + level
				if !workspace.ValidSectionName(name) {
					return invalid(fmt.Errorf("invalid CI level %q", level))
				}
			}
			_, ws, err := loadWorkspace()
			if err != nil {
				return err
			}
			patterns, err := aliasPatterns(ws, name, args)
			if err != nil {
				return invalid(err)
			}
			return runSections(stdout, argv, ws, patterns, dryRun)
		},
	}
	if section == "ci" {
		cmd.Flags().StringVarP(&level, "level", "l", "full", "run section ci.<level>")
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would run, in order, without running it")
	return cmd
}

// aliasPatterns returns patterns that match section in the named projects, or in every project when none are named.
func aliasPatterns(ws *workspace.Workspace, section string, projects []string) ([]string, error) {
	quoted := regexp.QuoteMeta(section)
	if len(projects) == 0 {
		return []string{".*:" + quoted}, nil
	}
	patterns := make([]string, len(projects))
	for i, name := range projects {
		if _, ok := ws.Project(name); !ok {
			return nil, fmt.Errorf("unknown project %q", name)
		}
		patterns[i] = regexp.QuoteMeta(name) + ":" + quoted
	}
	return patterns, nil
}

func runSections(stdout io.Writer, argv []string, ws *workspace.Workspace, patterns []string, dryRun bool) error {
	plan, matched, err := buildPlan(ws, patterns)
	if err != nil {
		return invalid(err)
	}
	if dryRun {
		report.DryRun(stdout, plan, matched)
		return nil
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
	if slices.ContainsFunc(plan.Sections, func(s runner.Section) bool { return len(s.Wrappers) > 0 }) {
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
	projectWrapper := map[string]string{}
	for _, name := range plan.Projects {
		p, ok := ws.Project(name)
		if !ok {
			panic("stew: plan section for unregistered project " + name)
		}
		if p.Wrapper != "" {
			projectWrapper[name] = p.Wrapper
		}
	}
	if len(projectWrapper) == 0 {
		projectWrapper = nil
	}
	if err := logs.Start(argv, ws.Wrapper, projectWrapper, plan.Columns, plan.Projects); err != nil {
		return rejected(fmt.Errorf("log error: %w", err))
	}

	r := &runner.Runner{
		Exec:  runner.Shell{KillDelay: killDelay, Force: force},
		Steps: steps,
		OpenLog: func(project, section string) (runner.SectionLog, error) {
			l, err := logs.OpenSection(project, section)
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
