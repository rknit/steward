package runner

import (
	"context"
	"testing"
	"time"
)

// TestShellReapsAdoptedLeftovers checks that once stew adopts orphans, a stopped leftover is reaped by stew itself:
// otherwise its zombie keeps the process group alive until KillDelay passes.
func TestShellReapsAdoptedLeftovers(t *testing.T) {
	dir := t.TempDir()
	start := time.Now()
	res, _, _ := runShell(t, context.Background(), slowKillDelay, dir, "sleep 30 > /dev/null 2>&1 & echo $! > pid")
	if !res.OK() {
		t.Errorf("result = %+v", res)
	}
	if took := time.Since(start); took >= 2*time.Second {
		t.Errorf("took %v; want the stopped leftover reaped at once, not a wait for KillDelay (%v)", took, slowKillDelay)
	}
	if !gone(t, dir) {
		t.Error("background process outlived the step")
	}
}
