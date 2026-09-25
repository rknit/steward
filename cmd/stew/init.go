package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/rknit/steward/internal/workspace"
)

func newInitCmd(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create .stew/ in the current directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return rejected(err)
			}
			dir := filepath.Join(cwd, workspace.DirName)
			if err := os.Mkdir(dir, 0o755); err != nil {
				if errors.Is(err, fs.ErrExist) {
					return rejected(fmt.Errorf("%s already exists", dir))
				}
				return rejected(err)
			}
			if err := workspace.SaveRegistry(cwd, nil); err != nil {
				return rejected(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("runs/\n"), 0o644); err != nil {
				return rejected(err)
			}
			fmt.Fprintf(stdout, "initialized %s\n", dir)
			return nil
		},
	}
}
