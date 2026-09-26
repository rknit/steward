package main

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// pagerEnv is the test's environment without pager variables, plus kv.
func pagerEnv(kv ...string) []string {
	env := slices.DeleteFunc(os.Environ(), func(e string) bool {
		key, _, _ := strings.Cut(e, "=")
		return key == "STEW_PAGER" || key == "PAGER" || key == "LESS"
	})
	return append(env, kv...)
}

func TestPagerCommand(t *testing.T) {
	t.Parallel()
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
		proc := process{env: pagerEnv("STEW_PAGER="+tt.stewPager, "PAGER="+tt.pager)}
		if got := pagerCommand(proc); got != tt.want {
			t.Errorf("STEW_PAGER=%q PAGER=%q: pagerCommand() = %q, want %q", tt.stewPager, tt.pager, got, tt.want)
		}
	}
}

func pageTo(t *testing.T, env []string, out []byte, tty bool) (string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	proc := process{dir: t.TempDir(), env: env, loc: time.UTC, stdout: &stdout, stderr: &stderr}
	if err := page(proc, out, tty); err != nil {
		t.Fatalf("page: %v", err)
	}
	return stdout.String(), stderr.String()
}

func TestPageNotTTY(t *testing.T) {
	t.Parallel()
	if got, _ := pageTo(t, pagerEnv("STEW_PAGER=echo pager-ran"), []byte("a\nb\n"), false); got != "a\nb\n" {
		t.Errorf("stdout = %q", got)
	}
}

func TestPageDisabled(t *testing.T) {
	t.Parallel()
	if got, _ := pageTo(t, pagerEnv("STEW_PAGER=cat"), []byte("a\n"), true); got != "a\n" {
		t.Errorf("stdout = %q", got)
	}
}

func TestPageThroughPager(t *testing.T) {
	t.Parallel()
	if got, _ := pageTo(t, pagerEnv(`STEW_PAGER=sed 's/^/> /'`), []byte("a\nb\n"), true); got != "> a\n> b\n" {
		t.Errorf("stdout = %q", got)
	}
}

func TestPageLessDefault(t *testing.T) {
	t.Parallel()
	const printLess = `STEW_PAGER=cat > /dev/null; printf 'LESS=%s\n' "$LESS"`
	if got, _ := pageTo(t, pagerEnv(printLess), []byte("x\n"), true); got != "LESS=FRX\n" {
		t.Errorf("unset LESS: stdout = %q", got)
	}
	if got, _ := pageTo(t, pagerEnv(printLess, "LESS=R"), []byte("x\n"), true); got != "LESS=R\n" {
		t.Errorf("LESS=R: stdout = %q", got)
	}
}

func TestPageEarlyQuit(t *testing.T) {
	t.Parallel()
	big := bytes.Repeat([]byte("x\n"), 2<<20)
	if got, _ := pageTo(t, pagerEnv("STEW_PAGER=head -c 1"), big, true); got != "x" {
		t.Errorf("stdout = %q", got)
	}
}

func TestPageMissingPager(t *testing.T) {
	t.Parallel()
	got, errOut := pageTo(t, pagerEnv("STEW_PAGER=stew-test-no-such-pager"), []byte("a\n"), true)
	if got != "a\n" {
		t.Errorf("stdout = %q, want the page written directly", got)
	}
	if !strings.Contains(errOut, "stew-test-no-such-pager") {
		t.Errorf("stderr = %q, want the shell's error", errOut)
	}
}
