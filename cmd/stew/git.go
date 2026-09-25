package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/rknit/steward/internal/githook"
	"github.com/rknit/steward/internal/workspace"
)

func newGitCmd(stdout io.Writer) *cobra.Command {
	git := &cobra.Command{
		Use:   "git",
		Short: "Git integration",
	}
	git.AddCommand(&cobra.Command{
		Use:   "install <hook>",
		Short: "Install a git hook that runs stew (supported: pre-commit)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return rejected(err)
			}
			root, err := workspace.FindRoot(cwd)
			if err != nil {
				return invalid(err)
			}
			path, err := githook.Install(root, args[0])
			if err != nil {
				if errors.Is(err, githook.ErrUnsupported) {
					return invalid(err)
				}
				return rejected(err)
			}
			fmt.Fprintf(stdout, "installed %s\n", path)
			return nil
		},
	})
	return git
}
