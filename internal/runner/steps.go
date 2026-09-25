package runner

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// StepPlaceholder marks where a wrapper runs the step's command. stew replaces it with the path of a step script.
const StepPlaceholder = "{{STEW_STEP}}"

// StepDir holds the scripts of wrapped steps in a directory whose path needs no shell quoting,
// so a wrapper may put StepPlaceholder anywhere, even in a string a tool parses again.
type StepDir struct {
	Dir string

	mu     sync.Mutex
	levels map[string]int // wrapper count of each prepared key
}

var plainPath = regexp.MustCompile(`^[A-Za-z0-9/._-]+$`)

// NewStepDir creates a stew-* directory in $TMPDIR, or /tmp when it is unset.
func NewStepDir() (*StepDir, error) {
	base, err := filepath.Abs(os.TempDir())
	if err != nil {
		return nil, err
	}
	if !plainPath.MatchString(base) {
		return nil, fmt.Errorf("%s needs shell quoting; set TMPDIR to a path of letters, digits, and /._-", base)
	}
	dir, err := os.MkdirTemp(base, "stew-")
	if err != nil {
		return nil, err
	}
	return &StepDir{Dir: dir, levels: map[string]int{}}, nil
}

// Remove deletes the directory and everything in it.
func (d *StepDir) Remove() error { return os.RemoveAll(d.Dir) }

// Prepare implements Steps. The step script counts one reach, sets env again, runs cmd as sh -c cmd,
// records cmd's exit status, and exits with it. It exits 125 without running cmd when it cannot count the reach.
// Each inner wrapper level gets its own script; the outermost wrapper runs as sh -c.
func (d *StepDir) Prepare(key string, wrappers, env []string, cmd string) (argv []string, err error) {
	if err := d.clear(key, "reach", "status"); err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.levels[key] = len(wrappers)
	d.mu.Unlock()
	defer func() {
		if err != nil {
			err = errors.Join(err, d.forget(key))
		}
	}()
	var b strings.Builder
	b.WriteString("#!/bin/sh\necho >> " + shellQuote(d.path(key, "reach")) + " || exit 125\n")
	for _, kv := range env {
		b.WriteString("export " + shellQuote(kv) + "\n")
	}
	b.WriteString("sh -c " + shellQuote(cmd) + "\ncode=$?\n")
	b.WriteString(`echo "$code" > ` + shellQuote(d.path(key, "status")) + " || exit 125\n")
	b.WriteString(`exit "$code"` + "\n")
	next := d.path(key, "step")
	if err := os.WriteFile(next, []byte(b.String()), 0o700); err != nil {
		return nil, err
	}
	for i := len(wrappers) - 1; i >= 1; i-- {
		level := d.path(key, strconv.Itoa(i))
		script := "#!/bin/sh\n" + strings.Replace(wrappers[i], StepPlaceholder, next, 1) + "\n"
		if err := os.WriteFile(level, []byte(script), 0o700); err != nil {
			return nil, err
		}
		next = level
	}
	return []string{"sh", "-c", strings.Replace(wrappers[0], StepPlaceholder, next, 1)}, nil
}

// Collect implements Steps. It removes key's files even when reading them fails.
func (d *StepDir) Collect(key string) (reaches, status int, finished bool, err error) {
	reach, err := d.read(key, "reach")
	reaches = bytes.Count(reach, []byte("\n"))
	if err == nil {
		var code []byte
		if code, err = d.read(key, "status"); err == nil && len(code) > 0 {
			status, err = strconv.Atoi(strings.TrimSpace(string(code)))
			finished = err == nil
		}
	}
	return reaches, status, finished, errors.Join(err, d.forget(key))
}

// read returns the content of key's file ext, or nothing when it does not exist.
func (d *StepDir) read(key, ext string) ([]byte, error) {
	data, err := os.ReadFile(d.path(key, ext))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

// forget removes key's reach and status files, step script, and level scripts, and drops its wrapper count.
func (d *StepDir) forget(key string) error {
	d.mu.Lock()
	levels := d.levels[key]
	delete(d.levels, key)
	d.mu.Unlock()
	names := []string{"reach", "status", "step"}
	for i := 1; i < levels; i++ {
		names = append(names, strconv.Itoa(i))
	}
	return d.clear(key, names...)
}

func (d *StepDir) path(key, ext string) string { return filepath.Join(d.Dir, key+"."+ext) }

// clear removes key's files with the given extensions by name. A glob on key+".*" could match another step's
// files: project names may contain dots, e.g. key "p-ci.full.q-setup" matches "p-ci.full.*".
func (d *StepDir) clear(key string, exts ...string) error {
	for _, ext := range exts {
		if err := os.Remove(d.path(key, ext)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// shellQuote quotes s as one sh word.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
