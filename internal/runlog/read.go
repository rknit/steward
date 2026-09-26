package runlog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/rknit/steward/internal/runner"
)

// ErrUnknownRun means no run directory has the requested ID.
var ErrUnknownRun = errors.New("unknown run")

// ErrNoManifest means the run directory has no run.json.
var ErrNoManifest = errors.New("no run.json")

var idPattern = regexp.MustCompile(`^\d{8}T\d{6}Z-[0-9a-f]{4}$`)

// projectPattern and sectionPattern are the name shapes stew ever writes to run.json and log file names.
var (
	projectPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	sectionPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*(\.[a-z0-9][a-z0-9_-]*)*$`)
)

var statuses = []runner.Status{runner.Done, runner.Skip, runner.Fail, runner.Blocked, runner.Interrupted}

// IDs returns the run IDs in <stewDir>/runs, oldest first. A missing runs directory has none.
func IDs(stewDir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(stewDir, "runs"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && idPattern.MatchString(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	slices.Sort(ids)
	return ids, nil
}

// Latest returns the run ID that sorts last in <stewDir>/runs.
func Latest(stewDir string) (string, error) {
	ids, err := IDs(stewDir)
	if err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", ErrUnknownRun
	}
	return ids[len(ids)-1], nil
}

// StartTime returns the UTC start time encoded in a run ID.
func StartTime(id string) (time.Time, error) {
	if !idPattern.MatchString(id) {
		return time.Time{}, fmt.Errorf("invalid run ID %q", id)
	}
	return time.Parse(idTimeLayout, id[:len(idTimeLayout)])
}

// Load reads and validates the run.json of run id.
func Load(stewDir, id string) (*Run, error) {
	if !idPattern.MatchString(id) {
		return nil, ErrUnknownRun
	}
	dir := filepath.Join(stewDir, "runs", id)
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) || err == nil && !info.IsDir() {
		return nil, ErrUnknownRun
	}
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoManifest
	}
	if err != nil {
		return nil, err
	}
	run := &Run{ID: id, Dir: dir}
	if err := json.Unmarshal(data, &run.Manifest); err != nil {
		return nil, err
	}
	if err := run.Manifest.validate(); err != nil {
		return nil, fmt.Errorf("invalid run.json: %w", err)
	}
	return run, nil
}

// validate rejects records that stew never writes, so readers can trust names, statuses, and durations.
func (m Manifest) validate() error {
	for i, p := range m.Projects {
		if !projectPattern.MatchString(p) {
			return fmt.Errorf("projects %d: invalid project %q", i, p)
		}
	}
	for p := range m.ProjectWrapper {
		if !slices.Contains(m.Projects, p) {
			return fmt.Errorf("project_wrapper: project %q is not in projects", p)
		}
	}
	if m.TotalMS != nil && *m.TotalMS < 0 {
		return fmt.Errorf("negative total_ms")
	}
	for i, c := range m.Columns {
		if !sectionPattern.MatchString(c) {
			return fmt.Errorf("columns %d: invalid column %q", i, c)
		}
	}
	for i, s := range m.Sections {
		switch {
		case !slices.Contains(statuses, runner.Status(s.Status)):
			return fmt.Errorf("section %d: unknown status %q", i, s.Status)
		case !projectPattern.MatchString(s.Project):
			return fmt.Errorf("section %d: invalid project %q", i, s.Project)
		case !slices.Contains(m.Projects, s.Project):
			return fmt.Errorf("section %d: project %q is not in projects", i, s.Project)
		case !slices.Contains(m.Columns, s.Section):
			return fmt.Errorf("section %d: section %q is not in columns", i, s.Section)
		case s.DurationMS != nil && *s.DurationMS < 0:
			return fmt.Errorf("section %d: negative duration", i)
		}
		for _, req := range s.BlockedBy {
			if _, ok := ParseKey(req); !ok {
				return fmt.Errorf("section %d: invalid blocked_by %q", i, req)
			}
		}
	}
	return nil
}

// Key names a section's log files: <Project>:<Section>.
type Key struct {
	Project string
	Section string
}

// String returns "<project>:<section>".
func (k Key) String() string { return k.Project + ":" + k.Section }

// ParseKey splits "<project>:<section>" at its only ":". Both parts must have the shape stew writes.
func ParseKey(s string) (Key, bool) {
	project, section, ok := strings.Cut(s, ":")
	if !ok || !projectPattern.MatchString(project) || !sectionPattern.MatchString(section) {
		return Key{}, false
	}
	return Key{Project: project, Section: section}, true
}

// Unfinished returns the sections that have a .log file but no record, sorted by key.
func (r *Run) Unfinished() ([]Key, error) {
	recorded := make(map[Key]bool, len(r.Manifest.Sections))
	for _, s := range r.Manifest.Sections {
		recorded[Key{Project: s.Project, Section: s.Section}] = true
	}
	entries, err := os.ReadDir(r.Dir)
	if err != nil {
		return nil, err
	}
	var keys []Key
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".log")
		if !ok || e.IsDir() {
			continue
		}
		if k, ok := ParseKey(name); ok && !recorded[k] {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, func(a, b Key) int { return strings.Compare(a.String(), b.String()) })
	return keys, nil
}

// ReadLog returns the section's combined log. ok is false when the section has none.
func (r *Run) ReadLog(k Key) (data []byte, ok bool, err error) {
	if _, valid := ParseKey(k.String()); !valid {
		return nil, false, fmt.Errorf("invalid log key %q", k.String())
	}
	data, err = os.ReadFile(filepath.Join(r.Dir, k.String()+".log"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if data == nil {
		data = []byte{}
	}
	return data, true, nil
}
