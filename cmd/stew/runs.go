package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rknit/steward/internal/report"
	"github.com/rknit/steward/internal/runlog"
	"github.com/rknit/steward/internal/runner"
	"github.com/rknit/steward/internal/workspace"
)

// newRunsCmd returns the "runs" command group.
func newRunsCmd(stdout io.Writer) *cobra.Command {
	runs := &cobra.Command{
		Use:   "runs",
		Short: "Inspect past runs",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	var porcelain, noPager bool
	show := &cobra.Command{
		Use:   "show <run-id> [<project-phase-regex>...]",
		Short: "Show a run's phases and their logs (run-id may be \"latest\")",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return showRun(stdout, cmd.ErrOrStderr(), args[0], args[1:], porcelain, noPager)
		},
	}
	show.Flags().BoolVar(&porcelain, "porcelain", false, "print tab-separated project, phase, status, duration in ms, and log path")
	show.Flags().BoolVar(&noPager, "no-pager", false, "print directly instead of through a pager")
	runs.AddCommand(show)
	return runs
}

func showRun(stdout, stderr io.Writer, id string, patterns []string, porcelain, noPager bool) error {
	matchers, err := compileKeyPatterns(patterns)
	if err != nil {
		return invalid(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return rejected(err)
	}
	root, err := workspace.FindRoot(cwd)
	if err != nil {
		return invalid(err)
	}
	stewDir := filepath.Join(root, workspace.DirName)

	requested := id
	if id == "latest" {
		id, err = runlog.Latest(stewDir)
		if errors.Is(err, runlog.ErrUnknownRun) {
			return invalid(fmt.Errorf("unknown run %q", requested))
		}
		if err != nil {
			return rejected(fmt.Errorf("read runs: %w", err))
		}
	}
	run, err := runlog.Load(stewDir, id)
	switch {
	case errors.Is(err, runlog.ErrUnknownRun):
		return invalid(fmt.Errorf("unknown run %q", requested))
	case errors.Is(err, runlog.ErrNoManifest):
		return rejected(fmt.Errorf("run %s has no run.json", id))
	case err != nil:
		return rejected(fmt.Errorf("read run %s: %w", id, err))
	}

	phases, err := shownPhases(run)
	if err != nil {
		return rejected(fmt.Errorf("read run %s: %w", id, err))
	}
	if len(matchers) > 0 {
		phases = slices.DeleteFunc(phases, func(p report.ShownPhase) bool {
			key := p.Project + "-" + p.Phase.Used
			return !slices.ContainsFunc(matchers, func(re *regexp.Regexp) bool { return re.MatchString(key) })
		})
		if len(phases) == 0 {
			return rejected(errors.New("no phase matches"))
		}
	}

	logsDir := path.Join(workspace.DirName, "runs", id)
	if porcelain {
		writePorcelain(stdout, phases, logsDir)
		return nil
	}
	var sum *report.ShownSummary
	if len(matchers) == 0 {
		sum = &report.ShownSummary{Results: summaryResults(run.Manifest, phases), Finished: run.Manifest.TotalMS != nil, Logs: logsDir}
		if sum.Finished {
			sum.Total = time.Duration(*run.Manifest.TotalMS) * time.Millisecond
		}
	}
	var b bytes.Buffer
	report.Show(&b, id, run.Manifest.Argv, phases, sum)
	if err := page(stdout, stderr, b.Bytes(), !noPager && isTerminal(stdout)); err != nil {
		return rejected(err)
	}
	return nil
}

// compileKeyPatterns compiles each pattern to match a whole "<project>-<phase>" key.
func compileKeyPatterns(patterns []string) ([]*regexp.Regexp, error) {
	var matchers []*regexp.Regexp
	for _, p := range patterns {
		if _, err := regexp.Compile(p); err != nil {
			return nil, err
		}
		re, err := regexp.Compile("^(?:" + p + ")$")
		if err != nil {
			return nil, err
		}
		matchers = append(matchers, re)
	}
	return matchers, nil
}

// shownPhases lists recorded phases in execution order, then unfinished ones, each with its log.
func shownPhases(run *runlog.Run) ([]report.ShownPhase, error) {
	var phases []report.ShownPhase
	for _, rec := range run.Manifest.Phases {
		p := report.ShownPhase{
			Project:   rec.Project,
			Phase:     runner.Phase{Name: rec.Phase, Used: rec.Used},
			Outcome:   runner.Outcome{Status: runner.Status(rec.Status), Cause: rec.Cause},
			BlockedBy: rec.BlockedBy,
		}
		if rec.DurationMS != nil {
			p.Outcome.Duration = time.Duration(*rec.DurationMS) * time.Millisecond
		}
		phases = append(phases, p)
	}
	unfinished, err := run.Unfinished()
	if err != nil {
		return nil, err
	}
	for _, k := range unfinished {
		phases = append(phases, report.ShownPhase{
			Project: k.Project,
			Phase:   runner.Phase{Name: requestedPhase(run.Manifest.Columns, k.Used), Used: k.Used},
			Outcome: runner.Outcome{Status: report.Unfinished},
		})
	}
	for i := range phases {
		data, ok, err := run.ReadLog(runlog.PhaseKey{Project: phases[i].Project, Used: phases[i].Phase.Used})
		if err != nil {
			return nil, err
		}
		if ok {
			phases[i].Log = data
		}
	}
	return phases, nil
}

// requestedPhase maps a used CI level back to the run's CI column. Other phases keep their name.
func requestedPhase(columns []string, used string) string {
	if strings.HasPrefix(used, "ci.") {
		for _, c := range columns {
			if strings.HasPrefix(c, "ci.") {
				return c
			}
		}
	}
	return used
}

// summaryResults rebuilds the summary table from the manifest's rows and columns and the shown phases.
func summaryResults(m runlog.Manifest, phases []report.ShownPhase) *runner.Results {
	res := &runner.Results{Columns: m.Columns}
	column := make(map[string]int, len(m.Columns))
	for i, c := range m.Columns {
		column[c] = i
	}
	row := make(map[string]int, len(m.Projects))
	for i, p := range m.Projects {
		row[p] = i
		res.Rows = append(res.Rows, runner.Row{Project: p, Cells: make([]runner.Cell, len(m.Columns))})
	}
	for _, p := range phases {
		r, okRow := row[p.Project]
		c, okCol := column[p.Phase.Name]
		if okRow && okCol {
			res.Rows[r].Cells[c] = runner.Cell{Status: p.Outcome.Status, Fallback: p.Phase.Fallback()}
		}
	}
	return res
}

// writePorcelain prints "<project>\t<phase>\t<status>\t<duration-ms>\t<log-path>" per phase.
func writePorcelain(w io.Writer, phases []report.ShownPhase, logsDir string) {
	var b strings.Builder
	for _, p := range phases {
		duration := "-"
		switch p.Outcome.Status {
		case runner.Done, runner.Pass, runner.Skip, runner.Fail:
			duration = strconv.FormatInt(p.Outcome.Duration.Milliseconds(), 10)
		}
		logPath := "-"
		if p.Log != nil {
			logPath = path.Join(logsDir, p.Project+"-"+p.Phase.Used+".log")
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\n", p.Project, p.Phase.Used, p.Outcome.Status, duration, logPath)
	}
	io.WriteString(w, b.String())
}
