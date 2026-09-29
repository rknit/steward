package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rknit/steward/internal/report"
	"github.com/rknit/steward/internal/runner"
	"github.com/rknit/steward/internal/trust"
	"github.com/rknit/steward/internal/workspace"
)

func newTrustCmd(proc process) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "trust",
		Short: "Run every trust command again, after consent, and record them for this tree",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := loadWorkspace(proc)
			if err != nil {
				return err
			}
			entries := trust.Entries(ws)
			if len(entries) == 0 {
				if !yes {
					fmt.Fprintln(proc.stdout, "nothing to trust")
				}
				return nil
			}
			return withStops(func(ctx context.Context, force <-chan struct{}) error {
				return grantTrust(ctx, force, proc, ws.Root, entries, projectPaths(ws), true, yes, "stew trust --yes")
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "run the trust commands without asking")
	return cmd
}

// ensureTrust runs the workspace's pending trust entries, after consent, before stew runs a wrapped command.
func ensureTrust(ctx context.Context, force <-chan struct{}, proc process, ws *workspace.Workspace) error {
	return grantTrust(ctx, force, proc, ws.Root, trust.Entries(ws), projectPaths(ws), false, false, "stew trust")
}

// grantTrust runs entries after consent and records each success in the tree's trust.json, keeping only the projects
// at registered paths. With all, it runs every entry and the record starts empty; otherwise it runs the pending ones.
// yes gives consent. Otherwise stew asks when stdin and stdout are terminals, and fails suggesting hint when not.
// A stop signal that cancels ctx stops the running entry (see runner.Shell) and exits 128+n.
func grantTrust(ctx context.Context, force <-chan struct{}, proc process, root string, entries []trust.Entry,
	registered []string, all, yes bool, hint string) error {
	rec := trust.Load(root)
	run := entries
	if all {
		rec = trust.Record{Root: root}
	} else {
		run = rec.Pending(entries)
	}
	if len(run) == 0 {
		return nil
	}
	if !yes {
		if !proc.interactive {
			names := make([]string, len(run))
			for i, e := range run {
				names[i] = e.Name
			}
			return rejected(fmt.Errorf("untrusted: %s (run: %s)", strings.Join(names, ", "), hint))
		}
		ok, err := askTrust(ctx, proc, run)
		if err := interrupted(ctx); err != nil {
			return err
		}
		if err != nil {
			return rejected(err)
		}
		if !ok {
			return rejected(errors.New("trust declined"))
		}
	}
	if all {
		if err := trust.Save(root, rec, registered); err != nil {
			return rejected(fmt.Errorf("trust: save %s/%s: %w", workspace.DirName, trust.File, err))
		}
	}
	if err := runner.AdoptOrphans(); err != nil {
		return rejected(fmt.Errorf("cannot adopt orphaned processes: %w", err))
	}
	sh := runner.Shell{KillDelay: killDelay, Force: force, Environ: jobEnv(proc, nil)}
	err := trust.Run(ctx, root, &rec, run, registered, sh, proc.stdout, proc.stderr)
	if err := interrupted(ctx); err != nil {
		return err
	}
	if err != nil {
		return rejected(err)
	}
	return nil
}

// askTrust prints entries and reads one line from stdin. Only y or yes, in any case, is consent. It stops waiting
// when ctx is done; the read goes on in the background, as stew is about to exit.
func askTrust(ctx context.Context, proc process, entries []trust.Entry) (bool, error) {
	writeTrustTable(proc.stdout, entries)
	fmt.Fprint(proc.stdout, "run these trust commands? [y/N] ")
	type reply struct {
		line string
		err  error
	}
	replies := make(chan reply, 1)
	go func() {
		line, err := bufio.NewReader(proc.stdin).ReadString('\n')
		replies <- reply{line, err}
	}()
	var r reply
	select {
	case r = <-replies:
	case <-ctx.Done():
		return false, context.Cause(ctx)
	}
	if errors.Is(r.err, io.EOF) {
		fmt.Fprintln(proc.stdout)
	} else if r.err != nil {
		return false, r.err
	}
	answer := strings.ToLower(strings.TrimSpace(r.line))
	return answer == "y" || answer == "yes", nil
}

// writeTrustTable prints entries and their commands, each on one row.
func writeTrustTable(w io.Writer, entries []trust.Entry) {
	rows := [][]string{{"trust", "command"}}
	for _, e := range entries {
		rows = append(rows, []string{e.Name, report.OneLine(e.Command)})
	}
	report.Table(w, rows)
}

// projectPaths returns the registered path of every project.
func projectPaths(ws *workspace.Workspace) []string {
	paths := make([]string, len(ws.Projects))
	for i, p := range ws.Projects {
		paths[i] = p.Path
	}
	return paths
}
