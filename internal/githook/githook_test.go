package githook

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitEnv is an environment for git with no system or global config and none of the variables, such as GIT_DIR, that
// git exports to hooks, so git acts only on the test's temp repositories.
func gitEnv(t *testing.T) []string {
	return []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull}
}

func gitInit(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	cmd := exec.Command("git", append([]string{"init", "-q"}, args...)...)
	cmd.Dir = dir
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

func TestScript(t *testing.T) {
	t.Parallel()
	tests := []struct{ rel, hook, want string }{
		{".", "pre-commit", `cd "$(git rev-parse --show-toplevel)" && exec stew ci --level pre-commit`},
		{"mono/repo", "pre-commit", `cd "$(git rev-parse --show-toplevel)/mono/repo" && exec stew ci --level pre-commit`},
		{`we"ird $dir`, "pre-commit", `cd "$(git rev-parse --show-toplevel)/we\"ird \$dir" && exec stew ci --level pre-commit`},
		{".", "pre-push", `cd "$(git rev-parse --show-toplevel)" && exec stew ci --level pre-push`},
		{".", "post-checkout", postCheckout(`"$(git rev-parse --show-toplevel)"`)},
		{`we"ird $dir`, "post-checkout", postCheckout(`"$(git rev-parse --show-toplevel)/we\"ird \$dir"`)},
	}
	for _, tt := range tests {
		want := "#!/bin/sh\n# installed by stew\n" + tt.want + "\n"
		if got := Script(tt.rel, tt.hook); got != want {
			t.Errorf("Script(%q, %q) = %q, want %q", tt.rel, tt.hook, got, want)
		}
	}
}

// postCheckout is the post-checkout hook body for a stew root at dir, a double-quoted shell word.
func postCheckout(dir string) string {
	return `case "$1" in *[!0]*) exit 0 ;; esac` + "\n" +
		"dir=" + dir + "\n" +
		`[ -d "$dir/.stew" ] || { echo "stew: no workspace in $dir, skipping worktree setup" >&2; exit 0; }` + "\n" +
		`cd "$dir" && stew trust --yes && exec stew setup-worktree`
}

func TestInstallInSubdirectory(t *testing.T) {
	t.Parallel()
	top := t.TempDir()
	env := gitEnv(t)
	gitInit(t, top, env)
	root := filepath.Join(top, "mono")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}

	path, err := Install(root, "pre-commit", env)
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
	if string(data) != Script("mono", "pre-commit") {
		t.Errorf("content = %q", data)
	}

	if _, err := Install(root, "pre-commit", env); !errors.Is(err, ErrExists) {
		t.Errorf("second install: err = %v, want ErrExists", err)
	}
}

func TestInstallRespectsHooksPath(t *testing.T) {
	t.Parallel()
	top := t.TempDir()
	env := gitEnv(t)
	gitInit(t, top, env)
	// Set in env, so git sees it only if Install passes env on.
	env = append(env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0=custom-hooks")
	path, err := Install(top, "pre-commit", env)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(top, "custom-hooks", "pre-commit"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

func TestInstallErrors(t *testing.T) {
	t.Parallel()
	if _, err := Install(t.TempDir(), "post-merge", nil); err == nil || !strings.Contains(err.Error(), "supported: pre-commit, pre-push, post-checkout") {
		t.Errorf("unsupported hook: err = %v", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	env := append(gitEnv(t), "GIT_CEILING_DIRECTORIES="+filepath.Dir(dir))
	if _, err := Install(dir, "pre-commit", env); err == nil || !strings.Contains(err.Error(), "git rev-parse") {
		t.Errorf("not a repo: err = %v", err)
	}
}
