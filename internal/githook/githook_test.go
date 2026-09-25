package githook

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitInit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	cmd := exec.Command("git", append([]string{"init", "-q"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

func TestScript(t *testing.T) {
	tests := []struct{ rel, want string }{
		{".", `cd "$(git rev-parse --show-toplevel)" && exec stew ci --level pre-commit`},
		{"mono/repo", `cd "$(git rev-parse --show-toplevel)/mono/repo" && exec stew ci --level pre-commit`},
		{`we"ird $dir`, `cd "$(git rev-parse --show-toplevel)/we\"ird \$dir" && exec stew ci --level pre-commit`},
	}
	for _, tt := range tests {
		want := "#!/bin/sh\n# installed by stew\n" + tt.want + "\n"
		if got := Script(tt.rel); got != want {
			t.Errorf("Script(%q) = %q, want %q", tt.rel, got, want)
		}
	}
}

func TestInstallInSubdirectory(t *testing.T) {
	top := t.TempDir()
	gitInit(t, top)
	root := filepath.Join(top, "mono")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}

	path, err := Install(root, "pre-commit")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(top, ".git", "hooks", "pre-commit"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if string(data) != Script("mono") {
		t.Errorf("content = %q", data)
	}

	if _, err := Install(root, "pre-commit"); !errors.Is(err, ErrExists) {
		t.Errorf("second install: err = %v, want ErrExists", err)
	}
}

func TestInstallRespectsHooksPath(t *testing.T) {
	top := t.TempDir()
	gitInit(t, top)
	cmd := exec.Command("git", "config", "core.hooksPath", "custom-hooks")
	cmd.Dir = top
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config: %v\n%s", err, out)
	}
	path, err := Install(top, "pre-commit")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(top, "custom-hooks", "pre-commit"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

func TestInstallErrors(t *testing.T) {
	if _, err := Install(t.TempDir(), "pre-push"); err == nil || !strings.Contains(err.Error(), "supported: pre-commit") {
		t.Errorf("unsupported hook: err = %v", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	if _, err := Install(t.TempDir(), "pre-commit"); err == nil || !strings.Contains(err.Error(), "git rev-parse") {
		t.Errorf("not a repo: err = %v", err)
	}
}
