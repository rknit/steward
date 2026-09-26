package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rknit/steward/internal/trust"
	"github.com/rknit/steward/internal/workspace"
)

func newAddCmd(proc process) *cobra.Command {
	var alias string
	var trusted bool
	cmd := &cobra.Command{
		Use:   "add <path>",
		Short: "Register <path> as a project, creating <path>/stew.toml if missing",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return add(proc, args[0], alias, trusted)
		},
	}
	cmd.Flags().StringVarP(&alias, "alias", "a", "", "project name for a new stew.toml (default: the directory's name)")
	cmd.Flags().BoolVar(&trusted, "trusted", false, "run the project's trust command without asking")
	return cmd
}

func add(proc process, arg, alias string, trusted bool) error {
	ws, err := loadWorkspace(proc.dir)
	if err != nil {
		return err
	}
	root := ws.Root

	dir := arg
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(proc.dir, dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return rejected(err)
	}
	if !info.IsDir() {
		return rejected(fmt.Errorf("%s is not a directory", arg))
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return rejected(err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return rejected(err)
	}
	rel, err := filepath.Rel(realRoot, realDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rejected(fmt.Errorf("%s is outside the workspace root %s", arg, root))
	}
	rel = filepath.ToSlash(rel)

	paths := make([]string, 0, len(ws.Projects)+1)
	for _, p := range ws.Projects {
		if p.Path == rel {
			return rejected(fmt.Errorf("%s is already registered as %q", rel, p.Name))
		}
		paths = append(paths, p.Path)
	}

	manifest := filepath.Join(realDir, workspace.ManifestFile)
	manifestRel := path.Join(rel, workspace.ManifestFile)
	_, err = os.Lstat(manifest)
	existing := err == nil

	var name string
	var loaded *workspace.Project
	if existing {
		p, err := workspace.LoadProject(realRoot, rel)
		if err != nil {
			return rejected(err)
		}
		if alias != "" && alias != p.Name {
			return rejected(fmt.Errorf("-a %s does not match name %q in %s", alias, p.Name, manifestRel))
		}
		loaded = p
		name = p.Name
	} else {
		name = alias
		if name == "" {
			name = filepath.Base(realDir)
		}
		if !workspace.ValidName(name) {
			return rejected(fmt.Errorf("invalid project name %q (want [a-z0-9][a-z0-9._-]*); pick one with -a", name))
		}
	}
	if p, ok := ws.Project(name); ok {
		return rejected(fmt.Errorf("project name %q is already used by %s", name, p.Path))
	}
	if loaded != nil {
		if err := ws.CheckNewProject(loaded); err != nil {
			return rejected(err)
		}
	}

	if loaded != nil && loaded.Trust != "" {
		entries := []trust.Entry{trust.ProjectEntry(loaded)}
		registered := append(slices.Clone(paths), rel)
		err := withStops(func(ctx context.Context, force <-chan struct{}) error {
			return grantTrust(ctx, force, proc, root, entries, registered, false, trusted, "stew add --trusted")
		})
		if err != nil {
			return err
		}
	}

	if !existing {
		if err := writeTemplate(manifest, name); err != nil {
			return rejected(err)
		}
	}
	if err := workspace.SaveRegistry(root, append(paths, rel)); err != nil {
		if !existing {
			os.Remove(manifest)
		}
		return rejected(fmt.Errorf("register %s: %w", rel, err))
	}
	if existing {
		fmt.Fprintf(proc.stdout, "using existing %s\n", manifestRel)
	}
	fmt.Fprintf(proc.stdout, "added %s (%s)\n", name, rel)
	return nil
}

func writeTemplate(manifest, name string) error {
	f, err := os.OpenFile(manifest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s already exists", manifest)
	}
	if err != nil {
		return err
	}
	_, werr := f.Write(workspace.Template(name))
	if err := errors.Join(werr, f.Close()); err != nil {
		os.Remove(manifest)
		return err
	}
	return nil
}
