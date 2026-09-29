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
// A created run holds its directory's lock until Close.
type Run struct {
	ID       string
	Dir      string
	Manifest Manifest
	lock     *os.File
}

// Create makes <stewDir>/runs/<run-id>/ and locks it. The ID is the UTC time plus 4 random hex digits read from rand.
func Create(stewDir string, now time.Time, rand io.Reader) (*Run, error) {
	return create(stewDir, now, rand, nil)
}

// create is Create with a beforeFlock hook for lockDir.
func create(stewDir string, now time.Time, rand io.Reader, beforeFlock func(string)) (*Run, error) {
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
		lock, err := lockDir(dir, beforeFlock)
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, ErrRunning) {
			// A concurrent prune took the new, still unlocked directory.
			continue
		}
		if err != nil {
			return nil, err
		}
		return &Run{ID: id, Dir: dir, lock: lock}, nil
	}
	return nil, fmt.Errorf("could not create a unique run directory in %s", runs)
}

// Close releases the run's lock. A loaded run holds no lock.
func (r *Run) Close() error {
	if r.lock == nil {
		return nil
	}
	return r.lock.Close()
}

// SectionLog is the stdout, stderr, and combined log files of one project section.
type SectionLog struct {
	stdout, stderr, combined *os.File
	mu                       sync.Mutex
}

// OpenSection creates <project>:<section>.stdout, .stderr, and .log. None of them may exist yet.
func (r *Run) OpenSection(project, section string) (*SectionLog, error) {
	base := filepath.Join(r.Dir, project+":"+section)
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
	return &SectionLog{stdout: files[0], stderr: files[1], combined: files[2]}, nil
}

// Stdout returns the writer for the section's stdout; it also writes to the combined log.
func (l *SectionLog) Stdout() io.Writer { return &streamWriter{log: l, file: l.stdout} }

// Stderr returns the writer for the section's stderr; it also writes to the combined log.
func (l *SectionLog) Stderr() io.Writer { return &streamWriter{log: l, file: l.stderr} }

// streamWriter writes one stream to its own file and to the combined log, holding the lock so each write stays whole.
type streamWriter struct {
	log  *SectionLog
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
func (l *SectionLog) Marker(step, cmd string) error {
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
func (l *SectionLog) Close() error {
	return errors.Join(l.stdout.Close(), l.stderr.Close(), l.combined.Close())
}
