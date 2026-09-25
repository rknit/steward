// Package runlog stores every command's output for one stew run under .stew/runs/<run-id>/.
package runlog

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// maxAttempts bounds retries when a generated run directory already exists.
const maxAttempts = 16

// Run is one run's log directory.
type Run struct {
	ID  string
	Dir string
}

// Create makes <stewDir>/runs/<run-id>/. The ID is the UTC time plus 4 random hex digits read from rand.
func Create(stewDir string, now time.Time, rand io.Reader) (*Run, error) {
	runs := filepath.Join(stewDir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		return nil, err
	}
	stamp := now.UTC().Format("20060102T150405Z")
	for range maxAttempts {
		var suffix [2]byte
		if _, err := io.ReadFull(rand, suffix[:]); err != nil {
			return nil, err
		}
		id := stamp + "-" + hex.EncodeToString(suffix[:])
		dir := filepath.Join(runs, id)
		err := os.Mkdir(dir, 0o755)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return &Run{ID: id, Dir: dir}, nil
	}
	return nil, fmt.Errorf("could not create a unique run directory in %s", runs)
}

// PhaseLog is the stdout/stderr file pair of one project phase.
type PhaseLog struct {
	stdout, stderr *os.File
}

// OpenPhase creates <project>-<phase>.stdout and .stderr. Both files must not exist yet.
func (r *Run) OpenPhase(project, phase string) (*PhaseLog, error) {
	base := filepath.Join(r.Dir, project+"-"+phase)
	const flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL | os.O_APPEND
	stdout, err := os.OpenFile(base+".stdout", flags, 0o644)
	if err != nil {
		return nil, err
	}
	stderr, err := os.OpenFile(base+".stderr", flags, 0o644)
	if err != nil {
		stdout.Close()
		return nil, err
	}
	return &PhaseLog{stdout: stdout, stderr: stderr}, nil
}

// Stdout returns the writer for the phase's stdout file.
func (l *PhaseLog) Stdout() io.Writer { return l.stdout }

// Stderr returns the writer for the phase's stderr file.
func (l *PhaseLog) Stderr() io.Writer { return l.stderr }

// Marker writes "--- stew: <step>: <cmd>" to both files.
func (l *PhaseLog) Marker(step, cmd string) error {
	line := "--- stew: " + step + ": " + cmd + "\n"
	if _, err := io.WriteString(l.stdout, line); err != nil {
		return err
	}
	_, err := io.WriteString(l.stderr, line)
	return err
}

// Close closes both files and returns the first error.
func (l *PhaseLog) Close() error {
	return errors.Join(l.stdout.Close(), l.stderr.Close())
}
