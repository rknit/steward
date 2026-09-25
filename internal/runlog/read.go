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

	"github.com/rknit/steward/internal/runner"
)

// ErrUnknownRun means no run directory has the requested ID.
var ErrUnknownRun = errors.New("unknown run")

// ErrNoManifest means the run directory has no run.json.
var ErrNoManifest = errors.New("no run.json")

var idPattern = regexp.MustCompile(`^\d{8}T\d{6}Z-[0-9a-f]{4}$`)

// projectPattern is the project name shape stew ever writes to run.json.
var projectPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// phaseNames are the phase names that can appear in a log file name, as used after CI fallback.
var phaseNames = []string{"setup", "build", "ci.full", "ci.quick", "ci.pre-commit"}

var statuses = []runner.Status{runner.Done, runner.Pass, runner.Skip, runner.Fail, runner.Blocked, runner.Interrupted}

// Latest returns the run ID that sorts last in <stewDir>/runs.
func Latest(stewDir string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(stewDir, "runs"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrUnknownRun
	}
	if err != nil {
		return "", err
	}
	latest := ""
	for _, e := range entries {
		if e.IsDir() && idPattern.MatchString(e.Name()) {
			latest = max(latest, e.Name())
		}
	}
	if latest == "" {
		return "", ErrUnknownRun
	}
	return latest, nil
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
	if m.TotalMS != nil && *m.TotalMS < 0 {
		return fmt.Errorf("negative total_ms")
	}
	for i, p := range m.Phases {
		switch {
		case !slices.Contains(statuses, runner.Status(p.Status)):
			return fmt.Errorf("phase %d: unknown status %q", i, p.Status)
		case !projectPattern.MatchString(p.Project):
			return fmt.Errorf("phase %d: invalid project %q", i, p.Project)
		case !slices.Contains(m.Projects, p.Project):
			return fmt.Errorf("phase %d: project %q is not in projects", i, p.Project)
		case !slices.Contains(m.Columns, p.Phase):
			return fmt.Errorf("phase %d: phase %q is not in columns", i, p.Phase)
		case !slices.Contains(phaseNames, p.Used):
			return fmt.Errorf("phase %d: unknown used phase %q", i, p.Used)
		case strings.HasPrefix(p.Phase, "ci.") != strings.HasPrefix(p.Used, "ci."),
			!strings.HasPrefix(p.Phase, "ci.") && p.Phase != p.Used:
			return fmt.Errorf("phase %d: %q cannot run as %q", i, p.Phase, p.Used)
		case p.DurationMS != nil && *p.DurationMS < 0:
			return fmt.Errorf("phase %d: negative duration", i)
		}
	}
	return nil
}

// PhaseKey names a phase's log files: <Project>-<Used>.
type PhaseKey struct {
	Project string
	Used    string
}

// String returns "<project>-<used>".
func (k PhaseKey) String() string { return k.Project + "-" + k.Used }

// ParseKey splits "<project>-<phase>" at its known phase suffix.
func ParseKey(key string) (PhaseKey, bool) {
	for _, ph := range phaseNames {
		if project, ok := strings.CutSuffix(key, "-"+ph); ok && project != "" {
			return PhaseKey{Project: project, Used: ph}, true
		}
	}
	return PhaseKey{}, false
}

// Unfinished returns the phases that have a .log file but no record, sorted by key.
func (r *Run) Unfinished() ([]PhaseKey, error) {
	recorded := make(map[PhaseKey]bool, len(r.Manifest.Phases))
	for _, p := range r.Manifest.Phases {
		recorded[PhaseKey{Project: p.Project, Used: p.Used}] = true
	}
	entries, err := os.ReadDir(r.Dir)
	if err != nil {
		return nil, err
	}
	var keys []PhaseKey
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".log")
		if !ok || e.IsDir() {
			continue
		}
		if k, ok := ParseKey(name); ok && !recorded[k] {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, func(a, b PhaseKey) int { return strings.Compare(a.String(), b.String()) })
	return keys, nil
}

// ReadLog returns the phase's combined log. ok is false when the phase has none.
func (r *Run) ReadLog(k PhaseKey) (data []byte, ok bool, err error) {
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
