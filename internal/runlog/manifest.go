package runlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/rknit/steward/internal/runner"
)

// ManifestName is the manifest's file name inside a run directory.
const ManifestName = "run.json"

// Manifest is run.json: what a run did, for stew runs show.
type Manifest struct {
	Argv             []string      `json:"argv"`
	WorkspaceWrapper string        `json:"workspace_wrapper"`
	Columns          []string      `json:"columns"`
	Projects         []string      `json:"projects"`
	Phases           []PhaseRecord `json:"phases"`
	TotalMS          *int64        `json:"total_ms,omitempty"`
}

// PhaseRecord is one phase that ended or was blocked.
type PhaseRecord struct {
	Project    string   `json:"project"`
	Phase      string   `json:"phase"`
	Used       string   `json:"used"`
	Status     string   `json:"status"`
	DurationMS *int64   `json:"duration_ms,omitempty"`
	Cause      string   `json:"cause,omitempty"`
	BlockedBy  []string `json:"blocked_by,omitempty"`
}

// Start records the command line, the workspace wrapper, and the plan, then saves run.json.
func (r *Run) Start(argv []string, workspaceWrapper string, columns, projects []string) error {
	r.Manifest = Manifest{Argv: argv, WorkspaceWrapper: workspaceWrapper, Columns: columns, Projects: projects, Phases: []PhaseRecord{}}
	return r.save()
}

// PhaseEnd implements runner.Recorder. If the save fails, the record is kept in memory as
// runner.LogErrorOutcome makes the runner report it, for the next successful save to include.
func (r *Run) PhaseEnd(project string, ph runner.Phase, out runner.Outcome) error {
	r.Manifest.Phases = append(r.Manifest.Phases, phaseRecord(project, ph, out))
	if err := r.save(); err != nil {
		last := len(r.Manifest.Phases) - 1
		r.Manifest.Phases[last] = phaseRecord(project, ph, runner.LogErrorOutcome(out, err))
		return err
	}
	return nil
}

// phaseRecord builds the record PhaseEnd saves for a phase's outcome.
func phaseRecord(project string, ph runner.Phase, out runner.Outcome) PhaseRecord {
	rec := PhaseRecord{Project: project, Phase: ph.Name, Used: ph.Used, Status: string(out.Status)}
	if out.Status == runner.Fail || out.Status == runner.Interrupted {
		rec.Cause = out.Cause
	}
	if out.Status != runner.Interrupted {
		rec.DurationMS = milliseconds(out.Duration)
	}
	return rec
}

// Blocked implements runner.Recorder. If the save fails, the record stays for the next save.
func (r *Run) Blocked(project string, ph runner.Phase, by []string) error {
	r.Manifest.Phases = append(r.Manifest.Phases, PhaseRecord{
		Project: project, Phase: ph.Name, Used: ph.Used, Status: string(runner.Blocked), BlockedBy: by,
	})
	return r.save()
}

// Finish records the run's wall time, which marks the run finished, then saves run.json.
func (r *Run) Finish(total time.Duration) error {
	r.Manifest.TotalMS = milliseconds(total)
	return r.save()
}

func milliseconds(d time.Duration) *int64 {
	ms := d.Milliseconds()
	return &ms
}

// save writes run.json atomically: a temp file in the run directory, then rename.
func (r *Run) save() error {
	data, err := json.MarshalIndent(r.Manifest, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(r.Dir, "."+ManifestName+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(r.Dir, ManifestName))
}

// Result is the run's outcome for stew runs list: "unfinished" without a total, then "interrupted",
// "fail" for a failed or blocked phase, and "ok" otherwise.
func (m Manifest) Result() string {
	if m.TotalMS == nil {
		return "unfinished"
	}
	has := func(statuses ...runner.Status) bool {
		return slices.ContainsFunc(m.Phases, func(p PhaseRecord) bool { return slices.Contains(statuses, runner.Status(p.Status)) })
	}
	switch {
	case has(runner.Interrupted):
		return "interrupted"
	case has(runner.Fail, runner.Blocked):
		return "fail"
	}
	return "ok"
}
