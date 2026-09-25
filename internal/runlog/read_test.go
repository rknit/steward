package runlog

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rknit/steward/internal/runner"
)

func TestLoadRoundTrip(t *testing.T) {
	run := newRun(t)
	if err := run.Start([]string{"build"}, []string{"setup", "build"}, []string{"core"}); err != nil {
		t.Fatal(err)
	}
	if err := run.PhaseEnd("core", runner.Phase{Name: "setup", Used: "setup"}, runner.Outcome{Status: runner.Done, Duration: time.Second}); err != nil {
		t.Fatal(err)
	}
	if err := run.Finish(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	stewDir := filepath.Dir(filepath.Dir(run.Dir))
	got, err := Load(stewDir, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != run.ID || got.Dir != run.Dir || !reflect.DeepEqual(got.Manifest, run.Manifest) {
		t.Errorf("Load = %+v, want %+v", got, run)
	}
}

func writeRun(t *testing.T, stewDir, id, manifest string) {
	t.Helper()
	dir := filepath.Join(stewDir, "runs", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const validManifest = `{"argv":["ci"],"columns":["setup","build","ci.full"],"projects":["core"],"phases":[` +
	`{"project":"core","phase":"setup","used":"setup","status":"done","duration_ms":5},` +
	`{"project":"core","phase":"ci.full","used":"ci.full","status":"fail","duration_ms":7,"cause":"exit 1"}]}`

func TestLoadErrors(t *testing.T) {
	stewDir := t.TempDir()
	writeRun(t, stewDir, "20260101T000000Z-0000", "")
	writeRun(t, stewDir, "20260101T000000Z-0001", "{not json")
	writeRun(t, stewDir, "20260101T000000Z-0002", validManifest)

	for _, id := range []string{"20000101T000000Z-0000", "../../etc", "latest", "", "runs"} {
		if _, err := Load(stewDir, id); !errors.Is(err, ErrUnknownRun) {
			t.Errorf("Load(%q) = %v, want ErrUnknownRun", id, err)
		}
	}
	if _, err := Load(stewDir, "20260101T000000Z-0000"); !errors.Is(err, ErrNoManifest) {
		t.Errorf("missing run.json: err = %v", err)
	}
	if _, err := Load(stewDir, "20260101T000000Z-0001"); err == nil || errors.Is(err, ErrUnknownRun) || errors.Is(err, ErrNoManifest) {
		t.Errorf("invalid JSON: err = %v", err)
	}
	if _, err := Load(stewDir, "20260101T000000Z-0002"); err != nil {
		t.Errorf("valid manifest: %v", err)
	}
}

func TestLoadRejectsInconsistentManifest(t *testing.T) {
	record := func(fields string) string {
		return `{"argv":["ci"],"columns":["setup","ci.pre-commit"],"projects":["core"],"phases":[{` + fields + `}]}`
	}
	tests := map[string]string{
		"unknown status":     record(`"project":"core","phase":"setup","used":"setup","status":"great"`),
		"unknown project":    record(`"project":"web","phase":"setup","used":"setup","status":"done"`),
		"phase not a column": record(`"project":"core","phase":"build","used":"build","status":"done"`),
		"used not a phase":   record(`"project":"core","phase":"setup","used":"lint","status":"done"`),
		"non-ci used for ci": record(`"project":"core","phase":"ci.pre-commit","used":"setup","status":"pass"`),
		"ci used for setup":  record(`"project":"core","phase":"setup","used":"ci.full","status":"done"`),
		"negative duration":  record(`"project":"core","phase":"setup","used":"setup","status":"done","duration_ms":-1`),
		"null phases entry":  `{"argv":["ci"],"columns":["setup"],"projects":["core"],"phases":[null]}`,
		"bad project in projects": `{"argv":["ci"],"columns":["setup"],"projects":["core","../../x"],` +
			`"phases":[{"project":"../../x","phase":"setup","used":"setup","status":"done"}]}`,
		"negative total": `{"argv":["ci"],"columns":["setup"],"projects":["core"],"phases":[],"total_ms":-1}`,
	}
	for name, manifest := range tests {
		stewDir := t.TempDir()
		writeRun(t, stewDir, "20260101T000000Z-0000", manifest)
		if _, err := Load(stewDir, "20260101T000000Z-0000"); err == nil || !strings.Contains(err.Error(), "invalid run.json") {
			t.Errorf("%s: err = %v, want an invalid run.json error", name, err)
		}
	}
}

func TestLatest(t *testing.T) {
	stewDir := t.TempDir()
	if _, err := Latest(stewDir); !errors.Is(err, ErrUnknownRun) {
		t.Errorf("no runs dir: err = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(stewDir, "runs", "tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Latest(stewDir); !errors.Is(err, ErrUnknownRun) {
		t.Errorf("no run dirs: err = %v", err)
	}
	writeRun(t, stewDir, "20260925T000000Z-ffff", "")
	writeRun(t, stewDir, "20260925T000001Z-0000", "")
	if err := os.WriteFile(filepath.Join(stewDir, "runs", "zzz"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := Latest(stewDir); err != nil || got != "20260925T000001Z-0000" {
		t.Errorf("Latest = %q, %v", got, err)
	}
}

func TestIDs(t *testing.T) {
	stewDir := t.TempDir()
	if ids, err := IDs(stewDir); err != nil || ids != nil {
		t.Errorf("no runs dir: IDs = %q, %v", ids, err)
	}
	writeRun(t, stewDir, "20260925T000001Z-0000", "")
	writeRun(t, stewDir, "20260925T000000Z-ffff", "")
	writeRun(t, stewDir, "tmp", "")
	if err := os.WriteFile(filepath.Join(stewDir, "runs", "20260925T000002Z-0000"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	want := []string{"20260925T000000Z-ffff", "20260925T000001Z-0000"}
	if ids, err := IDs(stewDir); err != nil || !slices.Equal(ids, want) {
		t.Errorf("IDs = %q, %v; want %q", ids, err, want)
	}
}

func TestStartTime(t *testing.T) {
	got, err := StartTime("20260925T043601Z-3f9a")
	if want := time.Date(2026, 9, 25, 4, 36, 1, 0, time.UTC); err != nil || !got.Equal(want) {
		t.Errorf("StartTime = %v, %v; want %v", got, err, want)
	}
	for _, id := range []string{"20261325T043601Z-3f9a", "latest", ""} {
		if _, err := StartTime(id); err == nil {
			t.Errorf("StartTime(%q): want an error", id)
		}
	}
}

func TestParseKey(t *testing.T) {
	tests := []struct {
		key  string
		want PhaseKey
		ok   bool
	}{
		{"api-build", PhaseKey{"api", "build"}, true},
		{"my-app-setup", PhaseKey{"my-app", "setup"}, true},
		{"api-ci.pre-commit", PhaseKey{"api", "ci.pre-commit"}, true},
		{"api-ci.quick", PhaseKey{"api", "ci.quick"}, true},
		{"x-build-build", PhaseKey{"x-build", "build"}, true},
		{"x-setup-ci.full", PhaseKey{"x-setup", "ci.full"}, true},
		{"api-lint", PhaseKey{}, false},
		{"-setup", PhaseKey{}, false},
		{"setup", PhaseKey{}, false},
	}
	for _, tt := range tests {
		got, ok := ParseKey(tt.key)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ParseKey(%q) = %v, %v; want %v, %v", tt.key, got, ok, tt.want, tt.ok)
		}
		if ok && got.String() != tt.key {
			t.Errorf("%v.String() = %q", got, got.String())
		}
	}
}

func TestUnfinishedAndReadLog(t *testing.T) {
	run := newRun(t)
	if err := run.Start([]string{"build"}, []string{"setup", "build"}, []string{"core", "x-build", "api"}); err != nil {
		t.Fatal(err)
	}
	setup := runner.Phase{Name: "setup", Used: "setup"}
	log, err := run.OpenPhase("core", "setup")
	if err != nil {
		t.Fatal(err)
	}
	log.Marker("run", "make")
	log.Close()
	if err := run.PhaseEnd("core", setup, runner.Outcome{Status: runner.Done}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []PhaseKey{{"x-build", "build"}, {"api", "setup"}} {
		l, err := run.OpenPhase(k.Project, k.Used)
		if err != nil {
			t.Fatal(err)
		}
		l.Close()
	}
	if err := os.WriteFile(filepath.Join(run.Dir, "notes.log"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := run.Unfinished()
	if err != nil {
		t.Fatal(err)
	}
	if want := []PhaseKey{{"api", "setup"}, {"x-build", "build"}}; !slices.Equal(got, want) {
		t.Errorf("Unfinished = %v, want %v", got, want)
	}

	data, ok, err := run.ReadLog(PhaseKey{"core", "setup"})
	if err != nil || !ok || string(data) != "--- stew: run: make\n" {
		t.Errorf("ReadLog(core-setup) = %q, %v, %v", data, ok, err)
	}
	data, ok, err = run.ReadLog(PhaseKey{"api", "setup"})
	if err != nil || !ok || data == nil || len(data) != 0 {
		t.Errorf("ReadLog(empty api-setup) = %q, %v, %v; want non-nil empty data", data, ok, err)
	}
	if _, ok, err := run.ReadLog(PhaseKey{"core", "build"}); ok || err != nil {
		t.Errorf("ReadLog(missing) ok=%v err=%v", ok, err)
	}
}
