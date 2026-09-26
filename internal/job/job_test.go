package job

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"
)

type ran struct {
	status syscall.WaitStatus
	err    error
}

// A signal stew gets reaches the job's group exactly once. Counting what the job receives cannot show this: two
// copies sent back to back merge into one, so this test counts what stew passes on.
func TestRunPassesEachSignalOnce(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	var passed []syscall.Signal
	pass := func(pgid int, sig syscall.Signal) {
		mu.Lock()
		passed = append(passed, sig)
		mu.Unlock()
		syscall.Kill(-pgid, sig)
	}
	cmd := `trap 'exit 3' USR1; touch started; i=0; while [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done`
	done := make(chan ran, 1)
	go func() {
		status, err := run(nil, dir, []string{"sh", "-c", cmd}, os.Environ(), pass)
		done <- ran{status, err}
	}()
	waitForFile(t, filepath.Join(dir, "started"))
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	var r ran
	select {
	case r = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the job did not exit after SIGUSR1")
	}
	if r.err != nil || !r.status.Exited() || r.status.ExitStatus() != 3 {
		t.Errorf("status = %v, err = %v; want exit 3", r.status, r.err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(passed, []syscall.Signal{syscall.SIGUSR1}) {
		t.Errorf("passed %v, want SIGUSR1 once", passed)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s after 10 s", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
