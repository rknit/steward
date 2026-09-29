package report

import (
	"bytes"
	"slices"
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

	want := `==> core: setup ... started
==> core: setup ... skip (0.1s)
==> api: build ... started
==> api: build ... done (1m3s)
==> web: ci.full ... started
==> web: ci.full ... fail (2.7s)
--- stew: run: npm test
out
no newline
--- stew: verify: test -f x
(exit 2)
==> app: build ... blocked by api:build, backend:build
==> lib: setup ... started
==> lib: setup ... fail (0.0s)
(log error: disk full)
==> x: build ... started
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

// writes records each Write call separately.
type writes struct{ calls []string }

func (w *writes) Write(p []byte) (int, error) {
	w.calls = append(w.calls, string(p))
	return len(p), nil
}

func TestPlainWritesEachEndInOneWrite(t *testing.T) {
	t.Parallel()
	var w writes
	p := &Plain{W: &w}
	s := runner.Section{Project: "core", Name: "build"}
	p.SectionEnd(s, runner.Outcome{
		Status: runner.Fail, Duration: time.Second, Cause: "exit 1",
		Steps: []runner.StepOutput{{Step: "run", Cmd: "make", Output: []byte("boom\n")}},
	})
	want := []string{"==> core: build ... fail (1.0s)\n--- stew: run: make\nboom\n(exit 1)\n"}
	if !slices.Equal(w.calls, want) {
		t.Errorf("writes = %q, want %q", w.calls, want)
	}
}

func noSize() (int, int, bool) { return 0, 0, false }

func noTicks() (<-chan time.Time, func()) { return nil, func() {} }

func build(project string) runner.Section { return runner.Section{Project: project, Name: "build"} }

func TestTTYAnimation(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	tick := make(chan time.Time)
	stopped := false
	tty := newTTY(&b, func() (<-chan time.Time, func()) { return tick, func() { stopped = true } }, noSize)

	tty.SectionStart(runner.Section{Project: "core", Name: "setup"})
	for range 4 {
		tick <- time.Time{}
	}
	tty.SectionEnd(runner.Section{Project: "core", Name: "setup"}, runner.Outcome{Status: runner.Done, Duration: time.Second})
	tty.Blocked(runner.Section{Project: "api", Name: "setup"}, []string{"core:setup"})

	const clr = "\r\x1b[J"
	want := clr + "==> core: setup ." +
		clr + "==> core: setup .." +
		clr + "==> core: setup ..." +
		clr + "==> core: setup ." +
		clr + "==> core: setup .." +
		clr + "==> core: setup ... done (1.0s)\n" +
		clr + "==> api: setup ... blocked by core:setup\n"
	if got := b.String(); got != want {
		t.Errorf("output:\n%q\nwant:\n%q", got, want)
	}
	if !stopped {
		t.Error("ticker not stopped")
	}
}

func TestTTYFooter(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	tty := newTTY(&b, noTicks, noSize)
	var got []string
	step := func(f func()) {
		b.Reset()
		f()
		got = append(got, b.String())
	}

	step(func() { tty.SectionStart(build("a")) })
	step(func() { tty.SectionStart(build("b")) })
	step(func() { tty.SectionStart(build("c")) })
	step(func() { tty.SectionEnd(build("b"), runner.Outcome{Status: runner.Done, Duration: time.Second}) })
	step(func() { tty.Blocked(build("d"), []string{"x:y"}) })
	step(func() {
		tty.SectionEnd(build("a"), runner.Outcome{
			Status: runner.Fail, Duration: 2 * time.Second, Cause: "exit 1",
			Steps: []runner.StepOutput{{Step: "run", Cmd: "make", Output: []byte("x\n")}},
		})
	})
	step(func() { tty.SectionEnd(build("c"), runner.Outcome{Status: runner.Done, Duration: 3 * time.Second}) })

	want := []string{
		"\r\x1b[J==> a: build .",
		"\r\x1b[J==> a: build .\n==> b: build .",
		"\r\x1b[1A\x1b[J==> a: build .\n==> b: build .\n==> c: build .",
		"\r\x1b[2A\x1b[J==> b: build ... done (1.0s)\n==> a: build .\n==> c: build .",
		"\r\x1b[1A\x1b[J==> d: build ... blocked by x:y\n==> a: build .\n==> c: build .",
		"\r\x1b[1A\x1b[J==> a: build ... fail (2.0s)\n--- stew: run: make\nx\n(exit 1)\n==> c: build .",
		"\r\x1b[J==> c: build ... done (3.0s)\n",
	}
	if !slices.Equal(got, want) {
		t.Errorf("output:\n%q\nwant:\n%q", got, want)
	}
}

func TestTTYFitsTerminal(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	tty := newTTY(&b, noTicks, func() (int, int, bool) { return 20, 3, true })
	long := build("a-very-long-project")
	var got []string
	step := func(f func()) {
		b.Reset()
		f()
		got = append(got, b.String())
	}

	step(func() { tty.SectionStart(long) })
	step(func() { tty.SectionStart(build("b")) })
	step(func() { tty.SectionStart(build("c")) })
	step(func() { tty.SectionEnd(long, runner.Outcome{Status: runner.Done, Duration: time.Second}) })

	want := []string{
		"\r\x1b[J==> a-very-long-pro",
		"\r\x1b[J==> a-very-long-pro\n==> b: build .",
		"\r\x1b[1A\x1b[J==> a-very-long-pro\n... 2 more running",
		"\r\x1b[1A\x1b[J==> a-very-long-project: build ... done (1.0s)\n==> b: build .\n==> c: build .",
	}
	if !slices.Equal(got, want) {
		t.Errorf("output:\n%q\nwant:\n%q", got, want)
	}
}

func TestTTYReadsSizeEachRedraw(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	width := 100
	tty := newTTY(&b, noTicks, func() (int, int, bool) { return width, 10, true })
	tty.SectionStart(build("core"))
	width = 8
	b.Reset()
	tty.SectionStart(build("api"))
	if want := "\r\x1b[J==> cor\n==> api"; b.String() != want {
		t.Errorf("output = %q, want %q", b.String(), want)
	}
}
