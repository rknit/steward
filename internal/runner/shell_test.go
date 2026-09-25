package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runShell(t *testing.T, ctx context.Context, dir, cmd string) (Result, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	res := Shell{KillDelay: 2 * time.Second}.Run(ctx, dir, cmd, &stdout, &stderr)
	return res, stdout.String(), stderr.String()
}

func TestShellExitAndStreams(t *testing.T) {
	dir := t.TempDir()
	res, out, errOut := runShell(t, context.Background(), dir, "pwd; echo oops >&2; exit 3")
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
	res, out, _ := runShell(t, context.Background(), t.TempDir(), "cat; echo eof")
	if !res.OK() || out != "eof\n" {
		t.Errorf("result = %+v, stdout = %q", res, out)
	}
}

func TestShellCannotStart(t *testing.T) {
	res, _, _ := runShell(t, context.Background(), filepath.Join(t.TempDir(), "missing"), "true")
	if res.Err == nil {
		t.Errorf("result = %+v, want start error", res)
	}
}

func TestShellAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(ErrInterrupted)
	res, _, _ := runShell(t, ctx, t.TempDir(), "touch ran")
	if !errors.Is(res.Err, ErrInterrupted) {
		t.Errorf("result = %+v", res)
	}
}

// TestShellCancelKillsGroup checks SIGTERM reaches the whole process group, including a grandchild.
// The grandchild inherits stdout, so if it survived, Run would wait out WaitDelay (KillDelay, 2s) for the pipe to close.
func TestShellCancelKillsGroup(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancelCause(context.Background())
	cancelledAt := make(chan time.Time, 1)
	go func() {
		waitForFile(t, filepath.Join(dir, "started"))
		cancelledAt <- time.Now()
		cancel(errors.New("log error"))
	}()
	res, _, _ := runShell(t, ctx, dir, "sh -c 'sleep 30' & touch started; wait")
	if res.Signal != "SIGTERM" {
		t.Errorf("result = %+v, want SIGTERM", res)
	}
	if took := time.Since(<-cancelledAt); took >= time.Second {
		t.Errorf("Run returned %v after cancel; the grandchild kept stdout open, so it was not signalled", took)
	}
}

func TestShellCancelEscalatesToKill(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancelCause(context.Background())
	go func() {
		waitForFile(t, filepath.Join(dir, "started"))
		cancel(errors.New("log error"))
	}()
	res, _, _ := runShell(t, ctx, dir, "trap '' TERM; touch started; while :; do sleep 0.1; done")
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
	res, out, _ := runShell(t, ctx, dir, "trap 'echo got-int; exit 7' INT; touch started; while :; do sleep 0.1; done")
	if res != (Result{ExitCode: 7}) || out != "got-int\n" {
		t.Errorf("result = %+v, stdout = %q", res, out)
	}
}

func TestShellBackgroundProcessDoesNotHang(t *testing.T) {
	start := time.Now()
	res, out, _ := runShell(t, context.Background(), t.TempDir(), "sleep 5 & echo started")
	if !res.OK() || out != "started\n" {
		t.Errorf("result = %+v, stdout = %q", res, out)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("took %v", time.Since(start))
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
