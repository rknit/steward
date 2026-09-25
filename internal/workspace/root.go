// Package workspace loads and validates a stew workspace: the .stew/ registry and every project's stew.toml.
package workspace

import (
	"errors"
	"os"
	"path/filepath"
)

// DirName is the name of the directory that marks a workspace root.
const DirName = ".stew"

// ErrNotWorkspace is returned by FindRoot when no .stew/ directory exists in cwd or any parent.
var ErrNotWorkspace = errors.New("not a stew workspace")

// FindRoot walks up from cwd and returns the first directory that contains a .stew/ directory.
func FindRoot(cwd string) (string, error) {
	dir, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	for {
		info, err := os.Stat(filepath.Join(dir, DirName))
		if err == nil && info.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNotWorkspace
		}
		dir = parent
	}
}
