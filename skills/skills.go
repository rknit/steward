// Package skills holds the agent skill stew distributes and installs it.
package skills

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Name is the skill's directory name and its frontmatter name.
const Name = "use-steward"

//go:embed use-steward
var files embed.FS

// ErrNotInstalled is returned by Uninstall when the skill is not in the directory.
var ErrNotInstalled = errors.New("not installed")

// Install writes the skill to dir/use-steward, creating dir if needed and replacing an earlier copy, and returns
// the skill's path.
func Install(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(dir, "."+Name+"-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, Name)
	err = writeSkill(tmp)
	if err == nil {
		err = os.RemoveAll(path)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	return path, nil
}

// Uninstall removes dir/use-steward and returns its path.
func Uninstall(dir string) (string, error) {
	path := filepath.Join(dir, Name)
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%s: %w", path, ErrNotInstalled)
		}
		return "", err
	}
	if err := os.RemoveAll(path); err != nil {
		return "", err
	}
	return path, nil
}

func writeSkill(dst string) error {
	if err := os.Chmod(dst, 0o755); err != nil {
		return err
	}
	return fs.WalkDir(files, Name, func(name string, d fs.DirEntry, err error) error {
		if err != nil || name == Name {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(name[len(Name)+1:]))
		if d.IsDir() {
			return os.Mkdir(target, 0o755)
		}
		data, err := files.ReadFile(name)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
