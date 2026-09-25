package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rknit/steward/internal/workspace"
)

func newAddCmd(stdout io.Writer) *cobra.Command {
	var alias string
	cmd := &cobra.Command{
		Use:   "add <path>",
		Short: "Create <path>/stew.toml and register <path> as a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return add(stdout, args[0], alias)
		},
	}
	cmd.Flags().StringVarP(&alias, "alias", "a", "", "project name (default: the directory's name)")
	return cmd
}

func add(stdout io.Writer, arg, alias string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return rejected(err)
	}
	root, err := workspace.FindRoot(cwd)
	if err != nil {
		return invalid(err)
	}
	ws, err := workspace.Load(root)
	if err != nil {
		return invalid(err)
	}

	dir := arg
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(cwd, dir)
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

	name := alias
	if name == "" {
		name = filepath.Base(realDir)
	}

	manifest := filepath.Join(realDir, workspace.ManifestFile)
	if _, err := os.Lstat(manifest); err == nil {
		return rejected(fmt.Errorf("%s already exists", manifest))
	}
	paths := make([]string, 0, len(ws.Projects)+1)
	for _, p := range ws.Projects {
		if p.Path == rel {
			return rejected(fmt.Errorf("%s is already registered as %q", rel, p.Name))
		}
		paths = append(paths, p.Path)
	}
	if !workspace.ValidName(name) {
		return rejected(fmt.Errorf("invalid project name %q (want [a-z0-9][a-z0-9._-]*); pick one with -a", name))
	}
	if p, ok := ws.Project(name); ok {
		return rejected(fmt.Errorf("project name %q is already used by %s", name, p.Path))
	}

	f, err := os.OpenFile(manifest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return rejected(fmt.Errorf("%s already exists", manifest))
	}
	if err != nil {
		return rejected(err)
	}
	_, werr := f.Write(workspace.Template(name))
	if err := errors.Join(werr, f.Close()); err != nil {
		os.Remove(manifest)
		return rejected(err)
	}
	if err := workspace.SaveRegistry(root, append(paths, rel)); err != nil {
		os.Remove(manifest)
		return rejected(fmt.Errorf("register %s: %w", rel, err))
	}
	fmt.Fprintf(stdout, "added %s (%s)\n", name, rel)
	return nil
}
