package workspace

import (
	"strings"
	"testing"
)

const validManifest = `name = "api"
dependencies = ["core"]

[setup]
run = "npm ci"
verify = "test -d node_modules"

[build]
run = "npm run build"
verify = ""

[ci.full]
run = "npm test"

[ci.pre-commit]
run = "npm run lint"
`

func TestParseProjectValid(t *testing.T) {
	p, err := ParseProject("stew.toml", []byte(validManifest))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "api" || len(p.Dependencies) != 1 || p.Dependencies[0] != "core" {
		t.Errorf("name/deps = %q %q", p.Name, p.Dependencies)
	}
	if p.Setup != (Phase{Run: "npm ci", Verify: "test -d node_modules"}) {
		t.Errorf("setup = %+v", p.Setup)
	}
	if p.Build != (Phase{Run: "npm run build", Verify: ""}) {
		t.Errorf("build = %+v", p.Build)
	}
	if len(p.CI) != 2 || p.CI[LevelFull] != "npm test" || p.CI[LevelPreCommit] != "npm run lint" {
		t.Errorf("ci = %v", p.CI)
	}
}

func TestTemplateParses(t *testing.T) {
	p, err := ParseProject("stew.toml", Template("my-lib.v2"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "my-lib.v2" || len(p.Dependencies) != 0 || p.Setup != (Phase{}) || p.Build != (Phase{}) {
		t.Errorf("project = %+v", p)
	}
	if len(p.CI) != 1 || p.CI[LevelFull] != "" {
		t.Errorf("ci = %v", p.CI)
	}
}

func TestTemplateExact(t *testing.T) {
	want := `name = "api"
dependencies = []

# Each phase: ` + "`verify` runs first; exit 0 skips `run`." + `
# Otherwise ` + "`run` runs, then `verify` confirms." + ` Empty strings are no-ops.
[setup]
run = ""
verify = ""

[build]
run = ""
verify = ""

# Levels: pre-commit falls back to quick, quick falls back to full.
[ci.full]
run = ""
`
	if got := string(Template("api")); got != want {
		t.Errorf("Template:\n%s\nwant:\n%s", got, want)
	}
}

func TestParseProjectErrors(t *testing.T) {
	// base builds a manifest with one line removed or appended.
	base := func(drop string, extra string) string {
		lines := []string{
			`name = "api"`,
			`dependencies = []`,
			`[setup]`, `run = ""`, `verify = ""`,
			`[build]`, `run = "b"`, `verify = "vb"`,
			`[ci.full]`, `run = ""`,
		}
		var out []string
		dropped := false
		for _, l := range lines {
			if !dropped && l == drop {
				dropped = true
				continue
			}
			out = append(out, l)
		}
		return strings.Join(out, "\n") + "\n" + extra
	}

	tests := []struct {
		name, content, wantErr string
	}{
		{"missing name", base(`name = "api"`, ""), `missing key "name"`},
		{"missing dependencies", base(`dependencies = []`, ""), `missing key "dependencies"`},
		{"missing setup", strings.Replace(base("", ""), "[setup]\nrun = \"\"\nverify = \"\"\n", "", 1),
			"missing section [setup]"},
		{"missing setup.run", base(`run = ""`, ""), `missing key "setup.run"`},
		{"missing setup.verify", base(`verify = ""`, ""), `missing key "setup.verify"`},
		{"missing build.run", base(`run = "b"`, ""), `missing key "build.run"`},
		{"missing build.verify", base(`verify = "vb"`, ""), `missing key "build.verify"`},
		{"missing ci.full", strings.Replace(base("", ""), "[ci.full]\nrun = \"\"\n", "", 1),
			"missing section [ci.full]"},
		{"ci.quick without run", base("", "[ci.quick]\n"), `missing key "ci.quick.run"`},
		{"unknown top key", "extra = 1\n" + base("", ""), `unknown key "extra"`},
		{"unknown key in ci level", base("", "extra = 1\n"), `unknown key "ci.full.extra"`},
		{"unknown section", base("", "[buidl]\nrun = \"\"\n"), `unknown key "buidl"`},
		{"verify in ci", base("", "verify = \"x\"\n"), `unknown key "ci.full.verify"`},
		{"unknown level", base("", "[ci.nightly]\nrun = \"\"\n"), `unknown CI level "nightly"`},
		{"invalid name", strings.Replace(base("", ""), `"api"`, `"My App"`, 1), `invalid name "My App"`},
		{"uppercase name", strings.Replace(base("", ""), `"api"`, `"Api"`, 1), `invalid name "Api"`},
		{"duplicate dependency", strings.Replace(base("", ""), `[]`, `["a", "a"]`, 1), `duplicate dependency "a"`},
		{"wrong type", strings.Replace(base("", ""), `name = "api"`, `name = 5`, 1), "stew.toml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseProject("stew.toml", []byte(tt.content))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q\ncontent:\n%s", err, tt.wantErr, tt.content)
			}
		})
	}
}

func TestResolveCI(t *testing.T) {
	tests := []struct {
		defined   []Level
		requested Level
		want      Level
	}{
		{[]Level{LevelFull}, LevelPreCommit, LevelFull},
		{[]Level{LevelFull}, LevelQuick, LevelFull},
		{[]Level{LevelFull}, LevelFull, LevelFull},
		{[]Level{LevelFull, LevelQuick}, LevelPreCommit, LevelQuick},
		{[]Level{LevelFull, LevelQuick}, LevelFull, LevelFull},
		{[]Level{LevelFull, LevelPreCommit}, LevelQuick, LevelFull},
		{[]Level{LevelFull, LevelPreCommit}, LevelPreCommit, LevelPreCommit},
		{[]Level{LevelFull, LevelQuick}, LevelQuick, LevelQuick},
		{[]Level{LevelFull, LevelPreCommit}, LevelFull, LevelFull},
		{[]Level{LevelFull, LevelQuick, LevelPreCommit}, LevelPreCommit, LevelPreCommit},
		{[]Level{LevelFull, LevelQuick, LevelPreCommit}, LevelQuick, LevelQuick},
		{[]Level{LevelFull, LevelQuick, LevelPreCommit}, LevelFull, LevelFull},
	}
	for _, tt := range tests {
		p := &Project{Name: "p", CI: map[Level]string{}}
		for _, l := range tt.defined {
			p.CI[l] = "run-" + string(l)
		}
		used, run := p.ResolveCI(tt.requested)
		if used != tt.want || run != "run-"+string(tt.want) {
			t.Errorf("defined %v, requested %s: got %s %q, want %s", tt.defined, tt.requested, used, run, tt.want)
		}
	}
}

func TestParseLevel(t *testing.T) {
	for _, s := range []string{"full", "quick", "pre-commit"} {
		if l, err := ParseLevel(s); err != nil || string(l) != s {
			t.Errorf("ParseLevel(%q) = %q, %v", s, l, err)
		}
	}
	if _, err := ParseLevel("nightly"); err == nil {
		t.Error("ParseLevel(nightly) succeeded")
	}
}
