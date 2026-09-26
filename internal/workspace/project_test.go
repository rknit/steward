package workspace

import (
	"slices"
	"strings"
	"testing"
)

const validManifest = `name = "api"
project_wrapper = ""
project_trust = "mise trust"

[setup]
skip_if = "test -d node_modules"
run = "npm ci"

[build]
run = "npm run build"
verify = "test -f dist/index.js"
requires = ["api:setup", "core:build"]

[ci.full]
run = "npm test"
requires = ["api:build"]

[ci.pre-commit]
run = ""
`

func TestParseProjectValid(t *testing.T) {
	p, err := ParseProject("stew.toml", []byte(validManifest))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "api" || p.Wrapper != "" || p.Trust != "mise trust" {
		t.Errorf("name/wrapper/trust = %q %q %q", p.Name, p.Wrapper, p.Trust)
	}
	if got := p.SectionNames(); !slices.Equal(got, []string{"build", "ci.full", "ci.pre-commit", "setup"}) {
		t.Errorf("sections = %q", got)
	}
	setup := p.Sections["setup"]
	if setup.Run != "npm ci" || setup.SkipIf != "test -d node_modules" || setup.Verify != "" || len(setup.Requires) != 0 {
		t.Errorf("setup = %+v", setup)
	}
	build := p.Sections["build"]
	if build.Verify != "test -f dist/index.js" ||
		!slices.Equal(build.Requires, []Key{{"api", "setup"}, {"core", "build"}}) {
		t.Errorf("build = %+v", build)
	}
	if got := p.Dependencies(); !slices.Equal(got, []string{"core"}) {
		t.Errorf("dependencies = %q", got)
	}
}

