package main

import (
	"path/filepath"
	"slices"

	"github.com/rknit/steward/internal/runner"
	"github.com/rknit/steward/internal/workspace"
)

// buildPlan turns a command ("setup", "build", or "ci") and project names into a runner plan.
// Every selected project gets setup (and build unless the command is setup);
// only named projects get CI.
func buildPlan(ws *workspace.Workspace, command string, names []string, level workspace.Level) (runner.Plan, error) {
	selected, named, err := ws.Select(names)
	if err != nil {
		return runner.Plan{}, err
	}
	ciName := "ci." + string(level)
	columns := []string{"setup"}
	if command != "setup" {
		columns = append(columns, "build")
	}
	if command == "ci" {
		columns = append(columns, ciName)
	}

	plan := runner.Plan{Columns: columns}
	for _, p := range selected {
		phases := []runner.Phase{{Name: "setup", Used: "setup", Run: p.Setup.Run, Verify: p.Setup.Verify}}
		if command != "setup" {
			phases = append(phases, runner.Phase{Name: "build", Used: "build", Run: p.Build.Run, Verify: p.Build.Verify})
		}
		if command == "ci" && named[p.Name] {
			used, run := p.ResolveCI(level)
			phases = append(phases, runner.Phase{Name: ciName, Used: "ci." + string(used), Run: run, CI: true})
		}
		plan.Jobs = append(plan.Jobs, runner.Job{
			Project:  p.Name,
			Dir:      filepath.Join(ws.Root, filepath.FromSlash(p.Path)),
			Deps:     p.Dependencies,
			Phases:   phases,
			Wrappers: wrappers(ws.Wrapper),
		})
	}
	return plan, nil
}

// wrappers returns the non-empty wrappers, outermost first.
func wrappers(all ...string) []string {
	return slices.DeleteFunc(all, func(w string) bool { return w == "" })
}
