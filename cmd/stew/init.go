package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/rknit/steward/internal/workspace"
)

func newInitCmd(proc process) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create .stew/ in the current directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := filepath.Join(proc.dir, workspace.DirName)
			if err := os.Mkdir(dir, 0o755); err != nil {
				if errors.Is(err, fs.ErrExist) {
					return rejected(fmt.Errorf("%s already exists", dir))
				}
				return rejected(err)
			}
			if err := workspace.SaveRegistry(proc.dir, nil); err != nil {
				return rejected(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("runs/\n"), 0o644); err != nil {
				return rejected(err)
			}
			if err := os.WriteFile(workspace.ConfigPath(proc.dir), []byte(workspace.ConfigTemplate), 0o644); err != nil {
				return rejected(err)
			}
			fmt.Fprintf(proc.stdout, "initialized %s\n", dir)
			return nil
		},
	}
}
