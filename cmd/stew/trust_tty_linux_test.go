package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// trustSession is a shell session whose workspace has a trust command that appends to the file "trusted".
func trustSession(t *testing.T) *shellSession {
	t.Helper()
	s := newShellSession(t)
	config := "workspace_wrapper = \"\"\nworkspace_trust = \"echo trusted >> trusted\"\n"
	if err := os.WriteFile(filepath.Join(s.dir, ".stew/config.toml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTrustPromptYes(t *testing.T) {
	t.Parallel()
	s := trustSession(t)
	s.send(`stew exec 'echo "R-$((6*7))"'; echo "rc=$((0+$?))"` + "\n")
	s.expect(`│ workspace │ echo trusted >> trusted │`)
	s.expect(`run these trust commands\? \[y/N\] `)
	s.send("y\n")
	s.expect(`R-42`)
	if rc := s.expect(`rc=(\d+)`)[1]; rc != "0" {
		t.Errorf("exit = %s, want 0", rc)
	}
	if n := s.lines("trusted"); n != 1 {
		t.Errorf("trust command ran %d times, want 1", n)
	}

	// stew trust lists and runs every entry, recorded or not.
	s.send(`stew trust; echo "rc=$((0+$?))"` + "\n")
	s.expect(`│ workspace │ echo trusted >> trusted │`)
	s.expect(`\[y/N\] `)
	s.send("yes\n")
	if rc := s.expect(`rc=(\d+)`)[1]; rc != "0" {
		t.Errorf("stew trust exit = %s, want 0", rc)
	}
	if n := s.lines("trusted"); n != 2 {
		t.Errorf("trust command ran %d times, want 2", n)
	}
}

func TestTrustPromptDecline(t *testing.T) {
	t.Parallel()
	for name, answer := range map[string]string{"no": "n\n", "empty": "\n", "eof": "\x04"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := trustSession(t)
			s.send(`stew exec 'echo "R-$((6*7))"'; echo "rc=$((0+$?))"` + "\n")
			s.expect(`\[y/N\] `)
			s.send(answer)
			s.expect(`stew: trust declined`)
			if rc := s.expect(`rc=(\d+)`)[1]; rc != "1" {
				t.Errorf("exit = %s, want 1", rc)
			}
			if _, err := os.Stat(filepath.Join(s.dir, "trusted")); err == nil {
				t.Error("trust command ran after a decline")
			}
		})
	}
}

func TestTrustPromptInterrupt(t *testing.T) {
	t.Parallel()
	s := trustSession(t)
	s.send(`stew exec 'echo "R-$((6*7))"'; echo "rc=$((0+$?))"` + "\n")
	s.expect(`\[y/N\] `)
	s.send("\x03")
	m := s.expect(`(?s)(.*)rc=(\d+)`)
	if m[2] != "130" {
		t.Errorf("exit = %s, want 130", m[2])
	}
	if strings.Contains(m[1], "stew:") || strings.Contains(m[1], "R-42") {
		t.Errorf("stew reported an error or ran the command after Ctrl-C:\n%s", m[1])
	}
	if _, err := os.Stat(filepath.Join(s.dir, "trusted")); err == nil {
		t.Error("trust command ran after Ctrl-C")
	}
}

func TestTrustPromptPipedStdout(t *testing.T) {
	t.Parallel()
	s := trustSession(t)
	s.send(`stew exec 'echo "R-$((6*7))"' | cat; echo "rc=$((0+${PIPESTATUS[0]}))"` + "\n")
	s.expect(`stew: untrusted: workspace \(run: stew trust\)`)
	if rc := s.expect(`rc=(\d+)`)[1]; rc != "1" {
		t.Errorf("exit = %s, want 1", rc)
	}
}
