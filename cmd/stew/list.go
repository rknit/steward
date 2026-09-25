package main

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rknit/steward/internal/report"
	"github.com/rknit/steward/internal/workspace"
)

func newListCmd(stdout io.Writer) *cobra.Command {
	var porcelain bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List registered projects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, ws, err := loadWorkspace()
			if err != nil {
				return err
			}
			projects := slices.SortedFunc(slices.Values(ws.Projects), func(a, b *workspace.Project) int {
				return strings.Compare(a.Name, b.Name)
			})
			if porcelain {
				for _, p := range projects {
					fmt.Fprintf(stdout, "%s\t%s\t%s\n", p.Name, p.Path, strings.Join(slices.Sorted(slices.Values(p.Dependencies)), ","))
				}
				return nil
			}
			if len(projects) == 0 {
				fmt.Fprintln(stdout, "no projects (add one with: stew add <path>)")
				return nil
			}
			table := [][]string{{"project", "path", "dependencies"}}
			for _, p := range projects {
				deps := "-"
				if len(p.Dependencies) > 0 {
					deps = strings.Join(slices.Sorted(slices.Values(p.Dependencies)), ", ")
				}
				table = append(table, []string{p.Name, p.Path, deps})
			}
			report.Table(stdout, table)
			return nil
		},
	}
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "print tab-separated name, path, and dependencies for scripts")
	return cmd
}
