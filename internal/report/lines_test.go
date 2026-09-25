package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/rknit/steward/internal/runner"
)

func TestFormatDuration(t *testing.T) {
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

var (
	setup = runner.Phase{Name: "setup", Used: "setup"}
	build = runner.Phase{Name: "build", Used: "build"}
	ciFB  = runner.Phase{Name: "ci.pre-commit", Used: "ci.quick", CI: true}
	ciOK  = runner.Phase{Name: "ci.full", Used: "ci.full", CI: true}
)

func TestPlainPhaseLines(t *testing.T) {
	var b bytes.Buffer
	p := &Plain{W: &b}
	sec := func(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }

	p.PhaseStart("core", setup)
	p.PhaseEnd("core", setup, runner.Outcome{Status: runner.Skip, Duration: sec(0.1)})
	p.PhaseStart("api", build)
	p.PhaseEnd("api", build, runner.Outcome{Status: runner.Done, Duration: sec(63)})
	p.PhaseStart("api", ciFB)
	p.PhaseEnd("api", ciFB, runner.Outcome{Status: runner.Pass, Duration: sec(8.2)})
	p.PhaseStart("web", ciOK)
	p.PhaseEnd("web", ciOK, runner.Outcome{
		Status: runner.Fail, Duration: sec(2.7), Cause: "exit 2",
		Steps: []runner.StepOutput{
			{Step: "run", Cmd: "npm test", Output: []byte("out\nno newline")},
			{Step: "verify after run", Cmd: "test -f x", Output: nil},
		},
	})
	p.Blocked("app", setup, []string{"api", "backend"})
	p.PhaseStart("lib", setup)
	p.PhaseEnd("lib", setup, runner.Outcome{Status: runner.Fail, Cause: "log error: disk full"})
	p.PhaseStart("x", build)
	p.PhaseEnd("x", build, runner.Outcome{
		Status: runner.Interrupted, Duration: sec(5), Cause: "signal SIGINT",
		Steps: []runner.StepOutput{{Step: "run", Cmd: "make", Output: []byte("partial\n")}},
	})

	want := `==> core: setup ... skip (0.1s)
==> api: build ... done (1m3s)
==> api: ci.pre-commit -> ci.quick ... pass (8.2s)
==> web: ci.full ... fail (2.7s)
--- stew: run: npm test
out
no newline
--- stew: verify after run: test -f x
(exit 2)
==> app: setup ... blocked by api, backend
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
	var b bytes.Buffer
	tick := make(chan time.Time)
	stopped := false
	tty := newTTY(&b, func() (<-chan time.Time, func()) { return tick, func() { stopped = true } })

	tty.PhaseStart("core", setup)
	for range 4 {
		tick <- time.Time{}
	}
	tty.PhaseEnd("core", setup, runner.Outcome{Status: runner.Done, Duration: time.Second})
	tty.Blocked("api", setup, []string{"core"})

	const clr = "\r\x1b[K"
	want := clr + "==> core: setup ." +
		clr + "==> core: setup .." +
		clr + "==> core: setup ..." +
		clr + "==> core: setup ." +
		clr + "==> core: setup .." +
		clr + "==> core: setup ... done (1.0s)\n" +
		"==> api: setup ... blocked by core\n"
	if got := b.String(); got != want {
		t.Errorf("output:\n%q\nwant:\n%q", got, want)
	}
	if !stopped {
		t.Error("ticker not stopped")
	}
}

func TestTTYFailureContent(t *testing.T) {
	var b bytes.Buffer
	tty := newTTY(&b, func() (<-chan time.Time, func()) { return nil, func() {} })
	tty.PhaseStart("core", build)
	tty.PhaseEnd("core", build, runner.Outcome{
		Status: runner.Fail, Duration: 2 * time.Second, Cause: "cannot start: no such file",
		Steps: []runner.StepOutput{{Step: "run", Cmd: "make", Output: []byte("x\n")}},
	})
	want := "\r\x1b[K==> core: build .\r\x1b[K==> core: build ... fail (2.0s)\n--- stew: run: make\nx\n(cannot start: no such file)\n"
	if got := b.String(); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}
