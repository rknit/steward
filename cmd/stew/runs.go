package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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
func newRunsCmd(proc process) *cobra.Command {
	runs := &cobra.Command{
		Use:   "runs",
		Short: "Inspect and prune past runs",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	var porcelain, noPager bool
	show := &cobra.Command{
		Use:   "show <run-id> [<project:section-regex>...]",
		Short: "Show a run's sections and their logs (run-id may be \"latest\")",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return showRun(proc, args[0], args[1:], porcelain, noPager)
		},
	}
	show.Flags().BoolVar(&porcelain, "porcelain", false, "print tab-separated project, section, status, duration in ms, and log path")
	show.Flags().BoolVar(&noPager, "no-pager", false, "print directly instead of through a pager")
	var listPorcelain, listNoPager bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List runs, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return listRuns(proc, listPorcelain, listNoPager)
		},
	}
	list.Flags().BoolVar(&listPorcelain, "porcelain", false, "print tab-separated start time, run ID, result, total in ms, and command")
	list.Flags().BoolVar(&listNoPager, "no-pager", false, "print directly instead of through a pager")
	runs.AddCommand(show, list, newRunsPruneCmd(proc))
	return runs
}

// findStewDir returns the .stew directory of the workspace above proc's directory without loading the workspace.
func findStewDir(proc process) (string, error) {
	root, err := proc.findRoot()
	if err != nil {
		return "", invalid(err)
	}
	return filepath.Join(root, workspace.DirName), nil
}

// listedRun is one row of stew runs list. started is zero and text fields are "-" when unknown.
type listedRun struct {
	started time.Time
	id      string
	result  string
	totalMS string
	total   string
	command string
}

func listRuns(proc process, porcelain, noPager bool) error {
	stewDir, err := findStewDir(proc)
	if err != nil {
		return err
	}
	ids, err := runlog.IDs(stewDir)
	if err != nil {
		return rejected(fmt.Errorf("read runs: %w", err))
	}
	var rows []listedRun
	for _, id := range slices.Backward(ids) {
		rows = append(rows, listRun(stewDir, id))
	}

	if porcelain {
		var b strings.Builder
		for _, r := range rows {
			fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\n", startedText(r.started, proc.loc, time.RFC3339), r.id, r.result, r.totalMS, r.command)
		}
		io.WriteString(proc.stdout, b.String())
		return nil
	}
	var b bytes.Buffer
	if len(rows) == 0 {
		b.WriteString("no runs\n")
	} else {
		table := [][]string{{"started", "run", "result", "total", "command"}}
		for _, r := range rows {
			table = append(table, []string{startedText(r.started, proc.loc, time.DateTime), r.id, r.result, r.total, r.command})
		}
		report.Table(&b, table)
	}
	if err := page(proc, b.Bytes(), !noPager && isTerminal(proc.stdout)); err != nil {
		return rejected(err)
	}
	return nil
}

// listRun reads one run for stew runs list. A run whose run.json cannot be loaded is "unreadable".
func listRun(stewDir, id string) listedRun {
	r := listedRun{id: id, result: "unreadable", totalMS: "-", total: "-", command: "-"}
	if started, err := runlog.StartTime(id); err == nil {
		r.started = started
	}
	run, err := runlog.Load(stewDir, id)
	if err != nil {
		return r
	}
	r.result = run.Manifest.Result()
	r.command = report.Command(run.Manifest.Argv)
	if run.Manifest.TotalMS != nil {
		r.totalMS = strconv.FormatInt(*run.Manifest.TotalMS, 10)
		r.total = report.FormatDuration(time.Duration(*run.Manifest.TotalMS) * time.Millisecond)
	}
	return r
}

// startedText formats a run's start time in loc, or "-" when the run ID has no valid time.
func startedText(t time.Time, loc *time.Location, layout string) string {
	if t.IsZero() {
		return "-"
	}
	return t.In(loc).Format(layout)
}

