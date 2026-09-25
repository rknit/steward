package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rknit/steward/internal/workspace"
)

func newRemoveCmd(stdout io.Writer) *cobra.Command {
	var clean bool
	cmd := &cobra.Command{
		Use:   "remove <name>...",
		Short: "Unregister projects",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return remove(stdout, cmd.ErrOrStderr(), args, clean)
		},
	}
	cmd.Flags().BoolVar(&clean, "clean", false, "also delete each project's stew.toml")
	return cmd
}

func remove(stdout, stderr io.Writer, names []string, clean bool) error {
	_, ws, err := loadWorkspace()
	if err != nil {
		return err
	}

	removing := make(map[string]*workspace.Project, len(names))
	for _, name := range names {
		p, ok := ws.Project(name)
		if !ok {
			return invalid(fmt.Errorf("unknown project %q", name))
		}
		removing[name] = p
	}

	neededBy := make(map[string][]string)
	var kept []string
	for _, p := range ws.Projects {
		if removing[p.Name] != nil {
			continue
		}
		kept = append(kept, p.Path)
		for _, dep := range p.Dependencies {
			if removing[dep] != nil {
				neededBy[dep] = append(neededBy[dep], p.Name)
			}
		}
	}
	if len(neededBy) > 0 {
		for _, name := range slices.Sorted(maps.Keys(neededBy)) {
			slices.Sort(neededBy[name])
			fmt.Fprintf(stderr, "stew: cannot remove %s: needed by %s\n", name, strings.Join(neededBy[name], ", "))
		}
		return &exitError{code: 1}
	}

	removed := slices.SortedFunc(maps.Values(removing), func(a, b *workspace.Project) int {
		return strings.Compare(a.Name, b.Name)
	})
	paths := make([]string, len(removed))
	for i, p := range removed {
		paths[i] = p.Path
	}
	if err := workspace.SaveRegistry(ws.Root, kept); err != nil {
		return rejected(fmt.Errorf("unregister %s: %w", strings.Join(paths, ", "), err))
	}
	for _, p := range removed {
		fmt.Fprintf(stdout, "removed %s (%s)\n", p.Name, p.Path)
	}
	if !clean {
		return nil
	}

	failed := false
	for _, p := range removed {
		manifest := path.Join(p.Path, workspace.ManifestFile)
		if err := os.Remove(filepath.Join(ws.Root, filepath.FromSlash(manifest))); err != nil {
			var pe *fs.PathError
			if errors.As(err, &pe) {
				err = pe.Err
			}
			fmt.Fprintf(stderr, "stew: delete %s: %v\n", manifest, err)
			failed = true
			continue
		}
		fmt.Fprintf(stdout, "deleted %s\n", manifest)
	}
	if failed {
		return &exitError{code: 1}
	}
	return nil
}
