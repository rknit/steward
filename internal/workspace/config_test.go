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
	for _, tc := range []struct{ name, content, want string }{
		{"missing key", "", `config.toml: missing key "workspace_wrapper"`},
		{"unknown key", "workspace_wrapper = \"\"\nworkspace_trust = \"\"\nshell = \"bash\"", `config.toml: unknown key "shell"`},
		{"wrong type", `workspace_wrapper = ["tool"]`, "config.toml: "},
		{"bad toml", `workspace_wrapper = "`, "config.toml: "},
		{"syntax", `workspace_wrapper = 'tool "x {{STEW_STEP}}'` + "\nworkspace_trust = \"\"", "config.toml: workspace_wrapper: sh: "},
		{"no placeholder", `workspace_wrapper = "tool exec ."` + "\nworkspace_trust = \"\"", "config.toml: workspace_wrapper: must contain {{STEW_STEP}} exactly once (found 0)"},
		{"missing trust", `workspace_wrapper = ""`, `config.toml: missing key "workspace_trust"`},
		{"trust type", "workspace_wrapper = \"\"\nworkspace_trust = 1", "config.toml: "},
	} {
		_, err := LoadConfig(writeConfig(t, tc.content))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	root := t.TempDir()
	_, err := LoadConfig(root)
	if err == nil || err.Error() != ConfigPath(root)+": missing" {
		t.Errorf("err = %v", err)
	}
}
