package main

import "testing"

// The pager sends Ctrl-C's SIGINT to stew, this test binary, and keeps running until the signal is no longer pending
// in /proc, that is, until stew has taken it while the pager runs.
func TestPageSurvivesCtrlC(t *testing.T) {
	const pager = `STEW_PAGER=kill -INT $PPID; ` +
		`i=0; while grep -q '^ShdPnd:.*[2367abef]$' /proc/$PPID/status && [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done; cat`
	if got, _ := pageTo(t, pagerEnv(pager), []byte("a\n"), true); got != "a\n" {
		t.Errorf("stdout = %q", got)
	}
}
