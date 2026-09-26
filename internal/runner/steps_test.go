package runner

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// collected is what Collect returned for a step.
type collected struct {
	reaches, status int
	finished        bool
}

// runStep prepares key through d and runs it with the real sh in dir, returning the outermost wrapper's result,
// the output, and what Collect returned.
func runStep(t *testing.T, d *StepDir, dir string, wrappers, env []string, cmd string) (Result, string, collected) {
	t.Helper()
	argv, err := d.Prepare("p-build", wrappers, env, cmd)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	res := Shell{KillDelay: slowKillDelay}.Run(context.Background(), dir, env, argv, &stdout, &stderr)
	var c collected
	if c.reaches, c.status, c.finished, err = d.Collect("p-build"); err != nil {
		t.Fatal(err)
	}
	return res, stdout.String() + stderr.String(), c
}

func testStepDir(t *testing.T) *StepDir {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	d, err := NewStepDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Remove() })
	return d
}

func TestShellQuote(t *testing.T) {
	for _, s := range []string{"", "a b", "it's", `say "hi"`, "$HOME", "two\nlines", `back\slash\`} {
		out, err := exec.Command("sh", "-c", "printf %s "+ShellQuote(s)).Output()
		if err != nil || string(out) != s {
			t.Errorf("shellQuote(%q) printed %q, %v", s, out, err)
		}
	}
}

func TestStepDirPathIsPlain(t *testing.T) {
	d := testStepDir(t)
	if !regexp.MustCompile(`^[A-Za-z0-9/._-]+$`).MatchString(d.Dir) || !filepath.IsAbs(d.Dir) ||
		!strings.HasPrefix(filepath.Base(d.Dir), "stew-") {
		t.Errorf("dir = %q", d.Dir)
	}
}

func TestStepDirRejectsTMPDIRNeedingQuotes(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "my tmp")
	os.Mkdir(tmp, 0o755)
	t.Setenv("TMPDIR", tmp)
	_, err := NewStepDir()
	if err == nil || !strings.Contains(err.Error(), "needs shell quoting; set TMPDIR to a path of letters, digits, and /._-") {
		t.Errorf("err = %v", err)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Errorf("left %d entries in TMPDIR", len(entries))
	}
}

func TestWrappedCommandRunsOnceWithExactEnv(t *testing.T) {
	d := testStepDir(t)
	root := t.TempDir() + "/it's a \"root\" $HOME"
	project := "two\nlines \\ and \\n"
	env := []string{"STEW_SECTION=build", "STEW_ROOT=" + root, "STEW_PROJECT=" + project}
	res, out, c := runStep(t, d, t.TempDir(),
		[]string{`export OUTER=o STEW_SECTION=clobbered STEW_ROOT=x STEW_PROJECT=y && {{STEW_STEP}}`},
		env, `printf '%s|%s|%s|%s\n' "$OUTER" "$STEW_SECTION" "$STEW_ROOT" "$STEW_PROJECT"; exit 3`)
	if c != (collected{1, 3, true}) || res != (Result{ExitCode: 3}) || out != "o|build|"+root+"|"+project+"\n" {
		t.Errorf("c=%+v res=%+v out=%q", c, res, out)
	}
}

func TestTwoLevelsNestOuterFirst(t *testing.T) {
	d := testStepDir(t)
	_, out, c := runStep(t, d, t.TempDir(), []string{`L=outer; export L; {{STEW_STEP}}`, `L="$L,inner" {{STEW_STEP}}`},
		nil, `echo "$L"`)
	if c.reaches != 1 || out != "outer,inner\n" {
		t.Errorf("c=%+v out=%q", c, out)
	}
}

func TestPrepareWritesWrapperTextUnchanged(t *testing.T) {
	d := testStepDir(t)
	outer, inner := "a 'x' \"$Y\" {{STEW_STEP}} ; b", "c\n{{STEW_STEP}} # d"
	argv, err := d.Prepare("p-build", []string{outer, inner}, []string{"STEW_SECTION=build"}, "make")
	if err != nil {
		t.Fatal(err)
	}
	level, step := filepath.Join(d.Dir, "p-build.1"), filepath.Join(d.Dir, "p-build.step")
	want := []string{"sh", "-c", strings.Replace(outer, StepPlaceholder, level, 1)}
	if strings.Join(argv, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv = %q, want %q", argv, want)
	}
	if got, _ := os.ReadFile(level); string(got) != "#!/bin/sh\n"+strings.Replace(inner, StepPlaceholder, step, 1)+"\n" {
		t.Errorf("level script = %q", got)
	}
	for _, path := range []string{level, step} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", path, info, err)
		}
	}
}

func TestPlaceholderInsideReparsedString(t *testing.T) {
	d := testStepDir(t)
	for _, w := range []string{
		`sh -c {{STEW_STEP}}`,
		`sh -c '{{STEW_STEP}}'`,
		`sh -c ". /dev/null && {{STEW_STEP}}"`,
	} {
		_, out, c := runStep(t, d, t.TempDir(), []string{w}, nil, "echo ran")
		if c.reaches != 1 || out != "ran\n" {
			t.Errorf("wrapper %q: c=%+v out=%q", w, c, out)
		}
	}
}

func TestWrapperThatDoesNotRunTheCommandOnce(t *testing.T) {
	d := testStepDir(t)
	for _, tc := range []struct {
		wrapper string
		want    int
	}{
		{"echo {{STEW_STEP}}", 0},
		{"false && {{STEW_STEP}}", 0},
		{"true # {{STEW_STEP}}", 0},
		{"no-such-tool-for-stew {{STEW_STEP}}", 0},
		{"for i in 1 2; do {{STEW_STEP}}; done", 2},
	} {
		_, _, c := runStep(t, d, t.TempDir(), []string{tc.wrapper}, nil, "true")
		if c.reaches != tc.want {
			t.Errorf("wrapper %q: reaches = %d, want %d", tc.wrapper, c.reaches, tc.want)
		}
	}
}

func TestStepResultIsTheCommandsStatus(t *testing.T) {
	d := testStepDir(t)
	for _, tc := range []struct {
		wrappers []string
		cmd      string
		want     collected
		wrapper  Result
	}{
		{[]string{"{{STEW_STEP}} || true"}, "exit 7", collected{1, 7, true}, Result{}},
		{[]string{"{{STEW_STEP}}; echo post"}, "exit 3", collected{1, 3, true}, Result{}},
		{[]string{"{{STEW_STEP}}; false"}, "true", collected{1, 0, true}, Result{ExitCode: 1}},
		{[]string{"{{STEW_STEP}}", "{{STEW_STEP}} || true"}, "exit 7", collected{1, 7, true}, Result{}},
		{[]string{"{{STEW_STEP}}"}, "kill -TERM $$", collected{1, 143, true}, Result{ExitCode: 143}},
	} {
		res, _, c := runStep(t, d, t.TempDir(), tc.wrappers, nil, tc.cmd)
		if c != tc.want || res != tc.wrapper {
			t.Errorf("wrappers %q, cmd %q: c=%+v res=%+v", tc.wrappers, tc.cmd, c, res)
		}
	}
}

func TestBackgroundedCommandIsUnfinishedAndStopped(t *testing.T) {
	d := testStepDir(t)
	dir := t.TempDir()
	wrapper := `{{STEW_STEP}} >/dev/null 2>&1 & i=0; while [ ! -f started ] && [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done`
	cmd := `touch started; i=0; while [ ! -f release ] && [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done; exit 3`
	res, _, c := runStep(t, d, dir, []string{wrapper}, nil, cmd)
	if c != (collected{1, 0, false}) || res != (Result{}) {
		t.Errorf("c=%+v res=%+v", c, res)
	}
	os.WriteFile(filepath.Join(dir, "release"), nil, 0o600)
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(d.Dir, "p-build.status")); err == nil {
		t.Error("the backgrounded command outlived the step and finished")
	}
}

func TestCollectRemovesStepFiles(t *testing.T) {
	d := testStepDir(t)
	runStep(t, d, t.TempDir(), []string{"a=1 {{STEW_STEP}}", "{{STEW_STEP}}"}, nil, "true")
	if entries, _ := os.ReadDir(d.Dir); len(entries) != 0 {
		t.Errorf("left %v", entries)
	}
}

func TestUnreadableStatusIsAnError(t *testing.T) {
	d := testStepDir(t)
	if _, err := d.Prepare("p-build", []string{"{{STEW_STEP}}"}, nil, "true"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(d.Dir, "p-build.reach"), []byte("\n"), 0o600)
	os.WriteFile(filepath.Join(d.Dir, "p-build.status"), []byte("x\n"), 0o600)
	if _, _, finished, err := d.Collect("p-build"); err == nil || finished {
		t.Errorf("finished=%v err=%v", finished, err)
	}
	if entries, _ := os.ReadDir(d.Dir); len(entries) != 0 {
		t.Errorf("left %v", entries)
	}
}

func TestCollectLeavesOtherStepsFiles(t *testing.T) {
	d := testStepDir(t)
	wrappers := []string{"{{STEW_STEP}}", "{{STEW_STEP}}"}
	if _, err := d.Prepare("p-ci.full.q-setup", wrappers, nil, "true"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Prepare("p-ci.full", wrappers, nil, "true"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := d.Collect("p-ci.full"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"p-ci.full.q-setup.1", "p-ci.full.q-setup.step"} {
		if _, err := os.Stat(filepath.Join(d.Dir, name)); err != nil {
			t.Error(err)
		}
	}
}

func TestPrepareRemovesStaleReachAndStatus(t *testing.T) {
	d := testStepDir(t)
	os.WriteFile(filepath.Join(d.Dir, "p-build.reach"), []byte("\n\n"), 0o600)
	os.WriteFile(filepath.Join(d.Dir, "p-build.status"), []byte("9\n"), 0o600)
	_, _, c := runStep(t, d, t.TempDir(), []string{"true # {{STEW_STEP}}"}, nil, "true")
	if c != (collected{}) {
		t.Errorf("c = %+v", c)
	}
}

func TestUncountableReachDoesNotRunCommand(t *testing.T) {
	d := testStepDir(t)
	argv, err := d.Prepare("p-build", []string{"{{STEW_STEP}}", "{{STEW_STEP}}"}, nil, "touch ran")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(d.Dir, "p-build.reach"), 0o700); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var out bytes.Buffer
	res := Shell{KillDelay: slowKillDelay}.Run(context.Background(), dir, nil, argv, &out, &out)
	if res != (Result{ExitCode: 125}) {
		t.Errorf("res = %+v, out = %q", res, out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "ran")); !os.IsNotExist(err) {
		t.Errorf("command ran: %v", err)
	}
	if _, _, _, err := d.Collect("p-build"); err == nil {
		t.Error("Collect returned no error for an unreadable reach file")
	}
	if entries, _ := os.ReadDir(d.Dir); len(entries) != 0 {
		t.Errorf("left %v", entries)
	}
	if len(d.levels) != 0 {
		t.Errorf("levels = %v", d.levels)
	}
}

func TestUnexecutableStepScriptIsExit126(t *testing.T) {
	d := testStepDir(t)
	argv, err := d.Prepare("p-build", []string{"{{STEW_STEP}}"}, nil, "true")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(d.Dir, "p-build.step"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	res := Shell{KillDelay: slowKillDelay}.Run(context.Background(), t.TempDir(), nil, argv, &out, &out)
	n, _, finished, err := d.Collect("p-build")
	if err != nil || n != 0 || finished || res != (Result{ExitCode: 126}) {
		t.Errorf("n=%d res=%+v err=%v out=%q", n, res, err, out.String())
	}
}

func TestFailedPrepareForgetsKey(t *testing.T) {
	d := testStepDir(t)
	level := filepath.Join(d.Dir, "p-build.1")
	if err := os.Mkdir(level, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := d.Prepare("p-build", []string{"{{STEW_STEP}}", "{{STEW_STEP}}"}, nil, "true")
	if err == nil || !strings.Contains(err.Error(), level) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(d.Dir, "p-build.step")); !os.IsNotExist(err) {
		t.Errorf("step script left: %v", err)
	}
	if len(d.levels) != 0 {
		t.Errorf("levels = %v", d.levels)
	}
	if entries, _ := os.ReadDir(d.Dir); len(entries) != 0 {
		t.Errorf("left %v", entries)
	}
}

func TestRemoveDeletesDir(t *testing.T) {
	d := testStepDir(t)
	if err := d.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(d.Dir); !os.IsNotExist(err) {
		t.Errorf("dir still exists: %v", err)
	}
}
