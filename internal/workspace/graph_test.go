package workspace

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// proj builds an in-memory project with the given dependencies.
func proj(name string, deps ...string) *Project {
	return &Project{Name: name, Path: name, Dependencies: deps, CI: map[Level]string{LevelFull: ""}}
}

func names(ps []*Project) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

func TestTopologicalOrderWithNameTieBreak(t *testing.T) {
	// app -> {api, backend} -> core; lib has no deps; web -> api.
	ws, err := newWorkspace("/r", []*Project{
		proj("web", "api"),
		proj("app", "backend", "api"),
		proj("lib"),
		proj("backend", "core"),
		proj("core"),
		proj("api", "core"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"core", "api", "backend", "app", "lib", "web"}
	if got := names(ws.Projects); !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestSelect(t *testing.T) {
	ws, err := newWorkspace("/r", []*Project{
		proj("app", "api", "backend"),
		proj("api", "core"),
		proj("backend", "core"),
		proj("core"),
		proj("lib"),
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		args      []string
		wantOrder []string
		wantNamed []string
	}{
		{nil, []string{"core", "api", "backend", "app", "lib"}, []string{"app", "api", "backend", "core", "lib"}},
		{[]string{"api"}, []string{"core", "api"}, []string{"api"}},
		{[]string{"app"}, []string{"core", "api", "backend", "app"}, []string{"app"}},
		{[]string{"lib", "api", "api"}, []string{"core", "api", "lib"}, []string{"api", "lib"}},
	}
	for _, tt := range tests {
		sel, named, err := ws.Select(tt.args)
		if err != nil {
			t.Fatalf("Select(%v): %v", tt.args, err)
		}
		if got := names(sel); !slices.Equal(got, tt.wantOrder) {
			t.Errorf("Select(%v) order = %v, want %v", tt.args, got, tt.wantOrder)
		}
		var gotNamed []string
		for n := range named {
			gotNamed = append(gotNamed, n)
		}
		slices.Sort(gotNamed)
		slices.Sort(tt.wantNamed)
		if !slices.Equal(gotNamed, tt.wantNamed) {
			t.Errorf("Select(%v) named = %v, want %v", tt.args, gotNamed, tt.wantNamed)
		}
	}

	if _, _, err := ws.Select([]string{"api", "nope"}); err == nil || !strings.Contains(err.Error(), `unknown project "nope"`) {
		t.Errorf("unknown name: err = %v", err)
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name     string
		projects []*Project
		wantErr  string
	}{
		{"duplicate name", []*Project{proj("a"), {Name: "a", Path: "other"}}, `duplicate project name "a" (a and other)`},
		{"self dependency", []*Project{proj("a", "a")}, `a/stew.toml: project "a" depends on itself`},
		{"unknown dependency", []*Project{proj("a", "b")}, `a/stew.toml: project "a" depends on unknown project "b"`},
		{"cycle", []*Project{proj("a", "b"), proj("b", "c"), proj("c", "a")}, "dependency cycle: a -> b -> c -> a"},
		{"cycle off the root", []*Project{proj("a", "b"), proj("b", "c"), proj("c", "b")}, "dependency cycle: b -> c -> b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newWorkspace("/r", tt.projects)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	root := newRoot(t)
	write := func(rel, content string) {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ManifestFile), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("libs/core", string(Template("core")))
	write("services/api", strings.Replace(string(Template("api")), "dependencies = []", `dependencies = ["core"]`, 1))
	if err := SaveRegistry(root, []string{"services/api", "libs/core"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath(root), []byte(ConfigTemplate), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Wrapper != "" {
		t.Errorf("Wrapper = %q, want empty", ws.Wrapper)
	}
	if got := names(ws.Projects); !slices.Equal(got, []string{"core", "api"}) {
		t.Errorf("order = %v", got)
	}
	if p, ok := ws.Project("api"); !ok || p.Path != "services/api" {
		t.Errorf("Project(api) = %+v, %v", p, ok)
	}

	if err := os.WriteFile(ConfigPath(root), []byte(`workspace_wrapper = "tool exec . {{STEW_STEP}}"`), 0o644); err != nil {
		t.Fatal(err)
	}
	if ws, err := Load(root); err != nil {
		t.Fatal(err)
	} else if ws.Wrapper != "tool exec . {{STEW_STEP}}" {
		t.Errorf("Wrapper = %q, want %q", ws.Wrapper, "tool exec . {{STEW_STEP}}")
	}

	if err := SaveRegistry(root, []string{"libs/core", "missing"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), `project "missing": missing stew.toml`) {
		t.Errorf("missing manifest: err = %v", err)
	}
}
