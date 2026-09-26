package workspace

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// proj builds a project; each section spec is "name" or "name<-a:b,c:d".
func proj(name string, sections ...string) *Project {
	p := &Project{Name: name, Path: name, Sections: map[string]*Section{}}
	for _, spec := range sections {
		sec, reqs, _ := strings.Cut(spec, "<-")
		s := &Section{Run: name + "-" + sec}
		if reqs != "" {
			for _, r := range strings.Split(reqs, ",") {
				k, _ := ParseKey(r)
				s.Requires = append(s.Requires, k)
			}
		}
		p.Sections[sec] = s
	}
	return p
}

func keys(nodes []Node) []string {
	var out []string
	for _, n := range nodes {
		k := n.Key.String()
		if n.Matched {
			k += "*"
		}
		out = append(out, k)
	}
	return out
}

// example is the spec's worked example graph.
func example(t *testing.T) *Workspace {
	t.Helper()
	ws, err := newWorkspace("/w", []*Project{
		proj("web", "build<-api:build,api:test", "e2e<-api:image,web:build"),
		proj("core", "setup", "lint", "build<-core:setup"),
		proj("docs", "build<-core:build"),
		proj("api", "setup", "build<-api:setup,core:build", "test<-api:build", "image<-api:build"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestSelectOrderAndTieBreak(t *testing.T) {
	ws := example(t)
	got, err := ws.Select([]string{"web:e2e", "api:test", "docs:build", "core:lint"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"api:setup", "core:lint*", "core:setup", "core:build", "api:build", "api:image", "api:test*",
		"docs:build*", "web:build", "web:e2e*"}
	if !slices.Equal(keys(got), want) {
		t.Errorf("order = %q\nwant    %q", keys(got), want)
	}
	if got[0].Project.Name != "api" || got[0].Section.Run != "api-setup" {
		t.Errorf("node 0 = %+v", got[0])
	}
}

func TestSelect(t *testing.T) {
	ws := example(t)
	tests := []struct {
		patterns []string
		want     []string
	}{
		{[]string{"core:build"}, []string{"core:setup", "core:build*"}},
		{[]string{".*:setup"}, []string{"api:setup*", "core:setup*"}},
		// Overlapping patterns select a section once.
		{[]string{"api:.*", ".*:build"}, []string{"api:setup*", "core:setup", "core:build*", "api:build*",
			"api:image*", "api:test*", "docs:build*", "web:build*"}},
		// A pattern must match the whole key.
		{[]string{"a.*:setup"}, []string{"api:setup*"}},
	}
	for _, tt := range tests {
		got, err := ws.Select(tt.patterns)
		if err != nil {
			t.Fatalf("%q: %v", tt.patterns, err)
		}
		if !slices.Equal(keys(got), tt.want) {
			t.Errorf("%q = %q, want %q", tt.patterns, keys(got), tt.want)
		}
	}
}

func TestSelectErrors(t *testing.T) {
	ws := example(t)
	tests := []struct {
		patterns []string
		want     string
	}{
		{[]string{"web:setup"}, `pattern "web:setup" matches no section`},
		{[]string{"api:build", `.*:ci\.pre-commit`}, `pattern ".*:ci\.pre-commit" matches no section`},
		{[]string{"api"}, `pattern "api" matches no section`},
		{[]string{"("}, "error parsing regexp"},
	}
	for _, tt := range tests {
		_, err := ws.Select(tt.patterns)
		if err == nil || !strings.HasPrefix(err.Error(), tt.want) {
			t.Errorf("%q: err = %v, want prefix %q", tt.patterns, err, tt.want)
		}
	}
}

func TestProjectsSortedByName(t *testing.T) {
	var names []string
	for _, p := range example(t).Projects {
		names = append(names, p.Name)
	}
	if !slices.Equal(names, []string{"api", "core", "docs", "web"}) {
		t.Errorf("projects = %q", names)
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name     string
		projects []*Project
		want     string
	}{
		{"duplicate name", []*Project{proj("a"), {Name: "a", Path: "other", Sections: map[string]*Section{}}},
			`duplicate project name "a" (a and other)`},
		{"unknown project", []*Project{proj("a", "build<-core:build")},
			`a/stew.toml: [build]: requires "core:build": unknown project "core"`},
		{"unknown section", []*Project{proj("a", "build<-b:build"), proj("b", "setup")},
			`a/stew.toml: [build]: requires "b:build": b has no section "build"`},
		{"cycle", []*Project{proj("api", "build<-core:build"), proj("core", "build<-api:build")},
			"cycle: api:build -> core:build -> api:build"},
		{"cycle in one project", []*Project{proj("a", "x<-a:y", "y<-a:x")},
			"cycle: a:x -> a:y -> a:x"},
	}
	for _, tt := range tests {
		_, err := newWorkspace("/w", tt.projects)
		if err == nil || err.Error() != tt.want {
			t.Errorf("%s: err = %v, want %q", tt.name, err, tt.want)
		}
	}
}

func TestCheckNewProject(t *testing.T) {
	ws := example(t)
	tests := []struct {
		p    *Project
		want string
	}{
		{proj("new", "build<-core:build", "test<-new:build"), ""},
		{proj("new", "build<-lib:build"), `new/stew.toml: [build]: requires "lib:build": unknown project "lib"; add it first`},
		{proj("new", "build<-core:test"), `new/stew.toml: [build]: requires "core:test": core has no section "test"`},
		{proj("new", "build<-new:setup"), `new/stew.toml: [build]: requires "new:setup": new has no section "setup"`},
		{proj("new", "build<-new:setup", "setup<-new:build"), `new/stew.toml: cycle: new:build -> new:setup -> new:build`},
		{proj("new", "build<-new:setup,core:build", "setup"), ""},
	}
	for _, tt := range tests {
		got := ""
		if err := ws.CheckNewProject(tt.p); err != nil {
			got = err.Error()
		}
		if got != tt.want {
			t.Errorf("err = %q, want %q", got, tt.want)
		}
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
	write("libs/core", "name = \"core\"\nproject_wrapper = \"\"\nproject_trust = \"\"\n[build]\nrun = \"b\"\n")
	write("services/api", "name = \"api\"\nproject_wrapper = \"\"\nproject_trust = \"\"\n[build]\nrun = \"b\"\nrequires = [\"core:build\"]\n")
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
	var names []string
	for _, p := range ws.Projects {
		names = append(names, p.Name)
	}
	if !slices.Equal(names, []string{"api", "core"}) {
		t.Errorf("projects = %q", names)
	}
	if p, ok := ws.Project("api"); !ok || p.Path != "services/api" {
		t.Errorf("Project(api) = %+v, %v", p, ok)
	}

	if err := os.WriteFile(ConfigPath(root), []byte("workspace_wrapper = \"tool exec . {{STEW_STEP}}\"\nworkspace_trust = \"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ws, err := Load(root); err != nil {
		t.Fatal(err)
	} else if ws.Wrapper != "tool exec . {{STEW_STEP}}" {
		t.Errorf("Wrapper = %q", ws.Wrapper)
	}

	if err := SaveRegistry(root, []string{"libs/core", "missing"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), `project "missing": missing stew.toml`) {
		t.Errorf("missing manifest: err = %v", err)
	}
}
