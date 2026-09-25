package runlog

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rknit/steward/internal/runner"
)

func newRun(t *testing.T) *Run {
	t.Helper()
	run, err := Create(t.TempDir(), now, bytes.NewReader([]byte{1, 2}))
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func readManifest(t *testing.T, run *Run) (Manifest, string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(run.Dir, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m, string(data)
}

func i64(v int64) *int64 { return &v }

func TestManifestSaves(t *testing.T) {
	run := newRun(t)
	setup := runner.Phase{Name: "setup", Used: "setup"}
	build := runner.Phase{Name: "build", Used: "build"}
	ciFB := runner.Phase{Name: "ci.pre-commit", Used: "ci.quick", CI: true}

	if err := run.Start([]string{"ci", "--level", "pre-commit"}, []string{"setup", "build", "ci.pre-commit"}, []string{"core", "api", "web"}); err != nil {
		t.Fatal(err)
	}
	m, raw := readManifest(t, run)
	if len(m.Phases) != 0 || !strings.Contains(raw, `"phases": []`) || strings.Contains(raw, "total_ms") {
		t.Errorf("after Start: %s", raw)
	}

	steps := []error{
		run.PhaseEnd("core", setup, runner.Outcome{Status: runner.Skip, Duration: 104 * time.Millisecond}),
		run.PhaseEnd("core", ciFB, runner.Outcome{Status: runner.Pass, Duration: 8210 * time.Millisecond}),
		run.PhaseEnd("api", build, runner.Outcome{Status: runner.Fail, Duration: 63012 * time.Millisecond, Cause: "exit 1"}),
		run.Blocked("web", setup, []string{"api"}),
		run.PhaseEnd("lib", setup, runner.Outcome{Status: runner.Interrupted, Duration: time.Second, Cause: "signal SIGINT"}),
		run.Finish(75004 * time.Millisecond),
	}
	for i, err := range steps {
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}

	got, raw := readManifest(t, run)
	want := Manifest{
		Argv:     []string{"ci", "--level", "pre-commit"},
		Columns:  []string{"setup", "build", "ci.pre-commit"},
		Projects: []string{"core", "api", "web"},
		Phases: []PhaseRecord{
			{Project: "core", Phase: "setup", Used: "setup", Status: "skip", DurationMS: i64(104)},
			{Project: "core", Phase: "ci.pre-commit", Used: "ci.quick", Status: "pass", DurationMS: i64(8210)},
			{Project: "api", Phase: "build", Used: "build", Status: "fail", DurationMS: i64(63012), Cause: "exit 1"},
			{Project: "web", Phase: "setup", Used: "setup", Status: "blocked", BlockedBy: []string{"api"}},
			{Project: "lib", Phase: "setup", Used: "setup", Status: "interrupted", Cause: "signal SIGINT"},
		},
		TotalMS: i64(75004),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("manifest:\n%s", raw)
	}
	if !reflect.DeepEqual(run.Manifest, want) {
		t.Errorf("in-memory manifest = %+v", run.Manifest)
	}

	entries, err := os.ReadDir(run.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != ManifestName {
		t.Errorf("run dir holds %v, want only %s (no temp files)", entries, ManifestName)
	}
}

func TestManifestSaveErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	run := newRun(t)
	setup := runner.Phase{Name: "setup", Used: "setup"}
	if err := run.Start([]string{"build"}, []string{"setup", "build"}, []string{"core", "api", "lib"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(run.Dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(run.Dir, 0o755) })

	if err := run.PhaseEnd("core", setup, runner.Outcome{Status: runner.Done, Duration: 5 * time.Millisecond}); err == nil {
		t.Fatal("PhaseEnd in a read-only run dir succeeded")
	}
	if len(run.Manifest.Phases) != 1 {
		t.Fatalf("failed PhaseEnd dropped its record: %+v", run.Manifest.Phases)
	}
	if rec := run.Manifest.Phases[0]; rec.Status != "fail" || !strings.HasPrefix(rec.Cause, "log error: ") ||
		rec.DurationMS == nil || *rec.DurationMS != 5 {
		t.Errorf("failed PhaseEnd for a done phase kept as: %+v", rec)
	}

	if err := run.PhaseEnd("api", setup, runner.Outcome{Status: runner.Fail, Duration: 2 * time.Millisecond, Cause: "exit 1"}); err == nil {
		t.Fatal("PhaseEnd in a read-only run dir succeeded")
	}
	if len(run.Manifest.Phases) != 2 {
		t.Fatalf("failed PhaseEnd dropped its record: %+v", run.Manifest.Phases)
	}
	if rec := run.Manifest.Phases[1]; rec.Status != "fail" || rec.Cause != "exit 1" ||
		rec.DurationMS == nil || *rec.DurationMS != 2 {
		t.Errorf("failed PhaseEnd for an already-fail phase kept as: %+v", rec)
	}

	if err := run.Blocked("lib", setup, []string{"core"}); err == nil {
		t.Fatal("Blocked in a read-only run dir succeeded")
	}
	if len(run.Manifest.Phases) != 3 {
		t.Errorf("failed Blocked dropped its record: %+v", run.Manifest.Phases)
	}
	if err := run.Finish(time.Second); err == nil {
		t.Fatal("Finish in a read-only run dir succeeded")
	}

	os.Chmod(run.Dir, 0o755)
	if err := run.Finish(time.Second); err != nil {
		t.Fatal(err)
	}
	m, _ := readManifest(t, run)
	if len(m.Phases) != 3 || m.Phases[2].Status != "blocked" {
		t.Errorf("next save did not include the blocked record: %+v", m.Phases)
	}
	if m.Phases[0].Status != "fail" || !strings.HasPrefix(m.Phases[0].Cause, "log error: ") {
		t.Errorf("next save did not include the log-error record: %+v", m.Phases[0])
	}
	if m.Phases[1].Status != "fail" || m.Phases[1].Cause != "exit 1" {
		t.Errorf("next save did not keep the already-fail record's cause: %+v", m.Phases[1])
	}
}
