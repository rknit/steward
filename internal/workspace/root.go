// Package workspace loads and validates a stew workspace: the .stew/ registry and every project's stew.toml.
package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
)

// DirName is the name of the directory that marks a workspace root.
const DirName = ".stew"

// CeilingEnv names the variable that lists directories FindRoot never walks up into, like GIT_CEILING_DIRECTORIES.
const CeilingEnv = "STEW_CEILING_DIRECTORIES"

// ErrNotWorkspace is returned by FindRoot when no .stew/ directory exists in cwd or any parent it may search.
var ErrNotWorkspace = errors.New("not a stew workspace")

// FindRoot walks up from cwd and returns the first directory that contains a .stew/ directory. cwd itself is always
// searched; the walk stops before it moves up into one of ceilings. A relative ceiling never matches.
func FindRoot(cwd string, ceilings []string) (string, error) {
	dir, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	stops := make([]string, len(ceilings))
	for i, c := range ceilings {
		stops[i] = filepath.Clean(c)
	}
	for {
		info, err := os.Stat(filepath.Join(dir, DirName))
		if err == nil && info.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir || slices.Contains(stops, parent) {
			return "", ErrNotWorkspace
		}
		dir = parent
	}
}
