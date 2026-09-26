// Package githook installs git hooks that run stew.
package githook

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Supported lists the hooks stew can install.
var Supported = []string{"pre-commit", "pre-push", "post-checkout"}

// ErrExists is returned when the hook file already exists.
var ErrExists = errors.New("hook already exists")

// ErrUnsupported is returned for a hook name not in Supported.
var ErrUnsupported = errors.New("unsupported hook")

// Install writes the named hook for the workspace at root and returns the hook's path. git runs with env as its
// environment.
func Install(root, hook string, env []string) (string, error) {
	if !slices.Contains(Supported, hook) {
		return "", fmt.Errorf("%w %q (supported: %s)", ErrUnsupported, hook, strings.Join(Supported, ", "))
	}
	hooksDir, err := git(root, env, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(hooksDir) {
		hooksDir = filepath.Join(root, hooksDir)
	}
	top, err := git(root, env, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	realTop, err := filepath.EvalSymlinks(top)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(realTop, realRoot)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(hooksDir, hook)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("%w: %s", ErrExists, path)
	}
	if err != nil {
		return "", err
	}
	_, werr := f.WriteString(Script(filepath.ToSlash(rel), hook))
	if err := errors.Join(werr, f.Chmod(0o755), f.Close()); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// Script returns the named hook for a stew root at rel (slash-separated) below the git top level.
// pre-commit and pre-push run the CI level of the same name. post-checkout trusts and sets up a new worktree: git
// passes an all-zero previous HEAD only when there was none.
func Script(rel, hook string) string {
	dir := `"$(git rev-parse --show-toplevel)"`
	if rel != "." {
		dir = `"$(git rev-parse --show-toplevel)/` + escapeDoubleQuoted(rel) + `"`
	}
	body := "cd " + dir + " && exec stew ci --level " + hook + "\n"
	if hook == "post-checkout" {
		body = `case "$1" in *[!0]*) exit 0 ;; esac` + "\n" +
			"dir=" + dir + "\n" +
			`[ -d "$dir/.stew" ] || { echo "stew: no workspace in $dir, skipping worktree setup" >&2; exit 0; }` + "\n" +
			`cd "$dir" && stew trust --yes && exec stew setup-worktree` + "\n"
	}
	return "#!/bin/sh\n# installed by stew\n" + body
}

// escapeDoubleQuoted escapes the characters that stay special inside a sh double-quoted string.
func escapeDoubleQuoted(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\', '"', '$', '`':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func git(dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}
