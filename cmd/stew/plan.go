package main

import (
	"path/filepath"
	"slices"

	"github.com/rknit/steward/internal/runner"
	"github.com/rknit/steward/internal/workspace"
)

// buildPlan selects the sections matching patterns, plus the sections they require, into a runner plan.
// matched holds the key of every node a pattern matched directly.
func buildPlan(ws *workspace.Workspace, patterns []string) (runner.Plan, map[string]bool, error) {
	nodes, err := ws.Select(patterns)
	if err != nil {
		return runner.Plan{}, nil, err
	}
	var plan runner.Plan
	matched := make(map[string]bool)
	position := make(map[workspace.Key]int, len(nodes))
	for i, n := range nodes {
		position[n.Key] = i
		if n.Matched {
			matched[n.Key.String()] = true
		}
		if !slices.Contains(plan.Columns, n.Key.Section) {
			plan.Columns = append(plan.Columns, n.Key.Section)
		}
		if !slices.Contains(plan.Projects, n.Key.Project) {
			plan.Projects = append(plan.Projects, n.Key.Project)
		}
		reqs := slices.SortedFunc(slices.Values(n.Section.Requires), func(a, b workspace.Key) int {
			return position[a] - position[b]
		})
		requires := make([]string, len(reqs))
		for j, k := range reqs {
			requires[j] = k.String()
		}
		plan.Sections = append(plan.Sections, runner.Section{
			Project:  n.Project.Name,
			Name:     n.Key.Section,
			Dir:      filepath.Join(ws.Root, filepath.FromSlash(n.Project.Path)),
			Wrappers: wrappers(ws.Wrapper, n.Project.Wrapper),
			Run:      n.Section.Run,
			SkipIf:   n.Section.SkipIf,
			Verify:   n.Section.Verify,
			Requires: requires,
		})
	}
	return plan, matched, nil
}

// wrappers returns the non-empty wrappers, outermost first.
func wrappers(all ...string) []string {
	return slices.DeleteFunc(all, func(w string) bool { return w == "" })
}
