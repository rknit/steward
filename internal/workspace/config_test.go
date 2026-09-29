package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath(root), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLoadConfig(t *testing.T) {
	t.Parallel()
	const trust = "\nworkspace_trust = \"\"\n"
	for _, tc := range []struct {
		content string
		want    Config
	}{
		{ConfigTemplate, Config{}},
		{`workspace_wrapper = "tool exec . {{STEW_STEP}}"` + trust, Config{Wrapper: "tool exec . {{STEW_STEP}}"}},
		{"workspace_wrapper = '''\nset -x\ntool exec . {{STEW_STEP}}'''" + trust, Config{Wrapper: "set -x\ntool exec . {{STEW_STEP}}"}},
		{"workspace_wrapper = \"\"\nworkspace_trust = \"direnv allow .\"\n", Config{Trust: "direnv allow ."}},
	} {
		got, err := LoadConfig(writeConfig(t, tc.content))
		if err != nil || got != tc.want {
			t.Errorf("LoadConfig(%q) = %+v, %v; want %+v", tc.content, got, err, tc.want)
		}
	}
}

func TestLoadConfigErrors(t *testing.T) {
	t.Parallel()
	// A prefix case ends where the TOML library's or the shell's own message begins.
	for _, tc := range []struct {
		name, content, want string
		prefix              bool
	}{
		{"missing key", "", `missing key "workspace_wrapper"`, false},
		{"unknown key", "workspace_wrapper = \"\"\nworkspace_trust = \"\"\nshell = \"bash\"", `unknown key "shell"`, false},
		{"wrong type", `workspace_wrapper = ["tool"]`, "toml: ", true},
		{"bad toml", `workspace_wrapper = "`, "toml: ", true},
		{"syntax", `workspace_wrapper = 'tool "x {{STEW_STEP}}'` + "\nworkspace_trust = \"\"", "workspace_wrapper: sh: ", true},
		{"no placeholder", `workspace_wrapper = "tool exec ."` + "\nworkspace_trust = \"\"", "workspace_wrapper: must contain {{STEW_STEP}} exactly once (found 0)", false},
		{"missing trust", `workspace_wrapper = ""`, `missing key "workspace_trust"`, false},
		{"trust type", "workspace_wrapper = \"\"\nworkspace_trust = 1", "toml: ", true},
	} {
		root := writeConfig(t, tc.content)
		_, err := LoadConfig(root)
		want := ConfigPath(root) + ": " + tc.want
		if err == nil || (tc.prefix && !strings.HasPrefix(err.Error(), want)) || (!tc.prefix && err.Error() != want) {
			t.Errorf("%s: err = %v, want %q (prefix %v)", tc.name, err, want, tc.prefix)
		}
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	_, err := LoadConfig(root)
	if err == nil || err.Error() != ConfigPath(root)+": missing" {
		t.Errorf("err = %v", err)
	}
}