func TestParseProjectNoSections(t *testing.T) {
	p, err := ParseProject("stew.toml", []byte("name = \"x\"\nproject_wrapper = \"\"\nproject_trust = \"\"\n[lint]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Sections) != 0 || len(p.Dependencies()) != 0 {
		t.Errorf("sections = %v", p.Sections)
	}
}

func TestParseProjectWrapper(t *testing.T) {
	data := strings.Replace(validManifest, `project_wrapper = ""`, `project_wrapper = "tool run {{STEW_STEP}}"`, 1)
	p, err := ParseProject("stew.toml", []byte(data))
	if err != nil || p.Wrapper != "tool run {{STEW_STEP}}" {
		t.Errorf("wrapper = %q, err = %v", p.Wrapper, err)
	}
}

func TestParseProjectWrapperErrors(t *testing.T) {
	with := func(line string) string {
		return strings.Replace(validManifest, `project_wrapper = ""`, line, 1)
	}
	tests := []struct{ data, want string }{
		{with(`project_wrapper = "tool run"`), "stew.toml: project_wrapper: must contain {{STEW_STEP}} exactly once (found 0)"},
		{with(`project_wrapper = "echo 'x {{STEW_STEP}}"`), "stew.toml: project_wrapper: sh: "},
	}
	for _, tt := range tests {
		_, err := ParseProject("stew.toml", []byte(tt.data))
		if err == nil || !strings.HasPrefix(err.Error(), tt.want) {
			t.Errorf("err = %v, want prefix %q", err, tt.want)
		}
	}
}

func TestParseProjectErrors(t *testing.T) {
	const head = "name = \"api\"\nproject_wrapper = \"\"\nproject_trust = \"\"\n"
	tests := []struct{ name, data, want string }{
		{"missing name", "project_wrapper = \"\"\nproject_trust = \"\"\n", `stew.toml: missing key "name"`},
		{"missing wrapper", "name = \"api\"\n", `stew.toml: missing key "project_wrapper"`},
		{"name type", "name = 1\nproject_wrapper = \"\"\nproject_trust = \"\"\n", "stew.toml: name: want a string"},
		{"bad name", "name = \"Api\"\nproject_wrapper = \"\"\nproject_trust = \"\"\n", `stew.toml: invalid name "Api" (want [a-z0-9][a-z0-9._-]*)`},
		{"missing trust", "name = \"api\"\nproject_wrapper = \"\"\n", `stew.toml: missing key "project_trust"`},
		{"trust type", "name = \"api\"\nproject_wrapper = \"\"\nproject_trust = 1\n", "stew.toml: project_trust: want a string"},
		{"dependencies", head + "dependencies = []\n", `stew.toml: unknown key "dependencies"`},
		{"missing run", head + "[build]\nverify = \"v\"\n", `stew.toml: [build]: missing key "run"`},
		{"unknown key", head + "[build]\nrun = \"\"\nfoo = \"x\"\n", `stew.toml: [build]: unknown key "foo"`},
		{"namespace key", head + "[ci]\nfoo = 1\n", `stew.toml: unknown key "ci.foo"`},
		{"nested bad segment", head + "[ci.Full]\nrun = \"\"\n", `stew.toml: invalid section name segment "Full" (want [a-z0-9][a-z0-9_-]*)`},
		{"run type", head + "[build]\nrun = 1\n", "stew.toml: [build]: run: want a string"},
		{"requires type", head + "[build]\nrun = \"\"\nrequires = \"x\"\n", "stew.toml: [build]: requires: want a list of strings"},
		{"requires item type", head + "[build]\nrun = \"\"\nrequires = [1]\n", "stew.toml: [build]: requires: want a list of strings"},
		{"section in section", head + "[ci]\nrun = \"\"\n[ci.full]\nrun = \"\"\n", "stew.toml: [ci]: a section cannot contain sections"},
		{"quoted dot", head + "[\"ci.full\"]\nrun = \"\"\n", `stew.toml: invalid section name segment "ci.full" (want [a-z0-9][a-z0-9_-]*)`},
		{"bad segment", head + "[Build]\nrun = \"\"\n", `stew.toml: invalid section name segment "Build" (want [a-z0-9][a-z0-9_-]*)`},
		{"no colon", head + "[build]\nrun = \"\"\nrequires = [\"core\"]\n", `stew.toml: [build]: requires "core": want <project>:<section>`},
		{"self", head + "[build]\nrun = \"\"\nrequires = [\"api:build\"]\n", `stew.toml: [build]: requires "api:build": section requires itself`},
		{"duplicate", head + "[build]\nrun = \"\"\nrequires = [\"core:build\", \"core:build\"]\n", `stew.toml: [build]: duplicate requires "core:build"`},
		{"toml syntax", "name = \n", "stew.toml: "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseProject("stew.toml", []byte(tt.data))
			if err == nil || !strings.HasPrefix(err.Error(), tt.want) {
				t.Errorf("err = %v, want prefix %q", err, tt.want)
			}
		})
	}
}

func TestTemplateParses(t *testing.T) {
	p, err := ParseProject("stew.toml", Template("my-app"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "my-app" || len(p.Sections) != 0 {
		t.Errorf("template project = %+v", p)
	}
}

func TestTemplateExact(t *testing.T) {
	want := `name = "x"
# Wraps every command of this project, inside the workspace wrapper.
# {{STEW_STEP}} marks where the command goes. "" means none.
project_wrapper = ""
# Makes the project wrapper usable in a new tree, e.g. "mise trust". Runs once per tree, with consent.
# "" means none.
project_trust = ""

# Sections: any [name] with a ` + "`run`" + ` key. ` + "`stew run '<regex>'`" + ` runs sections whose
# <project>:<section> key matches; ` + "`stew build`" + ` is ` + "`stew run '.*:build'`" + `.
# skip_if exit 0 skips the section. verify runs after run and must exit 0.
# requires lists sections that must succeed first, as "<project>:<section>".
#
# [build]
# run = ""
# skip_if = ""
# verify = ""
# requires = []
`
	if got := string(Template("x")); got != want {
		t.Errorf("template:\n%s\nwant:\n%s", got, want)
	}
}
