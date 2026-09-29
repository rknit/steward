package main

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/rknit/steward/internal/workspace"
	"github.com/rknit/steward/skills"
)

func newSkillsCmd(proc process) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Manage the " + skills.Name + " agent skill, which teaches AI agents to use stew",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "install [<dir>]",
			Short: "Install or update the " + skills.Name + " skill in <dir> (default: .agents/skills in the workspace root, else cwd)",
			Args:  cobra.MaximumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				dir, err := skillsDir(proc, args)
				if err != nil {
					return err
				}
				path, err := skills.Install(dir)
				if err != nil {
					return rejected(err)
				}
				fmt.Fprintf(proc.stdout, "installed %s\n", path)
				return nil
			},
		},
		&cobra.Command{
			Use:   "uninstall [<dir>]",
			Short: "Remove the " + skills.Name + " skill from <dir> (default: .agents/skills in the workspace root, else cwd)",
			Args:  cobra.MaximumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				dir, err := skillsDir(proc, args)
				if err != nil {
					return err
				}
				path, err := skills.Uninstall(dir)
				if err != nil {
					return rejected(err)
				}
				fmt.Fprintf(proc.stdout, "removed %s\n", path)
				return nil
			},
		},
	)
	return cmd
}

// skillsDir is the directory named in args, resolved against the working directory, or .agents/skills in the
// workspace root when args is empty. Outside a workspace, the root is the working directory, where stew init would
// put it.
func skillsDir(proc process, args []string) (string, error) {
	if len(args) == 1 {
		if filepath.IsAbs(args[0]) {
			return args[0], nil
		}
		return filepath.Join(proc.dir, args[0]), nil
	}
	root, err := workspace.FindRoot(proc.dir)
	if errors.Is(err, workspace.ErrNotWorkspace) {
		root = proc.dir
	} else if err != nil {
		return "", invalid(err)
	}
	return filepath.Join(root, ".agents", "skills"), nil
}
