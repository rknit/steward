package runlog

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Retention says which runs to keep. A run is kept when any set rule keeps it; with no rule set, none is kept.
type Retention struct {
	// Since keeps runs whose ID start time is at or after it. A run ID with no valid time is never kept by it.
	Since *time.Time
	// LastN keeps the LastN newest runs by ID. It must not be negative.
	LastN *int
}

// Expired returns the IDs that r does not keep, in the order given. ids must be sorted oldest first, as IDs returns them.
func (r Retention) Expired(ids []string) []string {
	var expired []string
	for i, id := range ids {
		if r.LastN != nil && i >= len(ids)-*r.LastN {
			continue
		}
		if r.Since != nil {
			if start, err := StartTime(id); err == nil && !start.Before(*r.Since) {
				continue
			}
		}
		expired = append(expired, id)
	}
	return expired
}

// Delete removes run id while holding its lock. A run in progress is ErrRunning. An ID that is not a run
// directory, including a symlink, is ErrUnknownRun and is left untouched.
func Delete(stewDir, id string) error {
	if !idPattern.MatchString(id) {
		return ErrUnknownRun
	}
	dir := filepath.Join(stewDir, "runs", id)
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) || err == nil && !info.IsDir() {
		return ErrUnknownRun
	}
	if err != nil {
		return err
	}
	lock, err := lockDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrUnknownRun
	}
	if err != nil {
		return err
	}
	defer lock.Close()
	return os.RemoveAll(dir)
}
