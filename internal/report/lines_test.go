package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/rknit/steward/internal/runner"
)

var (
	_ runner.Reporter = (*Plain)(nil)
	_ runner.Reporter = (*TTY)(nil)
)

func TestFormatDuration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0.0s"},
		{-time.Second, "0.0s"},
		{49 * time.Millisecond, "0.0s"},
		{10460 * time.Millisecond, "10.5s"},
		{59940 * time.Millisecond, "59.9s"},
		{59960 * time.Millisecond, "1m0s"},
		{62400 * time.Millisecond, "1m2s"},
		{3600 * time.Second, "1h0m0s"},
		{3912 * time.Second, "1h5m12s"},
	}
	for _, tt := range tests {
		if got := FormatDuration(tt.d); got != tt.want {
			t.Errorf("FormatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestPlainSectionLines(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	p := &Plain{W: &b}
	sec := func(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }

	coreSetup := runner.Section{Project: "core", Name: "setup"}
	apiBuild := runner.Section{Project: "api", Name: "build"}
	webCiFull := runner.Section{Project: "web", Name: "ci.full"}
	appBuild := runner.Section{Project: "app", Name: "build"}
	libSetup := runner.Section{Project: "lib", Name: "setup"}
	xBuild := runner.Section{Project: "x", Name: "build"}

	p.SectionStart(coreSetup)
	p.SectionEnd(coreSetup, runner.Outcome{Status: runner.Skip, Duration: sec(0.1)})
	p.SectionStart(apiBuild)
	p.SectionEnd(apiBuild, runner.Outcome{Status: runner.Done, Duration: sec(63)})
	p.SectionStart(webCiFull)
	p.SectionEnd(webCiFull, runner.Outcome{
		Status: runner.Fail, Duration: sec(2.7), Cause: "exit 2",
		Steps: []runner.StepOutput{
			{Step: "run", Cmd: "npm test", Output: []byte("out\nno newline")},
			{Step: "verify", Cmd: "test -f x", Output: nil},
		},
	})
	p.Blocked(appBuild, []string{"api:build", "backend:build"})
	p.SectionStart(libSetup)
	p.SectionEnd(libSetup, runner.Outcome{Status: runner.Fail, Cause: "log error: disk full"})
	p.SectionStart(xBuild)
	p.SectionEnd(xBuild, runner.Outcome{
		Status: runner.Interrupted, Duration: sec(5), Cause: "signal SIGINT",
		Steps: []runner.StepOutput{{Step: "run", Cmd: "make", Output: []byte("partial\n")}},
	})

	want := `==> core: setup ... skip (0.1s)
==> api: build ... done (1m3s)
==> web: ci.full ... fail (2.7s)
--- stew: run: npm test
out
no newline
--- stew: verify: test -f x
(exit 2)
==> app: build ... blocked by api:build, backend:build
==> lib: setup ... fail (0.0s)
(log error: disk full)
==> x: build ... interrupted
--- stew: run: make
partial
(signal SIGINT)
`
	if got := b.String(); got != want {
		t.Errorf("output:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(b.String(), "\x1b") {
		t.Error("plain output contains escape codes")
	}
}

func TestTTYAnimation(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	tick := make(chan time.Time)
	stopped := false
	tty := newTTY(&b, func() (<-chan time.Time, func()) { return tick, func() { stopped = true } })

	coreSetup := runner.Section{Project: "core", Name: "setup"}
	apiSetup := runner.Section{Project: "api", Name: "setup"}

	tty.SectionStart(coreSetup)
	for range 4 {
		tick <- time.Time{}
	}
	tty.SectionEnd(coreSetup, runner.Outcome{Status: runner.Done, Duration: time.Second})
	tty.Blocked(apiSetup, []string{"core:setup"})

	const clr = "\r\x1b[K"
	want := clr + "==> core: setup ." +
		clr + "==> core: setup .." +
		clr + "==> core: setup ..." +
		clr + "==> core: setup ." +
		clr + "==> core: setup .." +
		clr + "==> core: setup ... done (1.0s)\n" +
		"==> api: setup ... blocked by core:setup\n"
	if got := b.String(); got != want {
		t.Errorf("output:\n%q\nwant:\n%q", got, want)
	}
	if !stopped {
		t.Error("ticker not stopped")
	}
}

func TestTTYFailureContent(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	tty := newTTY(&b, func() (<-chan time.Time, func()) { return nil, func() {} })
	coreBuild := runner.Section{Project: "core", Name: "build"}
	tty.SectionStart(coreBuild)
	tty.SectionEnd(coreBuild, runner.Outcome{
		Status: runner.Fail, Duration: 2 * time.Second, Cause: "cannot start: no such file",
		Steps: []runner.StepOutput{{Step: "run", Cmd: "make", Output: []byte("x\n")}},
	})
	want := "\r\x1b[K==> core: build .\r\x1b[K==> core: build ... fail (2.0s)\n--- stew: run: make\nx\n(cannot start: no such file)\n"
	if got := b.String(); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}
