// Package githook installs git hooks that run stew.
package githook

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Supported lists the hooks stew can install.
var Supported = []string{"pre-commit"}

// ErrExists is returned when the hook file already exists.
var ErrExists = errors.New("hook already exists")

// ErrUnsupported is returned for a hook name not in Supported.
var ErrUnsupported = errors.New("unsupported hook")

// Install writes the named hook for the workspace at root and returns the hook's path.
func Install(root, hook string) (string, error) {
	if hook != "pre-commit" {
		return "", fmt.Errorf("%w %q (supported: %s)", ErrUnsupported, hook, strings.Join(Supported, ", "))
	}
	hooksDir, err := git(root, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(hooksDir) {
		hooksDir = filepath.Join(root, hooksDir)
	}
	top, err := git(root, "rev-parse", "--show-toplevel")
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
	_, werr := f.WriteString(Script(filepath.ToSlash(rel)))
	if err := errors.Join(werr, f.Chmod(0o755), f.Close()); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// Script returns the pre-commit hook for a stew root at rel (slash-separated) below the git top level.
func Script(rel string) string {
	dir := `"$(git rev-parse --show-toplevel)"`
	if rel != "." {
		dir = `"$(git rev-parse --show-toplevel)/` + escapeDoubleQuoted(rel) + `"`
	}
	return "#!/bin/sh\n# installed by stew\ncd " + dir + " && exec stew ci --level pre-commit\n"
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

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
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
