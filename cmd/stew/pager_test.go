package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestPagerCommand(t *testing.T) {
	tests := []struct {
		stewPager, pager, want string
	}{
		{"", "", "less"},
		{"more", "", "more"},
		{"", "most", "most"},
		{"less -S", "cat", "less -S"},
		{"cat", "", ""},
		{"", "cat", ""},
	}
	for _, tt := range tests {
		t.Setenv("STEW_PAGER", tt.stewPager)
		t.Setenv("PAGER", tt.pager)
		if got := pagerCommand(); got != tt.want {
			t.Errorf("STEW_PAGER=%q PAGER=%q: pagerCommand() = %q, want %q", tt.stewPager, tt.pager, got, tt.want)
		}
	}
}

func pageTo(t *testing.T, out []byte, tty bool) (string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if err := page(&stdout, &stderr, out, tty); err != nil {
		t.Fatalf("page: %v", err)
	}
	return stdout.String(), stderr.String()
}

func TestPageNotTTY(t *testing.T) {
	t.Setenv("STEW_PAGER", "echo pager-ran")
	if got, _ := pageTo(t, []byte("a\nb\n"), false); got != "a\nb\n" {
		t.Errorf("stdout = %q", got)
	}
}

func TestPageDisabled(t *testing.T) {
	t.Setenv("STEW_PAGER", "cat")
	if got, _ := pageTo(t, []byte("a\n"), true); got != "a\n" {
		t.Errorf("stdout = %q", got)
	}
}

func TestPageThroughPager(t *testing.T) {
	t.Setenv("STEW_PAGER", `sed 's/^/> /'`)
	if got, _ := pageTo(t, []byte("a\nb\n"), true); got != "> a\n> b\n" {
		t.Errorf("stdout = %q", got)
	}
}

func TestPageLessDefault(t *testing.T) {
	t.Setenv("STEW_PAGER", `cat > /dev/null; printf 'LESS=%s\n' "$LESS"`)
	t.Setenv("LESS", "")
	os.Unsetenv("LESS")
	if got, _ := pageTo(t, []byte("x\n"), true); got != "LESS=FRX\n" {
		t.Errorf("unset LESS: stdout = %q", got)
	}
	t.Setenv("LESS", "R")
	if got, _ := pageTo(t, []byte("x\n"), true); got != "LESS=R\n" {
		t.Errorf("LESS=R: stdout = %q", got)
	}
}

func TestPageEarlyQuit(t *testing.T) {
	t.Setenv("STEW_PAGER", "head -c 1")
	big := bytes.Repeat([]byte("x\n"), 2<<20)
	if got, _ := pageTo(t, big, true); got != "x" {
		t.Errorf("stdout = %q", got)
	}
}

func TestPageMissingPager(t *testing.T) {
	t.Setenv("STEW_PAGER", "stew-test-no-such-pager")
	got, errOut := pageTo(t, []byte("a\n"), true)
	if got != "a\n" {
		t.Errorf("stdout = %q, want the page written directly", got)
	}
	if !strings.Contains(errOut, "stew-test-no-such-pager") {
		t.Errorf("stderr = %q, want the shell's error", errOut)
	}
}

func TestPageSurvivesCtrlC(t *testing.T) {
	t.Setenv("STEW_PAGER", `kill -INT $PPID; sleep 0.2; cat`)
	if got, _ := pageTo(t, []byte("a\n"), true); got != "a\n" {
		t.Errorf("stdout = %q", got)
	}
}
