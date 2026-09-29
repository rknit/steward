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
	t.Parallel()
	run := newRun(t)
	if err := run.Start([]string{"build"}, "", nil, []string{"setup", "build"}, []string{"core"}); err != nil {
		t.Fatal(err)
	}
	if err := run.SectionEnd(runner.Section{Project: "core", Name: "setup"}, runner.Outcome{Status: runner.Done, Duration: time.Second}); err != nil {
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

const validManifest = `{"argv":["ci"],"columns":["setup","build","ci.full"],"projects":["core"],"sections":[` +
	`{"project":"core","section":"setup","status":"done","duration_ms":5},` +
	`{"project":"core","section":"ci.full","status":"fail","duration_ms":7,"cause":"exit 1"}]}`

func TestLoadErrors(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	record := func(fields string) string {
		return `{"argv":["ci"],"columns":["setup","ci.pre-commit"],"projects":["core"],"sections":[{` + fields + `}]}`
	}
	tests := []struct{ name, manifest, want string }{
		{"unknown status", record(`"project":"core","section":"setup","status":"great"`),
			`section 0: unknown status "great"`},
		{"unknown status pass", record(`"project":"core","section":"setup","status":"pass"`),
			`section 0: unknown status "pass"`},
		{"unknown project", record(`"project":"web","section":"setup","status":"done"`),
			`section 0: project "web" is not in projects`},
		{"section not a column", record(`"project":"core","section":"build","status":"done"`),
			`section 0: section "build" is not in columns`},
		{"negative duration", record(`"project":"core","section":"setup","status":"done","duration_ms":-1`),
			"section 0: negative duration"},
		{"invalid blocked_by", record(`"project":"core","section":"setup","status":"blocked","blocked_by":["core"]`),
			`section 0: invalid blocked_by "core"`},
		{"null sections entry", `{"argv":["ci"],"columns":["setup"],"projects":["core"],"sections":[null]}`,
			`section 0: unknown status ""`},
		{"bad project in projects", `{"argv":["ci"],"columns":["setup"],"projects":["core","../../x"],` +
			`"sections":[{"project":"../../x","section":"setup","status":"done"}]}`,
			`projects 1: invalid project "../../x"`},
		{"negative total", `{"argv":["ci"],"columns":["setup"],"projects":["core"],"sections":[],"total_ms":-1}`,
			"negative total_ms"},
		{"invalid column", `{"argv":["ci"],"columns":["../x"],"projects":["api"],"sections":[]}`,
			`columns 0: invalid column "../x"`},
	}
	for _, tt := range tests {
		stewDir := t.TempDir()
		writeRun(t, stewDir, "20260101T000000Z-0000", tt.manifest)
		_, err := Load(stewDir, "20260101T000000Z-0000")
		if want := "invalid run.json: " + tt.want; err == nil || err.Error() != want {
			t.Errorf("%s: err = %v, want %q", tt.name, err, want)
		}
	}
}

func TestLoadProjectWrapper(t *testing.T) {
	t.Parallel()
	stewDir := t.TempDir()
	writeRun(t, stewDir, "20260101T000000Z-0000",
		`{"argv":["ci"],"columns":["setup"],"projects":["core"],"project_wrapper":{"core":"x"},"sections":[]}`)
	writeRun(t, stewDir, "20260101T000000Z-0001",
		`{"argv":["ci"],"columns":["setup"],"projects":["core"],"project_wrapper":{"ghost":"x"},"sections":[]}`)
	if run, err := Load(stewDir, "20260101T000000Z-0000"); err != nil || run.Manifest.ProjectWrapper["core"] != "x" {
		t.Errorf("valid project_wrapper: run = %+v, err = %v", run, err)
	}
	want := `invalid run.json: project_wrapper: project "ghost" is not in projects`
	if _, err := Load(stewDir, "20260101T000000Z-0001"); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("unknown project_wrapper project: err = %v, want %q", err, want)
	}
}

func TestLatest(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	tests := []struct {
		in   string
		want Key
		ok   bool
	}{
		{"api:build", Key{"api", "build"}, true},
		{"my.lib:ci.pre-commit", Key{"my.lib", "ci.pre-commit"}, true},
		{"api-build", Key{}, false},
		{"api:../x", Key{}, false},
		{"API:build", Key{}, false},
		{"a:b:c", Key{}, false},
	}
	for _, tt := range tests {
		got, ok := ParseKey(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ParseKey(%q) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

// An old run has "phases" instead of "sections" and logs named <project>-<phase>.
func TestLoadOldRun(t *testing.T) {
	t.Parallel()
	stewDir := t.TempDir()
	const id = "20260925T043601Z-3f9a"
	writeRun(t, stewDir, id, `{"argv":["build"],"columns":["setup","build"],"projects":["api"],`+
		`"phases":[{"project":"api","phase":"setup","used":"setup","status":"done","duration_ms":1}],"total_ms":5}`)
	if err := os.WriteFile(filepath.Join(stewDir, "runs", id, "api-setup.log"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run, err := Load(stewDir, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Manifest.Sections) != 0 {
		t.Errorf("sections = %+v", run.Manifest.Sections)
	}
	unfinished, err := run.Unfinished()
	if err != nil || len(unfinished) != 0 {
		t.Errorf("unfinished = %v, %v", unfinished, err)
	}
}

func TestUnfinishedAndReadLog(t *testing.T) {
	t.Parallel()
	run := newRun(t)
	if err := run.Start([]string{"build"}, "", nil, []string{"setup", "build"}, []string{"core", "x-build", "api"}); err != nil {
		t.Fatal(err)
	}
	setup := runner.Section{Project: "core", Name: "setup"}
	log, err := run.OpenSection("core", "setup")
	if err != nil {
		t.Fatal(err)
	}
	log.Marker("run", "make")
	log.Close()
	if err := run.SectionEnd(setup, runner.Outcome{Status: runner.Done}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []Key{{"x-build", "build"}, {"api", "setup"}} {
		l, err := run.OpenSection(k.Project, k.Section)
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
	if want := []Key{{"api", "setup"}, {"x-build", "build"}}; !slices.Equal(got, want) {
		t.Errorf("Unfinished = %v, want %v", got, want)
	}

	data, ok, err := run.ReadLog(Key{"core", "setup"})
	if err != nil || !ok || string(data) != "--- stew: run: make\n" {
		t.Errorf("ReadLog(core:setup) = %q, %v, %v", data, ok, err)
	}
	data, ok, err = run.ReadLog(Key{"api", "setup"})
	if err != nil || !ok || data == nil || len(data) != 0 {
		t.Errorf("ReadLog(empty api:setup) = %q, %v, %v; want non-nil empty data", data, ok, err)
	}
	if _, ok, err := run.ReadLog(Key{"core", "build"}); ok || err != nil {
		t.Errorf("ReadLog(missing) ok=%v err=%v", ok, err)
	}
	if _, ok, err := run.ReadLog(Key{"..", "x/../../y"}); ok || err == nil || err.Error() != `invalid log key "..:x/../../y"` {
		t.Errorf("ReadLog(unsafe key) ok=%v err=%v", ok, err)
	}
}
