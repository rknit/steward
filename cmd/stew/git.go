package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rknit/steward/internal/githook"
)

func newGitCmd(proc process) *cobra.Command {
	git := &cobra.Command{
		Use:   "git",
		Short: "Git integration",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	git.AddCommand(&cobra.Command{
		Use:   "install <hook>",
		Short: "Install a git hook that runs stew (supported: " + strings.Join(githook.Supported, ", ") + ")",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := proc.findRoot()
			if err != nil {
				return invalid(err)
			}
			path, err := githook.Install(root, args[0], proc.env)
			if err != nil {
				if errors.Is(err, githook.ErrUnsupported) {
					return invalid(err)
				}
				return rejected(err)
			}
			fmt.Fprintf(proc.stdout, "installed %s\n", path)
			return nil
		},
	})
	return git
}
