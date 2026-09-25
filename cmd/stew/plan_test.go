package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rknit/steward/internal/runner"
	"github.com/rknit/steward/internal/workspace"
)

// testWorkspace writes a workspace with core <- api (api depends on core) and loads it.
// wrapper, if non-empty, becomes the workspace's workspace_wrapper.
func testWorkspace(t *testing.T, wrapper string) *workspace.Workspace {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		if err := writeFile(root, rel, content); err != nil {
			t.Fatal(err)
		}
	}
	write(".stew/projects.toml", `projects = ["api", "core"]`+"\n")
	if wrapper == "" {
		write(".stew/config.toml", workspace.ConfigTemplate)
	} else {
		write(".stew/config.toml", `workspace_wrapper = "`+wrapper+`"`+"\n")
	}
	write("core/stew.toml", `name = "core"
dependencies = []
[setup]
run = "cs"
verify = "cv"
[build]
run = "cb"
verify = ""
[ci.full]
run = "core-full"
[ci.quick]
run = "core-quick"
`)
	write("api/stew.toml", `name = "api"
dependencies = ["core"]
[setup]
run = ""
verify = ""
[build]
run = "ab"
verify = "av"
[ci.full]
run = "api-full"
`)
	ws, err := workspace.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func phaseNames(job runner.Job) []string {
	var names []string
	for _, ph := range job.Phases {
		names = append(names, ph.Name+"="+ph.Used)
	}
	return names
}

func TestBuildPlan(t *testing.T) {
	ws := testWorkspace(t, "")
	tests := []struct {
		command string
		names   []string
		level   workspace.Level
		columns []string
		jobs    map[string][]string
	}{
		{"setup", nil, workspace.LevelFull, []string{"setup"}, map[string][]string{
			"core": {"setup=setup"}, "api": {"setup=setup"},
		}},
		{"build", []string{"api"}, workspace.LevelFull, []string{"setup", "build"}, map[string][]string{
			"core": {"setup=setup", "build=build"}, "api": {"setup=setup", "build=build"},
		}},
		// Only named projects get CI; core is a dependency only.
		{"ci", []string{"api"}, workspace.LevelQuick, []string{"setup", "build", "ci.quick"}, map[string][]string{
			"core": {"setup=setup", "build=build"}, "api": {"setup=setup", "build=build", "ci.quick=ci.full"},
		}},
		// Naming both a project and its dependency runs CI for both.
		{"ci", []string{"api", "core"}, workspace.LevelPreCommit, []string{"setup", "build", "ci.pre-commit"},
			map[string][]string{
				"core": {"setup=setup", "build=build", "ci.pre-commit=ci.quick"},
				"api":  {"setup=setup", "build=build", "ci.pre-commit=ci.full"},
			}},
	}
	for _, tt := range tests {
		plan, err := buildPlan(ws, tt.command, tt.names, tt.level)
		if err != nil {
			t.Fatalf("%s %v: %v", tt.command, tt.names, err)
		}
		if !slices.Equal(plan.Columns, tt.columns) {
			t.Errorf("%s %v: columns = %v, want %v", tt.command, tt.names, plan.Columns, tt.columns)
		}
		if len(plan.Jobs) != 2 || plan.Jobs[0].Project != "core" || plan.Jobs[1].Project != "api" {
			t.Fatalf("%s %v: jobs = %+v", tt.command, tt.names, plan.Jobs)
		}
		for _, job := range plan.Jobs {
			if got := phaseNames(job); !slices.Equal(got, tt.jobs[job.Project]) {
				t.Errorf("%s %v: %s phases = %v, want %v", tt.command, tt.names, job.Project, got, tt.jobs[job.Project])
			}
			if len(job.Wrappers) != 0 {
				t.Errorf("%s %v: %s wrappers = %v, want none", tt.command, tt.names, job.Project, job.Wrappers)
			}
		}
	}

	plan, _ := buildPlan(ws, "ci", nil, workspace.LevelFull)
	core, api := plan.Jobs[0], plan.Jobs[1]
	if core.Phases[0] != (runner.Phase{Name: "setup", Used: "setup", Run: "cs", Verify: "cv"}) {
		t.Errorf("core setup = %+v", core.Phases[0])
	}
	if api.Phases[2] != (runner.Phase{Name: "ci.full", Used: "ci.full", Run: "api-full", CI: true}) {
		t.Errorf("api ci = %+v", api.Phases[2])
	}
	if !slices.Equal(api.Deps, []string{"core"}) || api.Dir != ws.Root+"/api" {
		t.Errorf("api deps/dir = %v %q", api.Deps, api.Dir)
	}

	if _, err := buildPlan(ws, "build", []string{"nope"}, workspace.LevelFull); err == nil {
		t.Error("unknown project accepted")
	}
}

func TestBuildPlanWrapper(t *testing.T) {
	ws := testWorkspace(t, "tool exec . {{STEW_STEP}}")
	plan, err := buildPlan(ws, "build", []string{"api"}, workspace.LevelFull)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range plan.Jobs {
		if !slices.Equal(job.Wrappers, []string{"tool exec . {{STEW_STEP}}"}) {
			t.Errorf("%s wrappers = %v, want [tool exec . {{STEW_STEP}}]", job.Project, job.Wrappers)
		}
	}
}

// writeFile writes content to root/rel, creating parent directories.
func writeFile(root, rel, content string) error {
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
