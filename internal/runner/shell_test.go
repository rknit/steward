package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// boundedLoop keeps a command busy for at most 10 s, so a regression fails a test instead of hanging the suite.
const boundedLoop = "i=0; while [ $i -lt 100 ]; do sleep 0.1; i=$((i+1)); done"

const (
	// slowKillDelay never elapses in a passing test; waiting it out means the behavior under test broke.
	slowKillDelay = 10 * time.Second
	// fastKillDelay is for tests that wait out KillDelay on purpose.
	fastKillDelay = 50 * time.Millisecond
)

func runShell(t *testing.T, ctx context.Context, killDelay time.Duration, dir, cmd string) (Result, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	res := Shell{KillDelay: killDelay}.Run(ctx, dir, nil, []string{"sh", "-c", cmd}, &stdout, &stderr)
	return res, stdout.String(), stderr.String()
}

func TestShellExitAndStreams(t *testing.T) {
	dir := t.TempDir()
	res, out, errOut := runShell(t, context.Background(), slowKillDelay, dir, "pwd; echo oops >&2; exit 3")
	if res != (Result{ExitCode: 3}) {
		t.Errorf("result = %+v", res)
	}
	real, _ := filepath.EvalSymlinks(dir)
	if strings.TrimSpace(out) != real && strings.TrimSpace(out) != dir {
		t.Errorf("stdout = %q, want %q", out, dir)
	}
	if errOut != "oops\n" {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestShellStdinIsDevNull(t *testing.T) {
	res, out, _ := runShell(t, context.Background(), slowKillDelay, t.TempDir(), "cat; echo eof")
	if !res.OK() || out != "eof\n" {
		t.Errorf("result = %+v, stdout = %q", res, out)
	}
}

func TestShellEnvOverridesInherited(t *testing.T) {
	t.Setenv("STEW_TAG", "parent:build")
	t.Setenv("STEW_TEST_KEPT", "kept")
	var stdout, stderr bytes.Buffer
	res := Shell{KillDelay: slowKillDelay}.Run(context.Background(), t.TempDir(), []string{"STEW_TAG=child:setup"},
		[]string{"sh", "-c", `env | grep -c '^STEW_TAG='; echo "$STEW_TAG $STEW_TEST_KEPT"`}, &stdout, &stderr)
	if !res.OK() || stdout.String() != "1\nchild:setup kept\n" {
		t.Errorf("result = %+v, stdout = %q, stderr = %q", res, stdout.String(), stderr.String())
	}
}

func TestShellCannotStart(t *testing.T) {
	res, _, _ := runShell(t, context.Background(), slowKillDelay, filepath.Join(t.TempDir(), "missing"), "true")
	if res.Err == nil {
		t.Errorf("result = %+v, want start error", res)
	}
}

func TestShellAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(ErrInterrupted)
	res, _, _ := runShell(t, ctx, slowKillDelay, t.TempDir(), "touch ran")
	if !errors.Is(res.Err, ErrInterrupted) {
		t.Errorf("result = %+v", res)
	}
}

// TestShellCancelKillsGroup checks SIGTERM reaches the whole process group, including a grandchild.
func TestShellCancelKillsGroup(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancelCause(context.Background())
	cancelledAt := make(chan time.Time, 1)
	go func() {
		waitForFile(t, filepath.Join(dir, "started"))
		cancelledAt <- time.Now()
		cancel(errors.New("log error"))
	}()
	res, _, _ := runShell(t, ctx, slowKillDelay, dir,
		`sh -c 'trap "touch grandchild-term; exit" TERM; touch started; sleep 30 & wait' & wait`)
	if res.Signal != "SIGTERM" {
		t.Errorf("result = %+v, want SIGTERM", res)
	}
	if took := time.Since(<-cancelledAt); took >= time.Second {
		t.Errorf("Run returned %v after cancel; the grandchild kept stdout open", took)
	}
	waitForFile(t, filepath.Join(dir, "grandchild-term"))
}

func TestShellCancelEscalatesToKill(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancelCause(context.Background())
	go func() {
		waitForFile(t, filepath.Join(dir, "started"))
		cancel(errors.New("log error"))
	}()
	res, _, _ := runShell(t, ctx, fastKillDelay, dir, "trap '' TERM; touch started; "+boundedLoop)
	if res.Signal != "SIGKILL" {
		t.Errorf("result = %+v, want SIGKILL", res)
	}
}

func TestShellInterruptSendsSIGINT(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancelCause(context.Background())
	go func() {
		waitForFile(t, filepath.Join(dir, "started"))
		cancel(ErrInterrupted)
	}()
	res, out, _ := runShell(t, ctx, slowKillDelay, dir,
		"trap 'echo got-int; exit 7' INT; touch started; "+boundedLoop)
	if res != (Result{ExitCode: 7}) || out != "got-int\n" {
		t.Errorf("result = %+v, stdout = %q", res, out)
	}
}

func TestShellInterruptSendsSIGTERM(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancelCause(context.Background())
	go func() {
		waitForFile(t, filepath.Join(dir, "started"))
		cancel(Interrupt{Signal: syscall.SIGTERM})
	}()
	res, out, _ := runShell(t, ctx, slowKillDelay, dir,
		"trap 'echo got-term; exit 7' TERM; touch started; "+boundedLoop)
	if res != (Result{ExitCode: 7}) || out != "got-term\n" {
		t.Errorf("result = %+v, stdout = %q", res, out)
	}
}

func TestShellInterruptSIGTERMEscalatesToKill(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancelCause(context.Background())
	go func() {
		waitForFile(t, filepath.Join(dir, "started"))
		cancel(Interrupt{Signal: syscall.SIGTERM})
	}()
	res, _, _ := runShell(t, ctx, fastKillDelay, dir, "trap '' TERM; touch started; "+boundedLoop)
	if res.Signal != "SIGKILL" {
		t.Errorf("result = %+v, want SIGKILL", res)
	}
}

// TestShellBackgroundProcessDoesNotHang checks Run stops waiting on stdout held open by a background process once
// WaitDelay (KillDelay) passes.
func TestShellBackgroundProcessDoesNotHang(t *testing.T) {
	start := time.Now()
	res, out, _ := runShell(t, context.Background(), fastKillDelay, t.TempDir(), "sleep 5 & echo started")
	if !res.OK() || out != "started\n" {
		t.Errorf("result = %+v, stdout = %q", res, out)
	}
	if took := time.Since(start); took >= 2*time.Second {
		t.Errorf("took %v; want Run to return once WaitDelay (%v) passes", took, fastKillDelay)
	}
}

func waitForFile(t *testing.T, path string) {
	for range 200 {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Errorf("timed out waiting for %s", path)
}

// TestShellForceKillsAfterInterrupt checks that closing Force SIGKILLs a command that survives the forwarded SIGINT.
func TestShellForceKillsAfterInterrupt(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancelCause(context.Background())
	force := make(chan struct{})
	go func() {
		waitForFile(t, filepath.Join(dir, "started"))
		cancel(ErrInterrupted)
		waitForFile(t, filepath.Join(dir, "got-int"))
		close(force)
	}()
	var stdout, stderr bytes.Buffer
	start := time.Now()
	res := Shell{KillDelay: slowKillDelay, Force: force}.Run(ctx, dir, nil,
		[]string{"sh", "-c", "trap 'touch got-int' INT; touch started; " + boundedLoop}, &stdout, &stderr)
	if res.Signal != "SIGKILL" {
		t.Errorf("result = %+v, want SIGKILL", res)
	}
	if took := time.Since(start); took >= 2*time.Second {
		t.Errorf("took %v; want the force kill well before the command's own 10 s loop", took)
	}
}

// TestShellForceSkipsKillDelay checks that closing Force during a SIGTERM stop kills at once instead of waiting
// out KillDelay.
func TestShellForceSkipsKillDelay(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancelCause(context.Background())
	force := make(chan struct{})
	go func() {
		waitForFile(t, filepath.Join(dir, "started"))
		cancel(Interrupt{Signal: syscall.SIGTERM})
		waitForFile(t, filepath.Join(dir, "got-term"))
		close(force)
	}()
	var stdout, stderr bytes.Buffer
	start := time.Now()
	res := Shell{KillDelay: slowKillDelay, Force: force}.Run(ctx, dir, nil,
		[]string{"sh", "-c", "trap 'touch got-term' TERM; touch started; " + boundedLoop}, &stdout, &stderr)
	if res.Signal != "SIGKILL" {
		t.Errorf("result = %+v, want SIGKILL", res)
	}
	if took := time.Since(start); took >= 2*time.Second {
		t.Errorf("took %v; want the force kill well before KillDelay (%v)", took, slowKillDelay)
	}
}

// gone reports whether the process whose pid is in dir/pid has exited, waiting up to 2 s for it to be reaped.
func gone(t *testing.T, dir string) bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	for range 200 {
		if syscall.Kill(pid, 0) != nil {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	syscall.Kill(pid, syscall.SIGKILL)
	return false
}

func TestShellStopsLeftoverProcesses(t *testing.T) {
	dir := t.TempDir()
	res, _, _ := runShell(t, context.Background(), slowKillDelay, dir, "sleep 30 > /dev/null 2>&1 & echo $! > pid")
	if !res.OK() {
		t.Errorf("result = %+v", res)
	}
	if !gone(t, dir) {
		t.Error("background process outlived the step")
	}
}

func TestShellKillsLeftoverThatIgnoresSIGTERM(t *testing.T) {
	dir := t.TempDir()
	start := time.Now()
	res, _, _ := runShell(t, context.Background(), fastKillDelay, dir,
		`sh -c 'trap "" TERM; echo $$ > pid; exec sleep 30' > /dev/null 2>&1 & `+
			`i=0; while [ ! -s pid ] && [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done`)
	if !res.OK() {
		t.Errorf("result = %+v", res)
	}
	if !gone(t, dir) {
		t.Error("background process that ignores SIGTERM outlived the step")
	}
	if took := time.Since(start); took < fastKillDelay || took >= 2*time.Second {
		t.Errorf("took %v; want SIGKILL once KillDelay (%v) passes", took, fastKillDelay)
	}
}

func TestShellNoLeftoversNoDelay(t *testing.T) {
	start := time.Now()
	res, _, _ := runShell(t, context.Background(), slowKillDelay, t.TempDir(), "true")
	if !res.OK() {
		t.Errorf("result = %+v", res)
	}
	if took := time.Since(start); took >= time.Second {
		t.Errorf("took %v; want no wait when the step left nothing running", took)
	}
}
