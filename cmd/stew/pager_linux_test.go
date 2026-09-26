package main

import "testing"

// The pager sends Ctrl-C's SIGINT to stew, this test binary, and keeps running until the signal is no longer pending
// in /proc, that is, until stew has taken it while the pager runs. If it is still pending, the pager fails without
// printing, and the test fails.
func TestPageSurvivesCtrlC(t *testing.T) {
	const pending = `grep -q '^ShdPnd:.*[2367abef]$' /proc/$PPID/status`
	const pager = `STEW_PAGER=kill -INT $PPID; ` +
		`i=0; while ` + pending + ` && [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done; ` + pending + ` && exit 1; cat`
	if got, _ := pageTo(t, pagerEnv(pager), []byte("a\n"), true); got != "a\n" {
		t.Errorf("stdout = %q", got)
	}
}
