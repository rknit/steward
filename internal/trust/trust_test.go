package trust

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rknit/steward/internal/runner"
	"github.com/rknit/steward/internal/workspace"
)

type call struct {
	dir  string
	env  []string
	argv []string
}

// fakeExec records calls and returns results by command; a command without one succeeds.
type fakeExec struct {
	calls   []call
	results map[string]runner.Result
}

func (f *fakeExec) Run(_ context.Context, dir string, env, argv []string, _, _ io.Writer) runner.Result {
	f.calls = append(f.calls, call{dir, env, argv})
	return f.results[argv[len(argv)-1]]
}

func newRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, workspace.DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestEntries(t *testing.T) {
	t.Parallel()
	ws := &workspace.Workspace{Trust: "direnv allow .", Projects: []*workspace.Project{
		{Name: "api", Path: "services/api", Trust: "mise trust"},
		{Name: "core", Path: "libs/core"},
		{Name: "root", Path: ".", Trust: "r"},
	}}
	want := []Entry{{Workspace, "", "direnv allow ."}, {"api", "services/api", "mise trust"}, {"root", ".", "r"}}
	if got := Entries(ws); !slices.Equal(got, want) {
		t.Errorf("Entries = %+v, want %+v", got, want)
	}
	if got := Entries(&workspace.Workspace{}); len(got) != 0 {
		t.Errorf("Entries of a workspace without trust = %+v", got)
	}
}

func TestPending(t *testing.T) {
	t.Parallel()
	root := newRoot(t)
	entries := []Entry{{Workspace, "", "ws"}, {"api", "services/api", "a"}, {"core", "libs/core", "c"}}
	if got := Load(root).Pending(entries); !slices.Equal(got, entries) {
		t.Errorf("no file: pending = %+v, want all", got)
	}
	rec := Record{Root: root, Workspace: "ws", Projects: map[string]string{"services/api": "old", "libs/core": "c"}}
	if err := Save(root, rec, []string{"services/api", "libs/core"}); err != nil {
		t.Fatal(err)
	}
	if got := Load(root).Pending(entries); !slices.Equal(got, entries[1:2]) {
		t.Errorf("changed api command: pending = %+v, want only api", got)
	}
}

func TestLoadOtherRoot(t *testing.T) {
	t.Parallel()
	root := newRoot(t)
	data := `{"root":"/somewhere/else","workspace":"ws"}`
	if err := os.WriteFile(filepath.Join(root, workspace.DirName, File), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := []Entry{{Workspace, "", "ws"}}
	if got := Load(root).Pending(entries); !slices.Equal(got, entries) {
		t.Errorf("record of another root: pending = %+v, want all", got)
	}
	if got := Load(root); got.Root != root || got.Workspace != "" {
		t.Errorf("record of another root loads as %+v, want empty under %s", got, root)
	}
}

func TestLoadThroughSymlink(t *testing.T) {
	t.Parallel()
	root := newRoot(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	entries := []Entry{{Workspace, "", "ws"}}
	for _, tc := range []struct{ saved, loaded string }{{link, root}, {root, link}} {
		if err := Save(tc.saved, Record{Workspace: "ws"}, nil); err != nil {
			t.Fatal(err)
		}
		if got := Load(tc.loaded).Pending(entries); len(got) != 0 {
			t.Errorf("saved via %s, loaded via %s: pending = %+v, want none", tc.saved, tc.loaded, got)
		}
	}
}

func TestLoadInvalid(t *testing.T) {
	t.Parallel()
	root := newRoot(t)
	if err := os.WriteFile(filepath.Join(root, workspace.DirName, File), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Load(root); got.Root != root || got.Workspace != "" || len(got.Projects) != 0 {
		t.Errorf("invalid file loads as %+v, want empty", got)
	}
}

func TestRecordRootProject(t *testing.T) {
	t.Parallel()
	root := newRoot(t)
	entries := []Entry{{Workspace, "", "ws"}, {"root", ".", "r"}}
	rec := Load(root)
	if err := Run(context.Background(), root, &rec, entries[1:], []string{"."}, &fakeExec{}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := Load(root).Pending(entries); !slices.Equal(got, entries[:1]) {
		t.Errorf("after trusting the root project: pending = %+v, want only the workspace", got)
	}
}

func TestRun(t *testing.T) {
	t.Parallel()
	root := newRoot(t)
	entries := []Entry{{Workspace, "", "ws"}, {"api", "services/api", "a"}, {"core", "libs/core", "c"}}
	ex := &fakeExec{results: map[string]runner.Result{"a": {ExitCode: 3}}}
	rec := Load(root)
	err := Run(context.Background(), root, &rec, entries, []string{"services/api", "libs/core"}, ex, io.Discard, io.Discard)
	if err == nil || err.Error() != "trust api: exit 3" {
		t.Errorf("err = %v, want trust api: exit 3", err)
	}
	want := []call{
		{root, []string{"STEW_ROOT=" + root}, []string{"sh", "-c", "ws"}},
		{filepath.Join(root, "services/api"), []string{"STEW_ROOT=" + root, "STEW_PROJECT=api"}, []string{"sh", "-c", "a"}},
	}
	if len(ex.calls) != len(want) {
		t.Fatalf("calls = %+v, want %+v (the failure stops the run)", ex.calls, want)
	}
	for i := range want {
		if ex.calls[i].dir != want[i].dir || !slices.Equal(ex.calls[i].env, want[i].env) || !slices.Equal(ex.calls[i].argv, want[i].argv) {
			t.Errorf("call %d = %+v, want %+v", i, ex.calls[i], want[i])
		}
	}
	if got := Load(root).Pending(entries); !slices.Equal(got, entries[1:]) {
		t.Errorf("pending = %+v, want api and core (workspace recorded before the failure)", got)
	}
}

func TestSaveDropsUnregistered(t *testing.T) {
	t.Parallel()
	root := newRoot(t)
	rec := Record{Root: root, Workspace: "a > b && c", Projects: map[string]string{"gone": "x", "libs/core": "c"}}
	if err := Save(root, rec, []string{"libs/core"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, workspace.DirName, File))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"root":"` + root + `","workspace":"a > b && c","projects":{"libs/core":"c"}}` + "\n"
	if string(data) != want {
		t.Errorf("trust.json = %q, want %q: only libs/core, one line, no HTML escapes", data, want)
	}
	entries, _ := os.ReadDir(filepath.Join(root, workspace.DirName))
	if len(entries) != 1 {
		t.Errorf(".stew holds %d entries, want only trust.json (no temp file left)", len(entries))
	}
}

func TestRunSaveError(t *testing.T) {
	t.Parallel()
	root := newRoot(t)
	if err := os.Mkdir(filepath.Join(root, workspace.DirName, File), 0o755); err != nil {
		t.Fatal(err)
	}
	rec := Load(root)
	err := Run(context.Background(), root, &rec, []Entry{{Workspace, "", "ws"}}, nil, &fakeExec{}, io.Discard, io.Discard)
	if err == nil || !strings.HasPrefix(err.Error(), "trust: save .stew/trust.json: ") {
		t.Errorf("err = %v, want a save error", err)
	}
	var pe *os.LinkError
	if !errors.As(err, &pe) {
		t.Errorf("err = %v, want the rename error wrapped", err)
	}
}
