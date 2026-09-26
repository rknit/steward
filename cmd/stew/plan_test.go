package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rknit/steward/internal/runner"
	"github.com/rknit/steward/internal/workspace"
)

// testWorkspace writes core and api, where api:build requires core:build, and loads it.
func testWorkspace(t *testing.T, wrapper, apiWrapper string) *workspace.Workspace {
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
project_wrapper = ""
[setup]
run = "cs"
skip_if = "ck"
[build]
run = "cb"
requires = ["core:setup"]
`)
	write("api/stew.toml", `name = "api"
project_wrapper = "`+apiWrapper+`"
[build]
run = "ab"
verify = "av"
requires = ["core:build"]
[ci.full]
run = "api-full"
requires = ["api:build"]
`)
	ws, err := workspace.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestBuildPlan(t *testing.T) {
	t.Parallel()
	ws := testWorkspace(t, "", "")
	plan, _, err := buildPlan(ws, []string{"api:ci.full"})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, s := range plan.Sections {
		keys = append(keys, s.Key())
	}
	if !slices.Equal(keys, []string{"core:setup", "core:build", "api:build", "api:ci.full"}) {
		t.Errorf("keys = %q", keys)
	}
	if !slices.Equal(plan.Columns, []string{"setup", "build", "ci.full"}) {
		t.Errorf("columns = %q", plan.Columns)
	}
	if !slices.Equal(plan.Projects, []string{"core", "api"}) {
		t.Errorf("projects = %q", plan.Projects)
	}
	want := runner.Section{Project: "api", Name: "build", Dir: filepath.Join(ws.Root, "api"),
		Run: "ab", Verify: "av", Requires: []string{"core:build"}}
	if got := plan.Sections[2]; !sectionsEqual(got, want) {
		t.Errorf("api:build = %+v, want %+v", got, want)
	}
	if got := plan.Sections[0]; got.SkipIf != "ck" || got.Run != "cs" {
		t.Errorf("core:setup = %+v", got)
	}
	if _, _, err := buildPlan(ws, []string{"web:build"}); err == nil {
		t.Error("unmatched pattern accepted")
	}
	_, matched, _ := buildPlan(ws, []string{"api:ci.full"})
	if len(matched) != 1 || !matched["api:ci.full"] {
		t.Errorf("matched = %v", matched)
	}
}

// Requires are listed in execution order, not file order.
func TestBuildPlanRequiresInExecutionOrder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for rel, content := range map[string]string{
		".stew/projects.toml": `projects = ["a", "b"]` + "\n",
		".stew/config.toml":   workspace.ConfigTemplate,
		"a/stew.toml":         "name = \"a\"\nproject_wrapper = \"\"\n[x]\nrun = \"\"\nrequires = [\"b:y\", \"a:y\"]\n[y]\nrun = \"\"\n",
		"b/stew.toml":         "name = \"b\"\nproject_wrapper = \"\"\n[y]\nrun = \"\"\n",
	} {
		if err := writeFile(root, rel, content); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := workspace.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, _, err := buildPlan(ws, []string{"a:x"})
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Sections[2].Requires; !slices.Equal(got, []string{"a:y", "b:y"}) {
		t.Errorf("requires = %q", got)
	}
}

func sectionsEqual(a, b runner.Section) bool {
	return a.Project == b.Project && a.Name == b.Name && a.Dir == b.Dir && a.Run == b.Run &&
		a.SkipIf == b.SkipIf && a.Verify == b.Verify && slices.Equal(a.Requires, b.Requires) &&
		slices.Equal(a.Wrappers, b.Wrappers)
}

func TestBuildPlanWrapper(t *testing.T) {
	t.Parallel()
	const outer, inner = "tool exec . {{STEW_STEP}}", "other run {{STEW_STEP}}"
	tests := []struct {
		workspace, api string
		want           map[string][]string
	}{
		{outer, "", map[string][]string{"core": {outer}, "api": {outer}}},
		{outer, inner, map[string][]string{"core": {outer}, "api": {outer, inner}}},
		{"", inner, map[string][]string{"core": nil, "api": {inner}}},
	}
	for _, tt := range tests {
		ws := testWorkspace(t, tt.workspace, tt.api)
		plan, _, err := buildPlan(ws, []string{"api:build"})
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range plan.Sections {
			if want := tt.want[s.Project]; !slices.Equal(s.Wrappers, want) {
				t.Errorf("%q/%q: %s wrappers = %q, want %q", tt.workspace, tt.api, s.Key(), s.Wrappers, want)
			}
		}
	}
}

func TestAliasPatterns(t *testing.T) {
	t.Parallel()
	ws := testWorkspace(t, "", "")
	tests := []struct {
		section  string
		projects []string
		want     []string
		err      string
	}{
		{"build", nil, []string{`.*:build`}, ""},
		{"ci.pre-commit", nil, []string{`.*:ci\.pre-commit`}, ""},
		{"build", []string{"api", "core"}, []string{`api:build`, `core:build`}, ""},
		{"build", []string{"web"}, nil, `unknown project "web"`},
	}
	for _, tt := range tests {
		got, err := aliasPatterns(ws, tt.section, tt.projects)
		if tt.err != "" {
			if err == nil || err.Error() != tt.err {
				t.Errorf("%v: err = %v, want %q", tt.projects, err, tt.err)
			}
			continue
		}
		if err != nil || !slices.Equal(got, tt.want) {
			t.Errorf("%s %v = %q, %v; want %q", tt.section, tt.projects, got, err, tt.want)
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
