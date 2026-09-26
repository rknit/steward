package main

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/rknit/steward/internal/runlog"
)

// newRunsPruneCmd returns the "runs prune" command.
func newRunsPruneCmd(proc process) *cobra.Command {
	var keepSince string
	var keepLastN int
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete runs that no keep rule keeps, except runs in progress",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var keep runlog.Retention
			if cmd.Flags().Changed("keep-since") {
				since, err := parseKeepSince(keepSince, time.Now(), proc.loc)
				if err != nil {
					return invalid(err)
				}
				keep.Since = &since
			}
			if cmd.Flags().Changed("keep-last-n") {
				if keepLastN < 0 {
					return invalid(fmt.Errorf("invalid --keep-last-n %d: must be 0 or more", keepLastN))
				}
				keep.LastN = &keepLastN
			}
			if keep.Since == nil && keep.LastN == nil {
				return invalid(errors.New("at least one of --keep-since or --keep-last-n is required"))
			}
			return pruneRuns(proc, keep)
		},
	}
	cmd.Flags().StringVar(&keepSince, "keep-since", "", "keep runs started at or after a time: a duration ago (36h, 7d, 2w) or a date (2026-09-01, \"2026-09-01 15:04:05\", RFC 3339)")
	cmd.Flags().IntVar(&keepLastN, "keep-last-n", 0, "keep the n newest runs")
	return cmd
}

var (
	keepSinceDuration = regexp.MustCompile(`^(?:\d+[wdhms])+$`)
	durationPart      = regexp.MustCompile(`(\d+)([wdhms])`)
	durationUnits     = map[string]time.Duration{
		"w": 7 * 24 * time.Hour, "d": 24 * time.Hour, "h": time.Hour, "m": time.Minute, "s": time.Second,
	}
)

// parseKeepSince returns the --keep-since cutoff: now minus a duration such as 36h, 7d, or 1w2d,
// or a date or date and time in loc, or an RFC 3339 time.
func parseKeepSince(s string, now time.Time, loc *time.Location) (time.Time, error) {
	if keepSinceDuration.MatchString(s) {
		var d time.Duration
		for _, m := range durationPart.FindAllStringSubmatch(s, -1) {
			n, err := strconv.ParseInt(m[1], 10, 64)
			unit := durationUnits[m[2]]
			if err != nil || n > (math.MaxInt64-int64(d))/int64(unit) {
				return time.Time{}, fmt.Errorf("invalid --keep-since %q: duration too long", s)
			}
			d += time.Duration(n) * unit
		}
		return now.Add(-d), nil
	}
	for _, layout := range []string{time.DateOnly, time.DateTime} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid --keep-since %q: want a duration such as 7d or a time such as 2026-09-01", s)
}

// pruneRuns deletes the runs keep does not keep, oldest first, printing one line per run.
// A failed delete is reported and the rest still run.
func pruneRuns(proc process, keep runlog.Retention) error {
	stewDir, err := findStewDir(proc.dir)
	if err != nil {
		return err
	}
	ids, err := runlog.IDs(stewDir)
	if err != nil {
		return rejected(fmt.Errorf("read runs: %w", err))
	}
	failed := false
	for _, id := range keep.Expired(ids) {
		switch err := runlog.Delete(stewDir, id); {
		case err == nil:
			fmt.Fprintf(proc.stdout, "pruned %s\n", id)
		case errors.Is(err, runlog.ErrRunning):
			fmt.Fprintf(proc.stdout, "kept %s: running\n", id)
		case errors.Is(err, runlog.ErrUnknownRun):
			// Another prune removed it first.
		default:
			fmt.Fprintf(proc.stderr, "stew: prune %s: %v\n", id, err)
			failed = true
		}
	}
	if failed {
		return &exitError{code: 1}
	}
	return nil
}
