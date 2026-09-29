package main

import (
	"context"
	"crypto/rand"
	"errors"
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

func newRunCmd(proc process, argv []string) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "run <regex>...",
		Short: "Run sections whose <project>:<section> key matches, after the sections they require",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := loadWorkspace(proc)
			if err != nil {
				return err
			}
			return runSections(proc, argv, ws, args, dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would run, in order, without running it")
	return cmd
}

// newAliasCmd returns the command use, which runs one section in the named projects, or in every project that has it.
// For section "ci" the section is ci.<level>.
func newAliasCmd(proc process, argv []string, use, section, short string) *cobra.Command {
	var level string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   use + " [project...]",
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := section
			if section == "ci" {
				name = "ci." + level
				if !workspace.ValidSectionName(name) {
					return invalid(fmt.Errorf("invalid CI level %q", level))
				}
			}
			ws, err := loadWorkspace(proc)
			if err != nil {
				return err
			}
			patterns, err := aliasPatterns(ws, name, args)
			if err != nil {
				return invalid(err)
			}
			return runSections(proc, argv, ws, patterns, dryRun)
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

func runSections(proc process, argv []string, ws *workspace.Workspace, patterns []string, dryRun bool) error {
	plan, matched, err := buildPlan(ws, patterns)
	if err != nil {
		return invalid(err)
	}
	if dryRun {
		report.DryRun(proc.stdout, plan, matched)
		return nil
	}
	ctx, force, release := catchStops()
	defer release()
	if err := ensureTrust(ctx, force, proc, ws); err != nil {
		return err
	}

	if err := runner.AdoptOrphans(); err != nil {
		return rejected(fmt.Errorf("cannot adopt orphaned processes: %w", err))
	}
	var steps runner.Steps
	if slices.ContainsFunc(plan.Sections, func(s runner.Section) bool { return len(s.Wrappers) > 0 }) {
		d, err := runner.NewStepDir(proc.tempDir())
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
		Exec:  runner.Shell{KillDelay: killDelay, Force: force, Environ: proc.env},
		Steps: steps,
		OpenLog: func(project, section string) (runner.SectionLog, error) {
			l, err := logs.OpenSection(project, section)
			if err != nil {
				return nil, err
			}
			return l, nil
		},
		Report: newReporter(proc.stdout),
		Record: logs,
		Now:    time.Now,
		RunID:  logs.ID,
		Root:   ws.Root,
	}
	res := r.Run(ctx, plan)
	total := time.Since(start)
	report.Summary(proc.stdout, res, total, path.Join(workspace.DirName, "runs", logs.ID))
	code := res.ExitCode()
	if err := logs.Finish(total); err != nil {
		return &exitError{code: max(code, 1), err: fmt.Errorf("log error: %w", err)}
	}
	if code != 0 {
		return &exitError{code: code}
	}
	return nil
}

// catchStops handles stop signals (Ctrl-C, SIGTERM, SIGHUP) until release. The first one cancels ctx with a
// runner.Interrupt cause; a second one closes force, which force-kills the command stew is waiting for.
func catchStops() (ctx context.Context, force <-chan struct{}, release func()) {
	ctx, cancel := context.WithCancelCause(context.Background())
	finished := make(chan struct{})
	forced := make(chan struct{})
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		select {
		case sig := <-sigs:
			cancel(runner.Interrupt{Signal: sig.(syscall.Signal)})
		case <-finished:
			return
		}
		select {
		case <-sigs:
			close(forced)
		case <-finished:
		}
	}()
	release = func() {
		signal.Stop(sigs)
		close(finished)
		cancel(nil)
	}
	return ctx, forced, release
}

// withStops runs fn under catchStops. A stop signal makes it exit 128+n, even when fn succeeded.
func withStops(fn func(ctx context.Context, force <-chan struct{}) error) error {
	ctx, force, release := catchStops()
	err := fn(ctx, force)
	release()
	if err != nil {
		return err
	}
	return interrupted(ctx)
}

// interrupted returns exit 128+n, with no message, when stop signal n cancelled ctx, and nil otherwise.
func interrupted(ctx context.Context) error {
	var i runner.Interrupt
	if errors.As(context.Cause(ctx), &i) {
		return &exitError{code: 128 + int(i.Signal)}
	}
	return nil
}

// newReporter animates when stdout is a terminal and prints plain lines otherwise.
func newReporter(stdout io.Writer) runner.Reporter {
	if f, ok := stdout.(*os.File); ok && isTerminal(stdout) {
		return report.NewTTY(stdout, terminalSize(f))
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
