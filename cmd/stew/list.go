package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rknit/steward/internal/report"
)

func newListCmd(proc process) *cobra.Command {
	var porcelain bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List registered projects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := loadWorkspace(proc.dir)
			if err != nil {
				return err
			}
			if porcelain {
				for _, p := range ws.Projects {
					fmt.Fprintf(proc.stdout, "%s\t%s\t%s\n", p.Name, p.Path, strings.Join(p.Dependencies(), ","))
				}
				return nil
			}
			if len(ws.Projects) == 0 {
				fmt.Fprintln(proc.stdout, "no projects (add one with: stew add <path>)")
				return nil
			}
			table := [][]string{{"project", "path", "dependencies"}}
			for _, p := range ws.Projects {
				deps := "-"
				if d := p.Dependencies(); len(d) > 0 {
					deps = strings.Join(d, ", ")
				}
				table = append(table, []string{p.Name, p.Path, deps})
			}
			report.Table(proc.stdout, table)
			return nil
		},
	}
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "print tab-separated name, path, and dependencies for scripts")
	return cmd
}