func showRun(proc process, id string, patterns []string, porcelain, noPager bool) error {
	matchers, err := compileKeyPatterns(patterns)
	if err != nil {
		return invalid(err)
	}
	stewDir, err := findStewDir(proc)
	if err != nil {
		return err
	}

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

	sections, err := shownSections(run, len(matchers) > 0)
	if err != nil {
		return rejected(fmt.Errorf("read run %s: %w", id, err))
	}
	if len(matchers) > 0 {
		sections = slices.DeleteFunc(sections, func(s report.ShownSection) bool {
			key := s.Project + ":" + s.Section
			return !slices.ContainsFunc(matchers, func(re *regexp.Regexp) bool { return re.MatchString(key) })
		})
		if len(sections) == 0 {
			return rejected(errors.New("no section matches"))
		}
	}

	logsDir := path.Join(workspace.DirName, "runs", id)
	if porcelain {
		writePorcelain(proc.stdout, sections, logsDir)
		return nil
	}
	var sum *report.ShownSummary
	if len(matchers) == 0 {
		sum = &report.ShownSummary{Results: summaryResults(run.Manifest, sections), Finished: run.Manifest.TotalMS != nil, Logs: logsDir}
		if sum.Finished {
			sum.Total = time.Duration(*run.Manifest.TotalMS) * time.Millisecond
		}
	}
	var projectWrappers []report.ProjectWrapper
	for _, p := range run.Manifest.Projects {
		if w := run.Manifest.ProjectWrapper[p]; w != "" {
			projectWrappers = append(projectWrappers, report.ProjectWrapper{Project: p, Wrapper: w})
		}
	}
	var b bytes.Buffer
	report.Show(&b, id, run.Manifest.Argv, run.Manifest.WorkspaceWrapper, projectWrappers, sections, sum)
	if err := page(proc, b.Bytes(), !noPager && isTerminal(proc.stdout)); err != nil {
		return rejected(err)
	}
	return nil
}

// compileKeyPatterns compiles each pattern to match a whole "<project>:<section>" key.
func compileKeyPatterns(patterns []string) ([]*regexp.Regexp, error) {
	var matchers []*regexp.Regexp
	for _, p := range patterns {
		re, err := workspace.CompileKeyPattern(p)
		if err != nil {
			return nil, err
		}
		matchers = append(matchers, re)
	}
	return matchers, nil
}

// shownSections lists recorded sections in execution order, then unfinished ones, each with its log.
// A blocked section's line lists the requirements that failed, or with filtered, every one in blocked_by.
func shownSections(run *runlog.Run, filtered bool) ([]report.ShownSection, error) {
	status := make(map[string]runner.Status, len(run.Manifest.Sections))
	for _, rec := range run.Manifest.Sections {
		status[rec.Project+":"+rec.Section] = runner.Status(rec.Status)
	}
	var sections []report.ShownSection
	for _, rec := range run.Manifest.Sections {
		s := report.ShownSection{
			Project: rec.Project,
			Section: rec.Section,
			Outcome: runner.Outcome{Status: runner.Status(rec.Status), Cause: rec.Cause},
		}
		for _, req := range rec.BlockedBy {
			if filtered || status[req] == runner.Fail {
				s.BlockedBy = append(s.BlockedBy, req)
			}
		}
		if rec.DurationMS != nil {
			s.Outcome.Duration = time.Duration(*rec.DurationMS) * time.Millisecond
		}
		sections = append(sections, s)
	}
	unfinished, err := run.Unfinished()
	if err != nil {
		return nil, err
	}
	for _, k := range unfinished {
		sections = append(sections, report.ShownSection{
			Project: k.Project, Section: k.Section, Outcome: runner.Outcome{Status: report.Unfinished},
		})
	}
	for i := range sections {
		data, ok, err := run.ReadLog(runlog.Key{Project: sections[i].Project, Section: sections[i].Section})
		if err != nil {
			return nil, err
		}
		if ok {
			sections[i].Log = data
		}
	}
	return sections, nil
}

// summaryResults rebuilds the summary table from the manifest's rows and columns and the shown sections.
func summaryResults(m runlog.Manifest, sections []report.ShownSection) *runner.Results {
	res := &runner.Results{Columns: m.Columns}
	column := make(map[string]int, len(m.Columns))
	for i, c := range m.Columns {
		column[c] = i
	}
	row := make(map[string]int, len(m.Projects))
	for i, p := range m.Projects {
		row[p] = i
		res.Rows = append(res.Rows, runner.Row{Project: p, Cells: make([]runner.Status, len(m.Columns))})
	}
	for _, s := range sections {
		r, okRow := row[s.Project]
		c, okCol := column[s.Section]
		if okRow && okCol {
			res.Rows[r].Cells[c] = s.Outcome.Status
		}
	}
	return res
}

// writePorcelain prints "<project>\t<section>\t<status>\t<duration-ms>\t<log-path>" per section.
func writePorcelain(w io.Writer, sections []report.ShownSection, logsDir string) {
	var b strings.Builder
	for _, s := range sections {
		duration := "-"
		switch s.Outcome.Status {
		case runner.Done, runner.Skip, runner.Fail:
			duration = strconv.FormatInt(s.Outcome.Duration.Milliseconds(), 10)
		}
		logPath := "-"
		if s.Log != nil {
			logPath = path.Join(logsDir, s.Project+":"+s.Section+".log")
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\n", s.Project, s.Section, s.Outcome.Status, duration, logPath)
	}
	io.WriteString(w, b.String())
}
