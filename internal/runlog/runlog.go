// Package runlog stores every command's output for one stew run under .stew/runs/<run-id>/,
// and saves, loads, and validates its run.json manifest.
package runlog

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// idTimeLayout is the UTC time at the start of a run ID.
const idTimeLayout = "20060102T150405Z"

// maxAttempts bounds retries when a generated run directory already exists.
const maxAttempts = 16

// Run is one run's log directory and, once started or loaded, its manifest.
type Run struct {
	ID       string
	Dir      string
	Manifest Manifest
}

// Create makes <stewDir>/runs/<run-id>/. The ID is the UTC time plus 4 random hex digits read from rand.
func Create(stewDir string, now time.Time, rand io.Reader) (*Run, error) {
	runs := filepath.Join(stewDir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		return nil, err
	}
	stamp := now.UTC().Format(idTimeLayout)
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

// PhaseLog is the stdout, stderr, and combined log files of one project phase.
type PhaseLog struct {
	stdout, stderr, combined *os.File
	mu                       sync.Mutex
}

// OpenPhase creates <project>-<phase>.stdout, .stderr, and .log. None of them may exist yet.
func (r *Run) OpenPhase(project, phase string) (*PhaseLog, error) {
	base := filepath.Join(r.Dir, project+"-"+phase)
	const flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL | os.O_APPEND
	var files []*os.File
	for _, ext := range []string{".stdout", ".stderr", ".log"} {
		f, err := os.OpenFile(base+ext, flags, 0o644)
		if err != nil {
			for _, open := range files {
				open.Close()
			}
			return nil, err
		}
		files = append(files, f)
	}
	return &PhaseLog{stdout: files[0], stderr: files[1], combined: files[2]}, nil
}

// Stdout returns the writer for the phase's stdout; it also writes to the combined log.
func (l *PhaseLog) Stdout() io.Writer { return &streamWriter{log: l, file: l.stdout} }

// Stderr returns the writer for the phase's stderr; it also writes to the combined log.
func (l *PhaseLog) Stderr() io.Writer { return &streamWriter{log: l, file: l.stderr} }

// streamWriter writes one stream to its own file and to the combined log, holding the lock so each write stays whole.
type streamWriter struct {
	log  *PhaseLog
	file *os.File
}

func (w *streamWriter) Write(p []byte) (int, error) {
	w.log.mu.Lock()
	defer w.log.mu.Unlock()
	if n, err := w.file.Write(p); err != nil {
		return n, err
	}
	return w.log.combined.Write(p)
}

// Marker writes "--- stew: <step>: <cmd>" to all three files.
func (l *PhaseLog) Marker(step, cmd string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	line := "--- stew: " + step + ": " + cmd + "\n"
	for _, f := range []*os.File{l.stdout, l.stderr, l.combined} {
		if _, err := io.WriteString(f, line); err != nil {
			return err
		}
	}
	return nil
}

// Close closes all three files and returns the errors joined.
func (l *PhaseLog) Close() error {
	return errors.Join(l.stdout.Close(), l.stderr.Close(), l.combined.Close())
}
